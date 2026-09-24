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
