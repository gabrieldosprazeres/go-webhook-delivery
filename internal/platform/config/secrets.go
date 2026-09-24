package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
)

const (
	secretFileMaxBytes = 64 << 10
	keyMaterialBytes   = 32
)

func applySecretPaths(cfg *Config, lookup func(string) (string, bool)) {
	cfg.Secrets.AuthPepper, _ = envValue(lookup, "WDE_AUTH_PEPPER_FILE")
	cfg.Secrets.IdempotencyPepper, _ = envValue(lookup, "WDE_IDEMPOTENCY_PEPPER_FILE")
	cfg.Secrets.FingerprintPepper, _ = envValue(lookup, "WDE_FINGERPRINT_PEPPER_FILE")
	cfg.Secrets.RateLimitPepper, _ = envValue(lookup, "WDE_RATE_LIMIT_PEPPER_FILE")
	cfg.Secrets.CursorPepper, _ = envValue(lookup, "WDE_CURSOR_PEPPER_FILE")
	cfg.Secrets.PayloadKeyring, _ = envValue(lookup, "WDE_PAYLOAD_KEYRING_FILE")
	cfg.Secrets.SigningKeyring, _ = envValue(lookup, "WDE_SIGNING_KEYRING_FILE")
}

func validateProductionSecrets(cfg Config) error {
	if cfg.Service == ServiceAPI {
		if err := validateAPIPeppers(cfg.Secrets); err != nil {
			return err
		}
	}
	payloadKeys, err := validateKeyringFile("WDE_PAYLOAD_KEYRING_FILE", cfg.Secrets.PayloadKeyring)
	if err != nil {
		return err
	}
	signingKeys, err := validateKeyringFile("WDE_SIGNING_KEYRING_FILE", cfg.Secrets.SigningKeyring)
	if err != nil {
		return err
	}
	for material := range payloadKeys {
		if _, reused := signingKeys[material]; reused {
			return errors.New("config: payload and signing keyrings must use distinct key material")
		}
	}
	return nil
}

func validateAPIPeppers(files SecretFiles) error {
	auth, err := validatePepperFile("WDE_AUTH_PEPPER_FILE", files.AuthPepper)
	if err != nil {
		return err
	}
	idempotency, err := validatePepperFile("WDE_IDEMPOTENCY_PEPPER_FILE", files.IdempotencyPepper)
	if err != nil {
		return err
	}
	fingerprint, err := validatePepperFile("WDE_FINGERPRINT_PEPPER_FILE", files.FingerprintPepper)
	if err != nil {
		return err
	}
	rateLimit, err := validatePepperFile("WDE_RATE_LIMIT_PEPPER_FILE", files.RateLimitPepper)
	if err != nil {
		return err
	}
	cursor, err := validatePepperFile("WDE_CURSOR_PEPPER_FILE", files.CursorPepper)
	if err != nil {
		return err
	}
	seen := make(map[[keyMaterialBytes]byte]struct{}, 5)
	for _, material := range [][keyMaterialBytes]byte{auth, idempotency, fingerprint, rateLimit, cursor} {
		if _, reused := seen[material]; reused {
			return errors.New("config: authentication, idempotency, fingerprint, rate-limit and cursor peppers must use distinct key material")
		}
		seen[material] = struct{}{}
	}
	return nil
}

func readSecretFile(name, path string) ([]byte, error) {
	info, err := validateSecretPath(name, path)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("config: %s cannot be read", name)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) {
		return nil, fmt.Errorf("config: %s changed while being opened", name)
	}
	contents, err := io.ReadAll(io.LimitReader(file, secretFileMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("config: %s cannot be read", name)
	}
	if len(contents) == 0 || len(contents) > secretFileMaxBytes {
		return nil, fmt.Errorf("config: %s has an invalid size", name)
	}
	return contents, nil
}

func validateSecretPath(name, path string) (os.FileInfo, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("config: %s is required in production", name)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("config: %s cannot be read", name)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("config: %s must reference a regular file, not a symlink", name)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("config: %s permissions must be at most 0600", name)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return nil, fmt.Errorf("config: %s must be owned by the process user", name)
	}
	return info, nil
}

func validatePepperFile(name, path string) ([keyMaterialBytes]byte, error) {
	var material [keyMaterialBytes]byte
	contents, err := readSecretFile(name, path)
	if err != nil {
		return material, err
	}
	defer clear(contents)
	encoded := strings.TrimSpace(string(contents))
	if !strings.HasPrefix(encoded, "v1:") {
		return material, fmt.Errorf("config: %s must use the v1 pepper format", name)
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(encoded, "v1:"))
	if err != nil || len(decoded) != keyMaterialBytes {
		clear(decoded)
		return material, fmt.Errorf("config: %s must contain exactly 32 bytes of base64url key material", name)
	}
	copy(material[:], decoded)
	clear(decoded)
	return material, nil
}
