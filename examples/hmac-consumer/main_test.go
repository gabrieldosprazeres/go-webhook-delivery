package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/signing"
)

func TestHandlerVerifiesBodyAndTimestamp(t *testing.T) {
	secret := bytes.Repeat([]byte{7}, 32)
	body := []byte(`{"demo":true}`)
	now := time.Now()
	valid := request(now.Unix(), signing.Sign(secret, now.Unix(), "evt", "del", "key-demo", body), body)
	response := httptest.NewRecorder()
	handler("key-demo", secret).ServeHTTP(response, valid)
	if response.Code != http.StatusNoContent {
		t.Fatalf("valid signature status = %d", response.Code)
	}

	tampered := request(now.Unix(), signing.Sign(secret, now.Unix(), "evt", "del", "key-demo", body), []byte(`{"demo":false}`))
	response = httptest.NewRecorder()
	handler("key-demo", secret).ServeHTTP(response, tampered)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("tampered body status = %d", response.Code)
	}

	stale := now.Add(-6 * time.Minute).Unix()
	response = httptest.NewRecorder()
	handler("key-demo", secret).ServeHTTP(response,
		request(stale, signing.Sign(secret, stale, "evt", "del", "key-demo", body), body))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("stale timestamp status = %d", response.Code)
	}
}

func request(timestamp int64, signature string, body []byte) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	request.Header.Set(signing.HeaderVersion, "v1")
	request.Header.Set(signing.HeaderTimestamp, strconv.FormatInt(timestamp, 10))
	request.Header.Set(signing.HeaderEventID, "evt")
	request.Header.Set(signing.HeaderDeliveryID, "del")
	request.Header.Set(signing.HeaderSignature, signing.HeaderValue("key-demo", signature))
	return request
}
