package main

import (
	"log/slog"
	"os"

	"github.com/willydocksha/go-clean-arch/internal/app"
	"github.com/willydocksha/go-clean-arch/internal/config"
	"github.com/willydocksha/go-clean-arch/pkg/logger"
)

// entrypoint: load config, build logger, run app
func main() {
	// load configuration
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// setup structured logger
	log := logger.New(cfg.App.Env, cfg.App.LogLevel)
	slog.SetDefault(log)

	// run the application
	if err := app.Run(cfg, log); err != nil {
		log.Error("application stopped with error", "error", err)
		os.Exit(1)
	}
}
