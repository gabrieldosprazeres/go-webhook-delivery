package chaoslab

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/signing"
)

func TestDeterministicScenariosAndSafeState(t *testing.T) {
	handler := Handler()
	for index, want := range []int{503, 503, 204} {
		response := request(t, handler, http.MethodPost, "/fail-n?failures=2", "CANARY-payload")
		if response.Code != want {
			t.Fatalf("fail-N call %d status = %d, want %d", index+1, response.Code, want)
		}
	}
	rateLimited := request(t, handler, http.MethodPost, "/rate-limit?retry_after=7", "CANARY-payload")
	if rateLimited.Code != http.StatusTooManyRequests || rateLimited.Header().Get("Retry-After") != "7" {
		t.Fatalf("unexpected rate-limit response: status=%d headers=%v", rateLimited.Code, rateLimited.Header())
	}
	if response := request(t, handler, http.MethodPost, "/permanent-failure", "CANARY-payload"); response.Code != 400 {
		t.Fatalf("permanent status = %d", response.Code)
	}
	state := request(t, handler, http.MethodGet, "/state", "")
	if strings.Contains(state.Body.String(), "CANARY") || !strings.Contains(state.Body.String(), `"fail_n_then_succeed":3`) {
		t.Fatalf("unsafe or incomplete state: %s", state.Body.String())
	}
	if reset := request(t, handler, http.MethodPost, "/reset", ""); reset.Code != 204 {
		t.Fatalf("reset status = %d", reset.Code)
	}
	if state := request(t, handler, http.MethodGet, "/state", ""); !strings.Contains(state.Body.String(), `"counters":{}`) {
		t.Fatalf("state not reset: %s", state.Body.String())
	}
}

func TestTimeoutHonorsCancellation(t *testing.T) {
	handler := Handler()
	request := httptest.NewRequest(http.MethodPost, "/timeout?delay_ms=1000", nil)
	ctx, cancel := context.WithTimeout(request.Context(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	handler.ServeHTTP(httptest.NewRecorder(), request.WithContext(ctx))
	if time.Since(started) > 250*time.Millisecond {
		t.Fatal("timeout scenario ignored request cancellation")
	}
}

func TestVerifySignatureRequiresEveryConfiguredRotationKey(t *testing.T) {
	handler := Handler()
	secrets := [][]byte{bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)}
	keyIDs := []string{"key-old-0001", "key-new-0002"}
	for index := range secrets {
		body := fmt.Sprintf(`{"key_id":%q,"secret":%q}`, keyIDs[index],
			base64.RawURLEncoding.EncodeToString(secrets[index]))
		if response := request(t, handler, http.MethodPost, "/configure", body); response.Code != 204 {
			t.Fatalf("configure status = %d body=%s", response.Code, response.Body.String())
		}
	}
	payload := []byte(`{"safe":"value"}`)
	timestamp := time.Now().Unix()
	header := signing.HeaderValue(keyIDs[0], signing.Sign(secrets[0], timestamp, "evt", "del", keyIDs[0], payload))
	one := signedRequest(handler, timestamp, header, payload)
	if one.Code != http.StatusUnauthorized {
		t.Fatalf("single signature status = %d, want 401", one.Code)
	}
	header += ";" + signing.HeaderValue(keyIDs[1], signing.Sign(secrets[1], timestamp, "evt", "del", keyIDs[1], payload))
	dual := signedRequest(handler, timestamp, header, payload)
	if dual.Code != http.StatusNoContent {
		t.Fatalf("dual signature status = %d body=%s", dual.Code, dual.Body.String())
	}
	state := request(t, handler, http.MethodGet, "/state", "")
	for _, secret := range []string{base64.RawURLEncoding.EncodeToString(secrets[0]), base64.RawURLEncoding.EncodeToString(secrets[1]), string(payload)} {
		if strings.Contains(state.Body.String(), secret) {
			t.Fatalf("state leaked secret/payload: %s", state.Body.String())
		}
	}
}

func TestConfigureHasBoundedSecretSet(t *testing.T) {
	handler := Handler()
	secret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	for index := 0; index < maxConfiguredSecrets; index++ {
		body := fmt.Sprintf(`{"key_id":"key-limit-%02d","secret":%q}`, index, secret)
		if response := request(t, handler, http.MethodPost, "/configure", body); response.Code != http.StatusNoContent {
			t.Fatalf("configure %d status = %d body=%s", index, response.Code, response.Body.String())
		}
	}
	body := fmt.Sprintf(`{"key_id":"key-limit-extra","secret":%q}`, secret)
	if response := request(t, handler, http.MethodPost, "/configure", body); response.Code != http.StatusConflict {
		t.Fatalf("overflow status = %d, want 409; body=%s", response.Code, response.Body.String())
	}

	// Replacing an existing key remains possible without growing the secret set.
	body = fmt.Sprintf(`{"key_id":"key-limit-00","secret":%q}`, secret)
	if response := request(t, handler, http.MethodPost, "/configure", body); response.Code != http.StatusNoContent {
		t.Fatalf("replacement status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestTimeoutScenarioRejectsAboveConcurrencyCapWithoutLeakingWork(t *testing.T) {
	state := &state{secrets: make(map[string][]byte), counters: make(map[string]uint64),
		slots: make(chan struct{}, maxConcurrentScenarios)}
	handler := newHandler(state)
	var group sync.WaitGroup
	group.Add(maxConcurrentScenarios)
	for range maxConcurrentScenarios {
		go func() {
			defer group.Done()
			response := request(t, handler, http.MethodPost, "/timeout?delay_ms=200", "")
			if response.Code != http.StatusNoContent {
				t.Errorf("admitted timeout status = %d", response.Code)
			}
		}()
	}
	deadline := time.Now().Add(time.Second)
	for {
		state.mu.RLock()
		active := state.active
		state.mu.RUnlock()
		if active == maxConcurrentScenarios {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scenario workers did not reach concurrency cap")
		}
		time.Sleep(time.Millisecond)
	}
	started := time.Now()
	rejected := request(t, handler, http.MethodPost, "/timeout?delay_ms=200", "")
	if rejected.Code != http.StatusTooManyRequests || rejected.Header().Get("Retry-After") != "1" {
		t.Fatalf("overflow response status=%d headers=%v", rejected.Code, rejected.Header())
	}
	if time.Since(started) > 50*time.Millisecond {
		t.Fatal("overflow request did not fail fast")
	}
	group.Wait()
	state.mu.RLock()
	defer state.mu.RUnlock()
	if state.active != 0 || state.peak != maxConcurrentScenarios || len(state.slots) != 0 {
		t.Fatalf("scenario capacity leaked: active=%d peak=%d slots=%d", state.active, state.peak, len(state.slots))
	}
}

func signedRequest(handler http.Handler, timestamp int64, signature string, payload []byte) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/verify-signature", bytes.NewReader(payload))
	request.Header.Set(signing.HeaderVersion, "v1")
	request.Header.Set(signing.HeaderTimestamp, strconv.FormatInt(timestamp, 10))
	request.Header.Set(signing.HeaderEventID, "evt")
	request.Header.Set(signing.HeaderDeliveryID, "del")
	request.Header.Set(signing.HeaderSignature, signature)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func request(t *testing.T, handler http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(method, target, strings.NewReader(body)))
	return response
}
