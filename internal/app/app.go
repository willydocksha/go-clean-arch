package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/willydocksha/go-clean-arch/internal/config"
	deliveryhttp "github.com/willydocksha/go-clean-arch/internal/delivery/http"
	"github.com/willydocksha/go-clean-arch/internal/delivery/http/handler"
	"github.com/willydocksha/go-clean-arch/internal/domain"
	"github.com/willydocksha/go-clean-arch/internal/repository/postgres"
	"github.com/willydocksha/go-clean-arch/internal/usecase"
	"github.com/willydocksha/go-clean-arch/migrations"
	"github.com/willydocksha/go-clean-arch/pkg/cache"
	"github.com/willydocksha/go-clean-arch/pkg/database"
	"github.com/willydocksha/go-clean-arch/pkg/migrator"
	"github.com/willydocksha/go-clean-arch/pkg/storage"
)

// composition root: wires all layers and runs the server
func Run(cfg *config.Config, log *slog.Logger) error {
	// cancel on sigint / sigterm
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// postgres (required)
	pool, err := database.NewPostgres(ctx, database.PostgresConfig{
		URL:      cfg.Database.URL,
		MaxConns: cfg.Database.MaxConns,
		MinConns: cfg.Database.MinConns,
	})
	if err != nil {
		return err
	}
	defer pool.Close()
	deps := map[string]handler.Pinger{"postgres": pool}

	// optional auto migration
	if cfg.Database.AutoMigrate {
		if err := autoMigrate(ctx, log, pool); err != nil {
			return err
		}
	}

	// redis (optional): failures degrade to no cache instead of crashing
	var appCache domain.Cache = cache.Noop{}
	if cfg.Redis.Enabled() {
		rdb, err := cache.NewRedis(ctx, cache.RedisConfig{
			Addr: cfg.Redis.Addr, Password: cfg.Redis.Password, DB: cfg.Redis.DB, KeyPrefix: cfg.Redis.KeyPrefix,
		})
		if err != nil {
			log.Warn("redis unavailable, running without cache", "error", err)
		} else {
			defer rdb.Close()
			appCache = rdb
			deps["redis"] = rdb
		}
	} else {
		log.Info("redis disabled (REDIS_ADDR empty), running without cache")
	}

	// s3 (optional): file endpoints return 503 when disabled
	var fileStorage domain.FileStorage
	if cfg.S3.Enabled() {
		s3c, err := storage.NewS3(ctx, storage.S3Config{
			Bucket:          cfg.S3.Bucket,
			Region:          cfg.S3.Region,
			Endpoint:        cfg.S3.Endpoint,
			PresignEndpoint: cfg.S3.PresignEndpoint,
			AccessKeyID:     cfg.S3.AccessKeyID,
			SecretAccessKey: cfg.S3.SecretAccessKey,
			ForcePathStyle:  cfg.S3.ForcePathStyle,
		})
		if err != nil {
			log.Warn("s3 unavailable, file storage disabled", "error", err)
		} else {
			fileStorage = s3c
			deps["s3"] = s3c
			// create bucket on startup for local minio setups
			if cfg.S3.CreateBucket {
				if err := s3c.EnsureBucket(ctx); err != nil {
					log.Warn("could not ensure s3 bucket", "bucket", cfg.S3.Bucket, "error", err)
				}
			}
		}
	} else {
		log.Info("s3 disabled (S3_BUCKET empty), storage endpoints will return 503")
	}

	// repositories
	userRepo := postgres.NewUserRepository(pool)
	productRepo := postgres.NewProductRepository(pool)

	// usecases
	userUC := usecase.NewUserUsecase(userRepo, cfg.App.ContextTimeout)
	productUC := usecase.NewProductUsecase(productRepo, appCache, cfg.App.ContextTimeout)
	fileUC := usecase.NewFileUsecase(fileStorage, cfg.S3.PresignExpiry, cfg.App.ContextTimeout)

	// delivery
	router := deliveryhttp.NewRouter(
		deliveryhttp.RouterConfig{Logger: log, AllowedOrigins: cfg.HTTP.AllowedOrigins},
		handler.NewHealthHandler(deps),
		handler.NewUserHandler(userUC),
		handler.NewProductHandler(productUC, fileUC, cfg.HTTP.MaxUploadSize),
		handler.NewFileHandler(fileUC, cfg.HTTP.MaxUploadSize),
	)

	// configure http server
	srv := &http.Server{
		Addr:         net.JoinHostPort("", cfg.HTTP.Port),
		Handler:      router,
		ReadTimeout:  cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout,
		IdleTimeout:  cfg.HTTP.IdleTimeout,
	}

	// start server in background
	errCh := make(chan error, 1)
	go func() {
		log.Info("http server started", "addr", srv.Addr, "env", cfg.App.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	// wait for shutdown signal or server error
	select {
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		log.Info("shutdown signal received")
	}

	// graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	log.Info("server stopped gracefully")
	return nil
}

// apply pending migrations at startup
func autoMigrate(ctx context.Context, log *slog.Logger, pool *pgxpool.Pool) error {
	m, err := migrator.New(pool, migrations.FS)
	if err != nil {
		return err
	}
	applied, err := m.Up(ctx)
	for _, a := range applied {
		log.Info("migration applied", "version", a.Version, "name", a.Name)
	}
	return err
}
