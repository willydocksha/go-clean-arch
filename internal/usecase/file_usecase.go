package usecase

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/willydocksha/go-clean-arch/internal/domain"
	"github.com/willydocksha/go-clean-arch/pkg/spreadsheet"
)

// allowed characters for folder names
var folderPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9/_-]{0,99}$`)

// returned when s3 is not configured
var errStorageDisabled = fmt.Errorf("%w: object storage (s3) is not configured", domain.ErrUnavailable)

// file usecase: s3 operations plus spreadsheet parsing
type fileUsecase struct {
	storage       domain.FileStorage // nil when s3 is disabled
	presignExpiry time.Duration
	timeout       time.Duration
	now           func() time.Time
}

// constructor; storage may be nil
func NewFileUsecase(storage domain.FileStorage, presignExpiry, timeout time.Duration) domain.FileUsecase {
	return &fileUsecase{storage: storage, presignExpiry: presignExpiry, timeout: timeout, now: time.Now}
}

// upload a user file under folder/yyyy/mm/dd/<uuid>.<ext>
func (u *fileUsecase) Upload(ctx context.Context, folder, filename string, body io.ReadSeeker, size int64) (*domain.StoredFile, error) {
	if u.storage == nil {
		return nil, errStorageDisabled
	}
	ctx, cancel := context.WithTimeout(ctx, 6*u.timeout)
	defer cancel()

	// detect content type from extension, then from bytes
	contentType, err := detectContentType(filename, body)
	if err != nil {
		return nil, err
	}

	// build key and upload
	key, err := u.newKey(folder, filename)
	if err != nil {
		return nil, err
	}
	if err := u.storage.Upload(ctx, key, body, size, contentType); err != nil {
		return nil, err
	}
	return u.presign(ctx, key, filepath.Base(filename), contentType, size)
}

// upload an app-generated file (e.g. export) and return a download url
func (u *fileUsecase) UploadGenerated(ctx context.Context, folder string, f *domain.File) (*domain.StoredFile, error) {
	if u.storage == nil {
		return nil, errStorageDisabled
	}
	ctx, cancel := context.WithTimeout(ctx, 6*u.timeout)
	defer cancel()

	key, err := u.newKey(folder, f.Name)
	if err != nil {
		return nil, err
	}
	size := int64(len(f.Data))
	if err := u.storage.Upload(ctx, key, bytes.NewReader(f.Data), size, f.ContentType); err != nil {
		return nil, err
	}
	return u.presign(ctx, key, f.Name, f.ContentType, size)
}

// presigned download url for an existing key
func (u *fileUsecase) PresignDownload(ctx context.Context, key, filename string) (*domain.StoredFile, error) {
	if u.storage == nil {
		return nil, errStorageDisabled
	}
	if err := validateKey(key); err != nil {
		return nil, err
	}
	return u.presign(ctx, key, filename, "", 0)
}

// presigned put url so the frontend uploads straight to s3
func (u *fileUsecase) PresignUpload(ctx context.Context, folder, filename, contentType string) (*domain.UploadTarget, error) {
	if u.storage == nil {
		return nil, errStorageDisabled
	}
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(filename))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	key, err := u.newKey(folder, filename)
	if err != nil {
		return nil, err
	}
	url, err := u.storage.PresignPut(ctx, key, contentType, u.presignExpiry)
	if err != nil {
		return nil, err
	}
	return &domain.UploadTarget{
		Key:       key,
		Method:    http.MethodPut,
		URL:       url,
		Headers:   map[string]string{"Content-Type": contentType},
		ExpiresAt: u.now().Add(u.presignExpiry).UTC(),
	}, nil
}

// download an object into memory (proxy through the api)
func (u *fileUsecase) Download(ctx context.Context, key string) (*domain.File, error) {
	if u.storage == nil {
		return nil, errStorageDisabled
	}
	if err := validateKey(key); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 6*u.timeout)
	defer cancel()

	data, contentType, err := u.storage.Get(ctx, key)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: file %q", domain.ErrNotFound, key)
	}
	if err != nil {
		return nil, err
	}
	return &domain.File{Name: path.Base(key), ContentType: contentType, Data: data}, nil
}

// delete an object
func (u *fileUsecase) Delete(ctx context.Context, key string) error {
	if u.storage == nil {
		return errStorageDisabled
	}
	if err := validateKey(key); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()
	return u.storage.Delete(ctx, key)
}

// parse a csv/xlsx upload into json rows (no s3 needed)
func (u *fileUsecase) Parse(r io.Reader, filename string, limit int) (*domain.ParsedSheet, error) {
	f, err := spreadsheet.FormatFromFilename(filename)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err)
	}
	table, err := spreadsheet.Read(r, f)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err)
	}

	// keep response small by returning only the first rows
	total := len(table.Rows)
	if limit > 0 && total > limit {
		table.Rows = table.Rows[:limit]
	}
	return &domain.ParsedSheet{Headers: table.Headers, Rows: table.Records(), Total: total}, nil
}

// ---------- helpers ----------

// presign and build the response
func (u *fileUsecase) presign(ctx context.Context, key, name, contentType string, size int64) (*domain.StoredFile, error) {
	url, err := u.storage.PresignGet(ctx, key, u.presignExpiry, name)
	if err != nil {
		return nil, err
	}
	return &domain.StoredFile{
		Key:         key,
		Name:        name,
		ContentType: contentType,
		Size:        size,
		URL:         url,
		ExpiresAt:   u.now().Add(u.presignExpiry).UTC(),
	}, nil
}

// generate a unique, date-partitioned key
func (u *fileUsecase) newKey(folder, filename string) (string, error) {
	folder = strings.Trim(strings.ToLower(strings.TrimSpace(folder)), "/")
	if folder == "" {
		folder = "uploads"
	}
	if !folderPattern.MatchString(folder) || strings.Contains(folder, "..") {
		return "", fmt.Errorf("%w: folder may only contain a-z, 0-9, '/', '_' and '-'", domain.ErrInvalidInput)
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if len(ext) > 10 {
		ext = ""
	}
	return path.Join(folder, u.now().UTC().Format("2006/01/02"), uuid.NewString()+ext), nil
}

// reject empty or traversal-looking keys
func validateKey(key string) error {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "..") || len(key) > 1024 {
		return fmt.Errorf("%w: invalid file key", domain.ErrInvalidInput)
	}
	return nil
}

// guess content type from extension, falling back to sniffing the first 512 bytes
func detectContentType(filename string, body io.ReadSeeker) (string, error) {
	if ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename))); ct != "" {
		return ct, nil
	}
	buf := make([]byte, 512)
	n, _ := io.ReadFull(body, buf)
	if _, err := body.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return http.DetectContentType(buf[:n]), nil
}
