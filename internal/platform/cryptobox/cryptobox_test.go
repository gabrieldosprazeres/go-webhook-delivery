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
