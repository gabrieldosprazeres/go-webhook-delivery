package main

import (
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

func validateOptions(opts options) error {
	if opts.events < 2 || opts.events > 5000 || opts.concurrency < 1 || opts.concurrency > 512 || opts.round < 0 {
		return errors.New("events/concurrency outside safe bounds")
	}
	if opts.credential == "" || opts.timeout <= 0 || opts.timeout > 30*time.Minute {
		return errors.New("credential and timeout between zero and thirty minutes are required")
	}
	if opts.ingestedSignal == "" || opts.deliveredSignal == "" || opts.databaseStatus == "" || opts.systemSamples == "" {
		return errors.New("private phase and database status paths are required")
	}
	for name, path := range map[string]string{
		"ingested signal":  opts.ingestedSignal,
		"delivered signal": opts.deliveredSignal,
		"database status":  opts.databaseStatus,
		"system samples":   opts.systemSamples,
		"output":           opts.output,
		"CPU profile":      opts.cpuProfile,
		"heap profile":     opts.heapProfile,
	} {
		if err := validateNewPath(name, path); err != nil {
			return err
		}
	}
	info, err := os.Stat(opts.credential)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("credential file must not be accessible by group or others")
	}
	for _, raw := range []string{opts.apiURL, opts.target} {
		parsed, parseErr := url.Parse(raw)
		if parseErr != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return errors.New("API and target must be plain loopback HTTP URLs without credentials/query/fragment")
		}
		host := parsed.Hostname()
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			return errors.New("API and target must use loopback IP literals")
		}
	}
	host, _, err := net.SplitHostPort(opts.listen)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return errors.New("receiver must bind to a loopback IP literal")
	}
	return nil
}

func validateNewPath(name, path string) error {
	if path == "" {
		return nil
	}
	if !filepath.IsAbs(path) {
		return errors.New(name + " path must be absolute")
	}
	if _, err := os.Stat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
		return errors.New(name + " path must not exist")
	}
	return nil
}

func writeSignal(path string) error {
	if path == "" {
		return nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	return file.Close()
}

func loopbackListen(address string) (net.Listener, error) {
	return net.Listen("tcp", address)
}
