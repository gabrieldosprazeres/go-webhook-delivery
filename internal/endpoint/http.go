package endpoint

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/auth"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/problem"
	"github.com/google/uuid"
)

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") != "application/json" {
		problem.Write(w, r, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	var input CreateInput
	if err := decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		problem.Write(w, r, http.StatusBadRequest, "invalid_request", "Invalid request")
		return
	}
	principal, _ := auth.PrincipalFrom(r.Context())
	created, err := h.service.Create(r.Context(), principal.WorkspaceID, input)
	if errors.Is(err, ErrInvalid) {
		problem.Write(w, r, http.StatusUnprocessableEntity, "invalid_endpoint", "Invalid endpoint")
		return
	}
	if errors.Is(err, ErrUnavailable) {
		problem.Write(w, r, http.StatusServiceUnavailable, "endpoint_creation_unavailable", "Endpoint creation unavailable")
		return
	}
	if err != nil {
		problem.Write(w, r, http.StatusInternalServerError, "internal_error", "Internal server error")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		problem.Write(w, r, http.StatusNotFound, "not_found", "Resource not found")
		return
	}
	principal, _ := auth.PrincipalFrom(r.Context())
	value, err := h.service.Get(r.Context(), principal.WorkspaceID, id)
	if errors.Is(err, ErrNotFound) {
		problem.Write(w, r, http.StatusNotFound, "not_found", "Resource not found")
		return
	}
	if err != nil {
		problem.Write(w, r, http.StatusInternalServerError, "internal_error", "Internal server error")
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
