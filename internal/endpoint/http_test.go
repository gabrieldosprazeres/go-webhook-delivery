package endpoint

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
)

func TestCreateProblemDetails(t *testing.T) {
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	handler := NewHandler(NewService(&memoryStore{}, config.ProfileTest, true, materials))
	request := httptest.NewRequest(http.MethodPost, "/v1/endpoints", strings.NewReader(`{"url":"http://example.com/hook","event_types":["x"]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.Create(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/problem+json" || !strings.Contains(response.Body.String(), `"code":"invalid_endpoint"`) {
		t.Fatalf("headers=%v body=%s", response.Header(), response.Body.String())
	}
}
