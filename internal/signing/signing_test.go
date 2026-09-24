package signing

import (
	"testing"
	"time"
)

func TestHMACV1VectorAndTampering(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	body := []byte(`{"amount":1999,"currency":"BRL"}`)
	timestamp := int64(1700000000)
	signature := Sign(secret, timestamp, "018bcfe5-6800-7000-8000-000000000001", "018bcfe5-6800-7000-8000-000000000002", "key_demo", body)
	const expected = "8dO_3VaIyJTSNsOOo6PdtKcPCPh8G6Ky5-wTDnZm7No"
	if signature != expected {
		t.Fatalf("signature=%s", signature)
	}
	now := time.Unix(timestamp, 0)
	header := HeaderValue("key_demo", signature)
	if err := Verify(secret, now, 5*time.Minute, "1700000000", "018bcfe5-6800-7000-8000-000000000001", "018bcfe5-6800-7000-8000-000000000002", "key_demo", header, body); err != nil {
		t.Fatal(err)
	}
	if err := Verify(secret, now, 5*time.Minute, "1700000000", "018bcfe5-6800-7000-8000-000000000001", "018bcfe5-6800-7000-8000-000000000002", "key_demo", header, append(body, ' ')); err == nil {
		t.Fatal("tampered body accepted")
	}
	for name, values := range map[string][5]string{
		"timestamp":   {"1700000001", "018bcfe5-6800-7000-8000-000000000001", "018bcfe5-6800-7000-8000-000000000002", "key_demo", header},
		"event id":    {"1700000000", "changed", "018bcfe5-6800-7000-8000-000000000002", "key_demo", header},
		"delivery id": {"1700000000", "018bcfe5-6800-7000-8000-000000000001", "changed", "key_demo", header},
		"key id":      {"1700000000", "018bcfe5-6800-7000-8000-000000000001", "018bcfe5-6800-7000-8000-000000000002", "changed", header},
		"signature":   {"1700000000", "018bcfe5-6800-7000-8000-000000000001", "018bcfe5-6800-7000-8000-000000000002", "key_demo", HeaderValue("key_demo", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")},
	} {
		t.Run(name, func(t *testing.T) {
			if err := Verify(secret, now, 5*time.Minute, values[0], values[1], values[2], values[3], values[4], body); err == nil {
				t.Fatal("tampered field accepted")
			}
		})
	}
}
