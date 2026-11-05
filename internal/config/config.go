package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// app configuration loaded from environment variables
type Config struct {
	App      AppConfig
	HTTP     HTTPConfig
	Database DatabaseConfig
	Redis    RedisConfig
	S3       S3Config
}

// general app settings
type AppConfig struct {
	Name           string
	Env            string
	LogLevel       string
	ContextTimeout time.Duration
}

// http server settings
type HTTPConfig struct {
	Port            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
	AllowedOrigins  []string
	MaxUploadSize   int64 // bytes
}

// postgres settings
type DatabaseConfig struct {
	URL         string
	MaxConns    int32
	MinConns    int32
	AutoMigrate bool // run embedded migrations on startup
}

// redis settings; empty addr disables redis (app falls back to no cache)
type RedisConfig struct {
	Addr      string
	Password  string
	DB        int
	KeyPrefix string
}

// enabled when an address is set
func (c RedisConfig) Enabled() bool { return c.Addr != "" }

// s3 settings; empty bucket disables s3 (file endpoints return 503)
type S3Config struct {
	Bucket          string
	Region          string
	Endpoint        string
	PresignEndpoint string
	AccessKeyID     string
	SecretAccessKey string
	ForcePathStyle  bool
	PresignExpiry   time.Duration
	CreateBucket    bool // create bucket on startup (local dev only)
}

// enabled when a bucket is set
func (c S3Config) Enabled() bool { return c.Bucket != "" }

// load config from env with sane defaults
func Load() (*Config, error) {
	// read .env from the working directory when present
	loadDotEnv(".env")

	cfg := &Config{
		App: AppConfig{
			Name:           getEnv("APP_NAME", "go-clean-arch"),
			Env:            getEnv("APP_ENV", "development"),
			LogLevel:       getEnv("LOG_LEVEL", "info"),
			ContextTimeout: getDuration("APP_CONTEXT_TIMEOUT", 5*time.Second),
		},
		HTTP: HTTPConfig{
			Port:            getEnv("HTTP_PORT", "8080"),
			ReadTimeout:     getDuration("HTTP_READ_TIMEOUT", 15*time.Second),
			WriteTimeout:    getDuration("HTTP_WRITE_TIMEOUT", 60*time.Second),
			IdleTimeout:     getDuration("HTTP_IDLE_TIMEOUT", 60*time.Second),
			ShutdownTimeout: getDuration("HTTP_SHUTDOWN_TIMEOUT", 15*time.Second),
			AllowedOrigins:  getList("HTTP_ALLOWED_ORIGINS", []string{"*"}),
			MaxUploadSize:   int64(getInt("HTTP_MAX_UPLOAD_MB", 10)) << 20,
		},
		Database: DatabaseConfig{
			URL:         getEnv("DATABASE_URL", ""),
			MaxConns:    int32(getInt("DB_MAX_CONNS", 10)),
			MinConns:    int32(getInt("DB_MIN_CONNS", 2)),
			AutoMigrate: getBool("DB_AUTO_MIGRATE", false),
		},
		Redis: RedisConfig{
			Addr:      getEnv("REDIS_ADDR", ""),
			Password:  getEnv("REDIS_PASSWORD", ""),
			DB:        getInt("REDIS_DB", 0),
			KeyPrefix: getEnv("REDIS_KEY_PREFIX", "app:"),
		},
		S3: S3Config{
			Bucket:          getEnv("S3_BUCKET", ""),
			Region:          getEnv("S3_REGION", "ap-southeast-1"),
			Endpoint:        getEnv("S3_ENDPOINT", ""),
			PresignEndpoint: getEnv("S3_PRESIGN_ENDPOINT", ""),
			AccessKeyID:     getEnv("S3_ACCESS_KEY_ID", ""),
			SecretAccessKey: getEnv("S3_SECRET_ACCESS_KEY", ""),
			ForcePathStyle:  getBool("S3_FORCE_PATH_STYLE", false),
			PresignExpiry:   getDuration("S3_PRESIGN_EXPIRY", 15*time.Minute),
			CreateBucket:    getBool("S3_CREATE_BUCKET", false),
		},
	}
	return cfg, cfg.validate()
}

// validate required fields
func (c *Config) validate() error {
	if c.Database.URL == "" {
		return fmt.Errorf("config: DATABASE_URL is required (copy .env.example to .env)")
	}
	return nil
}

// read string env with fallback
func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// read int env with fallback
func getInt(key string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return fallback
}

// read bool env with fallback
func getBool(key string, fallback bool) bool {
	if v, err := strconv.ParseBool(os.Getenv(key)); err == nil {
		return v
	}
	return fallback
}

// read duration env (e.g. "5s") with fallback
func getDuration(key string, fallback time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(key)); err == nil {
		return v
	}
	return fallback
}

// read comma-separated env with fallback
func getList(key string, fallback []string) []string {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	parts := strings.Split(raw, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}
