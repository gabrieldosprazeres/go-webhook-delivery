package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/signing"
)

func TestSuccessRequiresValidConfiguredSignature(t *testing.T) {
	handler := routes()
	secret := bytes.Repeat([]byte{7}, 32)
	keyID := "key_abcdefgh"
	configure := httptest.NewRequest(http.MethodPost, "/configure", bytes.NewBufferString(fmt.Sprintf(`{"key_id":%q,"secret":%q}`, keyID, base64.RawURLEncoding.EncodeToString(secret))))
	configure.Header.Set("Content-Type", "application/json")
	configured := httptest.NewRecorder()
	handler.ServeHTTP(configured, configure)
	if configured.Code != http.StatusNoContent {
		t.Fatalf("configure=%d", configured.Code)
	}
	body := []byte(`{"type":"test","data":{}}`)
	timestamp := time.Now().Unix()
	request := httptest.NewRequest(http.MethodPost, "/success", bytes.NewReader(body))
	request.Header.Set(signing.HeaderVersion, "v1")
	request.Header.Set(signing.HeaderTimestamp, fmt.Sprint(timestamp))
	request.Header.Set(signing.HeaderEventID, "event")
	request.Header.Set(signing.HeaderDeliveryID, "delivery")
	request.Header.Set(signing.HeaderSignature, signing.HeaderValue(keyID, signing.Sign(secret, timestamp, "event", "delivery", keyID, body)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("success=%d body=%s", response.Code, response.Body.String())
	}
	tamperedVersion := httptest.NewRequest(http.MethodPost, "/success", bytes.NewReader(body))
	tamperedVersion.Header = request.Header.Clone()
	tamperedVersion.Header.Set(signing.HeaderVersion, "v2")
	rejected := httptest.NewRecorder()
	handler.ServeHTTP(rejected, tamperedVersion)
	if rejected.Code != http.StatusUnauthorized {
		t.Fatalf("tampered version status=%d", rejected.Code)
	}
}
