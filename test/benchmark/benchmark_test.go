package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/signing"
)

func TestValidateOptionsRequiresLoopbackAndProtectedCredential(t *testing.T) {
	credential := filepath.Join(t.TempDir(), "credential.json")
	if err := os.WriteFile(credential, []byte(`{"api_key":"synthetic"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	valid := options{apiURL: "http://127.0.0.1:18080", target: "http://[::1]:18082/webhook",
		listen: "127.0.0.1:18082", credential: credential, events: 10, concurrency: 2, timeout: time.Second,
		ingestedSignal: filepath.Join(t.TempDir(), "ingested"), deliveredSignal: filepath.Join(t.TempDir(), "delivered"),
		databaseStatus: filepath.Join(t.TempDir(), "database.json"), systemSamples: filepath.Join(t.TempDir(), "system.tsv")}
	if err := validateOptions(valid); err != nil {
		t.Fatal(err)
	}
	remote := valid
	remote.target = "http://8.8.8.8/webhook"
	if validateOptions(remote) == nil {
		t.Fatal("remote benchmark target accepted")
	}
	relativeProfile := valid
	relativeProfile.cpuProfile = "cpu.pprof"
	if validateOptions(relativeProfile) == nil {
		t.Fatal("relative profile path accepted")
	}
	tooLarge := valid
	tooLarge.events = 5001
	if validateOptions(tooLarge) == nil {
		t.Fatal("dataset above hard ceiling accepted")
	}
	if err := os.Chmod(credential, 0o644); err != nil {
		t.Fatal(err)
	}
	if validateOptions(valid) == nil {
		t.Fatal("world-readable credential accepted")
	}
}

func TestReceiverVerifiesSignatureAndCompletesExpectedCount(t *testing.T) {
	secret := []byte("01234567890123456789012345678901")
	receiver := newReceiver()
	if err := receiver.expect([]string{"delivery-0", "delivery-1"}); err != nil {
		t.Fatal(err)
	}
	receiver.configure("key_test", secret)
	defer receiver.clearSecret()
	for index := 0; index < 2; index++ {
		body := []byte(`{"type":"benchmark.delivery","data":{}}`)
		timestamp := time.Now().Unix()
		request := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
		request.Header.Set(signing.HeaderTimestamp, strconv.FormatInt(timestamp, 10))
		request.Header.Set(signing.HeaderEventID, "event")
		request.Header.Set(signing.HeaderDeliveryID, "delivery-"+strconv.Itoa(index))
		request.Header.Set(signing.HeaderSignature, signing.HeaderValue("key_test",
			signing.Sign(secret, timestamp, "event", "delivery-"+strconv.Itoa(index), "key_test", body)))
		response := httptest.NewRecorder()
		receiver.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("response=%d", response.Code)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := receiver.waitAll(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := receiver.snapshot(); err == nil {
		t.Fatal("live receiver allowed a non-final snapshot")
	}
	receiver.quiesce()
	result, err := receiver.snapshot()
	if err != nil || result.unique != 2 || result.duplicates != 0 || result.missing != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestDuplicateCannotMaskMissingDelivery(t *testing.T) {
	secret := []byte("01234567890123456789012345678901")
	receiver := newReceiver()
	receiver.configure("key_test", secret)
	defer receiver.clearSecret()
	if err := receiver.expect([]string{"delivery-0", "delivery-1"}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 2; index++ {
		sendSignedDelivery(t, receiver, secret, "delivery-0")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := receiver.waitAll(ctx); err == nil {
		t.Fatal("duplicate completed expected set")
	}
	receiver.quiesce()
	result, err := receiver.snapshot()
	if err == nil || result.unique != 1 || result.duplicates != 1 || result.missing != 1 {
		t.Fatalf("duplicate masked missing delivery: result=%+v err=%v", result, err)
	}
}

func TestLateCallbacksAreIncludedOnlyAfterQuiescedSnapshot(t *testing.T) {
	secret := []byte("01234567890123456789012345678901")
	receiver := newReceiver()
	receiver.configure("key_test", secret)
	defer receiver.clearSecret()
	if err := receiver.expect([]string{"delivery-0"}); err != nil {
		t.Fatal(err)
	}
	sendSignedDelivery(t, receiver, secret, "delivery-0")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := receiver.waitAll(ctx); err != nil {
		t.Fatal(err)
	}
	sendSignedDelivery(t, receiver, secret, "delivery-0")
	if code := sendDeliveryCode(receiver, secret, "delivery-late", true); code != http.StatusBadRequest {
		t.Fatalf("unexpected ID response=%d", code)
	}
	if code := sendDeliveryCode(receiver, secret, "delivery-0", false); code != http.StatusUnauthorized {
		t.Fatalf("invalid signature response=%d", code)
	}
	receiver.quiesce()
	result, err := receiver.snapshot()
	if err == nil || result.unique != 1 || result.duplicates != 1 || result.unexpected != 1 ||
		result.invalidSignatures != 1 {
		t.Fatalf("late callbacks escaped snapshot: result=%+v err=%v", result, err)
	}
	if code := sendDeliveryCode(receiver, secret, "delivery-0", true); code != http.StatusServiceUnavailable {
		t.Fatalf("quiesced receiver accepted callback: %d", code)
	}
}

func sendSignedDelivery(t *testing.T, receiver *receiver, secret []byte, deliveryID string) {
	t.Helper()
	if code := sendDeliveryCode(receiver, secret, deliveryID, true); code != http.StatusNoContent {
		t.Fatalf("response=%d", code)
	}
}

func sendDeliveryCode(receiver *receiver, secret []byte, deliveryID string, valid bool) int {
	body := []byte(`{"type":"benchmark.delivery","data":{}}`)
	timestamp := time.Now().Unix()
	request := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	request.Header.Set(signing.HeaderTimestamp, strconv.FormatInt(timestamp, 10))
	request.Header.Set(signing.HeaderEventID, "event")
	request.Header.Set(signing.HeaderDeliveryID, deliveryID)
	signature := signing.Sign(secret, timestamp, "event", deliveryID, "key_test", body)
	if !valid {
		signature = "invalid"
	}
	request.Header.Set(signing.HeaderSignature, signing.HeaderValue("key_test", signature))
	response := httptest.NewRecorder()
	receiver.ServeHTTP(response, request)
	return response.Code
}

func TestReportUsesMeasuredRatesAndWritesPrivateFile(t *testing.T) {
	latencies := []time.Duration{time.Millisecond, 2 * time.Millisecond, 20 * time.Millisecond}
	result := buildReport(options{events: 3, concurrency: 2},
		loadResult{duration: time.Second, latencies: latencies},
		deliveryResult{duration: 500 * time.Millisecond, unique: 3},
		queueStatus{Succeeded: 3, Total: 3}, resourceEvidence{})
	if result.Ingestion.RequestsPerSecond != 3 || result.Ingestion.P95Milliseconds != 20 ||
		result.Delivery.RequestsPerSecond != 4 || result.Environment.Go != runtime.Version() {
		t.Fatalf("unexpected report: %+v", result)
	}
	path := filepath.Join(t.TempDir(), "result.json")
	if err := writeReport(path, result); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
	if writeReport(path, result) == nil {
		t.Fatal("existing report overwritten")
	}
}
