package cryptobox

import (
	"bytes"
	"testing"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
)

func TestSealOpenAndAADBinding(t *testing.T) {
	m, err := Load(config.ProfileTest, config.SecretFiles{})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := Seal(m.Payload, []byte("payload"), AAD("one", "two"))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := Open(m.Payload, envelope, AAD("one", "two"))
	if err != nil || !bytes.Equal(plaintext, []byte("payload")) {
		t.Fatalf("open = %q, %v", plaintext, err)
	}
	if _, err := Open(m.Payload, envelope, AAD("one", "changed")); err == nil {
		t.Fatal("expected AAD authentication failure")
	}
}

func TestEnvelopeUsesFreshNonceAndFailsClosed(t *testing.T) {
	m, _ := Load(config.ProfileTest, config.SecretFiles{})
	aad := AAD("2", "tenant", "event_payload", "resource")
	first, err := Seal(m.Payload, []byte("canary"), aad)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Seal(m.Payload, []byte("canary"), aad)
	if err != nil || bytes.Equal(first.Nonce, second.Nonce) || bytes.Equal(first.Ciphertext, second.Ciphertext) {
		t.Fatalf("fresh nonce/ciphertext not produced: err=%v", err)
	}
	tampered := first
	tampered.Ciphertext = append([]byte(nil), first.Ciphertext...)
	tampered.Ciphertext[0] ^= 1
	if _, err := Open(m.Payload, tampered, aad); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	unknown := first
	unknown.FormatVersion = 99
	if _, err := Open(m.Payload, unknown, aad); err == nil {
		t.Fatal("unknown envelope version accepted")
	}
	if _, err := Open(m.Signing, first, aad); err == nil {
		t.Fatal("payload envelope opened by signing keyring")
	}
}

func TestDevelopmentPeppersAreDomainSeparated(t *testing.T) {
	materials, err := Load(config.ProfileTest, config.SecretFiles{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[[32]byte]string{}
	for name, pepper := range map[string][32]byte{
		"auth": materials.AuthPepper, "idempotency": materials.IdempotencyPepper,
		"fingerprint": materials.FingerprintPepper, "rate-limit": materials.RateLimitPepper,
		"cursor": materials.CursorPepper,
	} {
		if previous, duplicate := seen[pepper]; duplicate {
			t.Fatalf("pepper %s reuses %s", name, previous)
		}
		seen[pepper] = name
	}
}
