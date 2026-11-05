package http

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/willydocksha/go-clean-arch/internal/delivery/http/handler"
	"github.com/willydocksha/go-clean-arch/internal/delivery/http/middleware"
	"github.com/willydocksha/go-clean-arch/internal/delivery/http/response"
	"github.com/willydocksha/go-clean-arch/internal/domain"
)

// route registrar implemented by each api handler, mounted under /api/v1
type Registrar interface {
	Register(r chi.Router)
}

// router options
type RouterConfig struct {
	Logger         *slog.Logger
	AllowedOrigins []string
}

// build the root http handler with routes and middlewares
func NewRouter(cfg RouterConfig, health *handler.HealthHandler, api ...Registrar) http.Handler {
	r := chi.NewRouter()

	// global middlewares, outermost first
	r.Use(
		middleware.RequestID,
		middleware.Logger(cfg.Logger),
		middleware.Recoverer(cfg.Logger),
		middleware.CORS(cfg.AllowedOrigins),
	)

	// json responses for unknown routes and wrong methods
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		response.Error(w, req, fmt.Errorf("%w: route %s %s", domain.ErrNotFound, req.Method, req.URL.Path))
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		response.MethodNotAllowed(w, req)
	})

	// probes live at the root
	health.Register(r)

	// versioned api; add per-group middleware (e.g. auth) inside handlers or here
	r.Route("/api/v1", func(v1 chi.Router) {
		for _, h := range api {
			h.Register(v1)
		}
	})
	return r
}

// compile-time checks that handlers satisfy registrar
var (
	_ Registrar = (*handler.UserHandler)(nil)
	_ Registrar = (*handler.ProductHandler)(nil)
	_ Registrar = (*handler.FileHandler)(nil)
)
