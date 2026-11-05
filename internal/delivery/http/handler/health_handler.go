package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/willydocksha/go-clean-arch/internal/delivery/http/response"
)

// pinger abstracts any dependency that can be health-checked
type Pinger interface {
	Ping(ctx context.Context) error
}

// health and readiness endpoints
type HealthHandler struct {
	deps map[string]Pinger
}

// constructor for health handler
func NewHealthHandler(deps map[string]Pinger) *HealthHandler {
	return &HealthHandler{deps: deps}
}

// register health routes on the router
func (h *HealthHandler) Register(r chi.Router) {
	r.Get("/healthz", h.Liveness)
	r.Get("/readyz", h.Readiness)
}

// liveness: process is up
func (h *HealthHandler) Liveness(w http.ResponseWriter, _ *http.Request) {
	response.JSON(w, http.StatusOK, map[string]string{"status": "ok"}, nil)
}

// readiness: all dependencies respond
func (h *HealthHandler) Readiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	// ping each dependency
	status, code := map[string]string{}, http.StatusOK
	for name, dep := range h.deps {
		if err := dep.Ping(ctx); err != nil {
			status[name], code = "down", http.StatusServiceUnavailable
			continue
		}
		status[name] = "up"
	}
	response.JSON(w, code, status, nil)
}
