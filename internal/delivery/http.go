package delivery

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/auth"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/problem"
	"github.com/google/uuid"
)

type Handler struct {
	store  ViewStore
	replay *ReplayService
}

func NewHandler(store ViewStore, replay ...*ReplayService) *Handler {
	handler := &Handler{store: store}
	if len(replay) == 1 {
		handler.replay = replay[0]
	}
	return handler
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		problem.Write(w, r, http.StatusNotFound, "not_found", "Resource not found")
		return
	}
	principal, _ := auth.PrincipalFrom(r.Context())
	details, err := h.store.Get(r.Context(), principal.WorkspaceID, id)
	if errors.Is(err, ErrNotFound) {
		problem.Write(w, r, http.StatusNotFound, "not_found", "Resource not found")
		return
	}
	if err != nil {
		problem.Write(w, r, http.StatusInternalServerError, "internal_error", "Internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(details)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	limit, cursor, err := listParameters(r)
	if err != nil {
		problem.Write(w, r, http.StatusBadRequest, "invalid_page", "Invalid page parameters")
		return
	}
	principal, _ := auth.PrincipalFrom(r.Context())
	result, err := h.store.List(r.Context(), principal.WorkspaceID, limit, cursor)
	if errors.Is(err, ErrInvalidList) {
		problem.Write(w, r, http.StatusBadRequest, "invalid_page", "Invalid page parameters")
		return
	}
	if err != nil {
		problem.Write(w, r, http.StatusInternalServerError, "internal_error", "Internal server error")
		return
	}
	writeDeliveryJSON(w, http.StatusOK, result)
}

func (h *Handler) Replay(w http.ResponseWriter, r *http.Request) {
	deliveryID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		problem.Write(w, r, http.StatusNotFound, "not_found", "Resource not found")
		return
	}
	if r.Header.Get("Content-Type") != "application/json" {
		problem.Write(w, r, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		problem.Write(w, r, http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key is required")
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	var input struct {
		Reason string `json:"reason"`
	}
	if err := decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		problem.Write(w, r, http.StatusBadRequest, "invalid_request", "Invalid request")
		return
	}
	if h.replay == nil {
		problem.Write(w, r, http.StatusInternalServerError, "internal_error", "Internal server error")
		return
	}
	principal, _ := auth.PrincipalFrom(r.Context())
	result, err := h.replay.Request(r.Context(), principal.WorkspaceID, deliveryID,
		principal.APIKeyID, key, input.Reason, problem.RequestID(r.Context()))
	writeReplayResult(w, r, result, err)
}

func writeReplayResult(w http.ResponseWriter, r *http.Request, result ReplayResult, err error) {
	switch {
	case errors.Is(err, ErrInvalidReplay):
		problem.Write(w, r, http.StatusUnprocessableEntity, "invalid_replay", "Invalid replay request")
	case errors.Is(err, ErrReplayConflict):
		problem.Write(w, r, http.StatusConflict, "idempotency_conflict", "Idempotency key was used with different content")
	case errors.Is(err, ErrNotFound):
		problem.Write(w, r, http.StatusNotFound, "not_found", "Resource not found")
	case errors.Is(err, ErrPayloadPurged):
		problem.Write(w, r, http.StatusConflict, "payload_purged", "Payload is no longer available")
	case errors.Is(err, ErrInvalidTransition):
		problem.Write(w, r, http.StatusConflict, "invalid_transition", "Delivery cannot be replayed")
	case err != nil:
		problem.Write(w, r, http.StatusInternalServerError, "internal_error", "Internal server error")
	default:
		writeDeliveryJSON(w, http.StatusAccepted, result)
	}
}

func listParameters(r *http.Request) (int, string, error) {
	query := r.URL.Query()
	for name := range query {
		if name != "limit" && name != "cursor" || len(query[name]) != 1 {
			return 0, "", ErrInvalidList
		}
	}
	limit := 50
	var err error
	if raw := query.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
	}
	if err != nil || limit < 1 || limit > 100 {
		return 0, "", ErrInvalidList
	}
	return limit, query.Get("cursor"), nil
}

func writeDeliveryJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
