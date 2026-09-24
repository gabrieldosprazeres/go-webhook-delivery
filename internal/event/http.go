package event

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/auth"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/problem"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/telemetry"
)

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

func (h *Handler) Publish(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") != "application/json" {
		problem.Write(w, r, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		problem.Write(w, r, http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key is required")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxPayloadBytes))
	if err != nil {
		problem.Write(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "Payload exceeds 1 MiB")
		return
	}
	principal, _ := auth.PrincipalFrom(r.Context())
	result, err := h.service.Publish(r.Context(), principal.WorkspaceID, key, body)
	clear(body)
	if errors.Is(err, ErrInvalid) {
		problem.Write(w, r, http.StatusUnprocessableEntity, "invalid_event", "Invalid event")
		return
	}
	if errors.Is(err, ErrConflict) {
		problem.Write(w, r, http.StatusConflict, "idempotency_conflict", "Idempotency key was used with different content")
		return
	}
	if err != nil {
		problem.Write(w, r, http.StatusInternalServerError, "internal_error", "Internal server error")
		return
	}
	deliveryIDs := make([]string, len(result.DeliveryIDs))
	for index, id := range result.DeliveryIDs {
		deliveryIDs[index] = id.String()
	}
	telemetry.AnnotateAccepted(r.Context(), result.EventID.String(), deliveryIDs)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(result)
}
