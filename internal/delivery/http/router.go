package http

import (
	"log/slog"
	"net/http"

	"github.com/willydocksha/go-clean-arch/internal/delivery/http/handler"
	"github.com/willydocksha/go-clean-arch/internal/delivery/http/middleware"
)

// route registrar implemented by each handler
type Registrar interface {
	Register(mux *http.ServeMux)
}

// router options
type RouterConfig struct {
	Logger         *slog.Logger
	AllowedOrigins []string
}

// build the root http handler with routes and middlewares
func NewRouter(cfg RouterConfig, registrars ...Registrar) http.Handler {
	mux := http.NewServeMux()

	// register all handler routes
	for _, r := range registrars {
		r.Register(mux)
	}

	// wrap with global middlewares
	return middleware.Chain(mux,
		middleware.RequestID,
		middleware.Logger(cfg.Logger),
		middleware.Recoverer(cfg.Logger),
		middleware.CORS(cfg.AllowedOrigins),
	)
}

// compile-time checks that handlers satisfy registrar
var (
	_ Registrar = (*handler.UserHandler)(nil)
	_ Registrar = (*handler.HealthHandler)(nil)
)
