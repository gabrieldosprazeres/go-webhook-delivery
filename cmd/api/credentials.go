package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/auth"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/database"
	"golang.org/x/term"
)

type credentialOptions struct {
	command, name, outputFile, prefix string
}

func runCredentials(ctx context.Context, args []string) error {
	options, err := parseCredentialOptions(args)
	if err != nil {
		return err
	}
	cfg, store, closeStore, err := openCredentialStore(ctx)
	if err != nil {
		return err
	}
	defer closeStore()
	if options.command == "bootstrap" {
		return bootstrapCredential(ctx, cfg.Profile, store, options)
	}
	return revokeCredential(ctx, store, options.prefix)
}

func parseCredentialOptions(args []string) (credentialOptions, error) {
	if len(args) == 0 {
		return credentialOptions{}, errors.New("credentials: command required")
	}
	if args[0] != "bootstrap" && args[0] != "revoke" {
		return credentialOptions{}, errors.New("credentials: unsupported command")
	}
	flags := flag.NewFlagSet("credentials "+args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	name := flags.String("name", "Local workspace", "workspace name")
	outputFile := flags.String("output-file", "", "new file for the credential")
	prefix := flags.String("prefix", "", "API key prefix")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return credentialOptions{}, errors.New("credentials: invalid arguments")
	}
	return credentialOptions{command: args[0], name: strings.TrimSpace(*name), outputFile: *outputFile, prefix: strings.TrimSpace(*prefix)}, nil
}

func openCredentialStore(ctx context.Context) (config.Config, *auth.AdminStore, func(), error) {
	lookupEnv := func(key string) (string, bool) {
		if key == "WDE_DATABASE_URL" {
			if value, ok := os.LookupEnv("WDE_ADMIN_DATABASE_URL"); ok {
				return value, true
			}
		}
		return os.LookupEnv(key)
	}
	cfg, err := config.Load(config.LoadOptions{Service: config.ServiceAPI, LookupEnv: lookupEnv})
	if err != nil {
		return config.Config{}, nil, nil, err
	}
	databaseCtx, cancel := context.WithTimeout(ctx, cfg.DatabaseTimeout)
	defer cancel()
	pool, err := database.Open(databaseCtx, cfg.DatabaseURL, database.RoleAdmin)
	if err != nil {
		return config.Config{}, nil, nil, err
	}
	materials, err := cryptobox.Load(cfg.Profile, cfg.Secrets)
	if err != nil {
		pool.Close()
		return config.Config{}, nil, nil, err
	}
	return cfg, auth.NewAdminStore(pool, materials.AuthPepper, cfg.Profile != config.ProfileProduction), pool.Close, nil
}

func bootstrapCredential(ctx context.Context, profile config.Profile, store *auth.AdminStore, options credentialOptions) error {
	if options.outputFile == "" && !canWriteCredentialToStdout(profile, term.IsTerminal(int(os.Stdout.Fd()))) {
		return errors.New("credentials: --output-file is required unless profile is local and stdout is a TTY")
	}
	_, err := store.Bootstrap(ctx, options.name, credentialSink(options.outputFile))
	return err
}

func credentialSink(outputFile string) auth.CredentialSink {
	return func(result auth.BootstrapResult) (func(), error) {
		encoded, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, '\n')
		defer clear(encoded)
		if outputFile != "" {
			return writeCredentialFile(outputFile, encoded)
		}
		written, err := os.Stdout.Write(encoded)
		if err != nil || written != len(encoded) {
			return nil, errors.New("credentials: failed to write complete credential")
		}
		return nil, nil
	}
}

func revokeCredential(ctx context.Context, store *auth.AdminStore, prefix string) error {
	if prefix == "" {
		return errors.New("credentials: --prefix is required")
	}
	changed, err := store.Revoke(ctx, prefix)
	if err != nil {
		return err
	}
	if !changed {
		return errors.New("credentials: key not found")
	}
	return nil
}

func canWriteCredentialToStdout(profile config.Profile, isTTY bool) bool {
	return profile == config.ProfileLocal && isTTY
}

func writeCredentialFile(path string, contents []byte) (func(), error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("credentials: create output: %w", err)
	}
	remove := func() { _ = os.Remove(path) }
	if err := persistCredential(file, path, contents); err != nil {
		remove()
		return nil, err
	}
	return remove, nil
}

func persistCredential(file *os.File, path string, contents []byte) error {
	written, err := file.Write(contents)
	if err == nil && written != len(contents) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = syncDirectory(filepath.Dir(path))
	}
	if err != nil {
		return fmt.Errorf("credentials: persist output: %w", err)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	if err = directory.Sync(); err != nil {
		_ = directory.Close()
		return err
	}
	return directory.Close()
}
