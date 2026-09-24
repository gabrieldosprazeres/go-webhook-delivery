package delivery

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/auth"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/problem"
	"github.com/google/uuid"
)

type Handler struct{ store Store }

func NewHandler(store Store) *Handler { return &Handler{store: store} }
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
