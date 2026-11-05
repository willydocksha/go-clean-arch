package domain

import (
	"context"
	"io"
	"time"
)

// an in-memory file produced by the app (exports, templates)
type File struct {
	Name        string
	ContentType string
	Data        []byte
}

// a file stored in object storage, returned to clients
type StoredFile struct {
	Key         string    `json:"key"`
	Name        string    `json:"name,omitempty"`
	ContentType string    `json:"content_type,omitempty"`
	Size        int64     `json:"size"`
	URL         string    `json:"url"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// presigned upload target for direct browser uploads
type UploadTarget struct {
	Key       string            `json:"key"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expires_at"`
}

// parsed spreadsheet preview
type ParsedSheet struct {
	Headers []string            `json:"headers"`
	Rows    []map[string]string `json:"rows"`
	Total   int                 `json:"total"`
}

// object storage port (implemented by pkg/storage)
// not-found errors must wrap fs.ErrNotExist
type FileStorage interface {
	Upload(ctx context.Context, key string, body io.ReadSeeker, size int64, contentType string) error
	Get(ctx context.Context, key string) (data []byte, contentType string, err error)
	Delete(ctx context.Context, key string) error
	PresignGet(ctx context.Context, key string, expiry time.Duration, filename string) (string, error)
	PresignPut(ctx context.Context, key, contentType string, expiry time.Duration) (string, error)
}

// cache port (implemented by pkg/cache redis and noop)
type Cache interface {
	Get(ctx context.Context, key string, dst any) error
	Set(ctx context.Context, key string, value any, ttl time.Duration) error
	Delete(ctx context.Context, keys ...string) error
}

// file usecase port
type FileUsecase interface {
	Upload(ctx context.Context, folder, filename string, body io.ReadSeeker, size int64) (*StoredFile, error)
	UploadGenerated(ctx context.Context, folder string, f *File) (*StoredFile, error)
	PresignDownload(ctx context.Context, key, filename string) (*StoredFile, error)
	PresignUpload(ctx context.Context, folder, filename, contentType string) (*UploadTarget, error)
	Download(ctx context.Context, key string) (*File, error)
	Delete(ctx context.Context, key string) error
	Parse(r io.Reader, filename string, limit int) (*ParsedSheet, error)
}
