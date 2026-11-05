package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/willydocksha/go-clean-arch/internal/delivery/http/response"
	"github.com/willydocksha/go-clean-arch/internal/domain"
)

// max json request body size (1mb)
const maxBodyBytes = 1 << 20

// how a generated file is returned to the client (?delivery=)
const (
	DeliveryFile   = "file"   // raw bytes with content-disposition (default)
	DeliveryBase64 = "base64" // json with base64 content
	DeliveryURL    = "url"    // uploaded to s3, json with presigned url
)

// decode a limited, strict json body
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return err
		}
		return fmt.Errorf("%w: malformed json body: %v", domain.ErrInvalidInput, err)
	}
	return nil
}

// read an int query param with fallback
func queryInt(r *http.Request, key string, fallback int) (int, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: %s must be an integer", domain.ErrInvalidInput, key)
	}
	return v, nil
}

// read the "file" field from a multipart form; caller must close the file
func formFile(w http.ResponseWriter, r *http.Request, maxSize int64) (multipart.File, *multipart.FileHeader, error) {
	// cap the whole body (file + small overhead for other fields)
	r.Body = http.MaxBytesReader(w, r.Body, maxSize+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("%w: expected multipart/form-data with a \"file\" field", domain.ErrInvalidInput)
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		return nil, nil, fmt.Errorf("%w: missing \"file\" field", domain.ErrInvalidInput)
	}
	if header.Size > maxSize {
		file.Close()
		return nil, nil, fmt.Errorf("%w: file exceeds %d bytes", domain.ErrInvalidInput, maxSize)
	}
	return file, header, nil
}

// send a generated file using the delivery mode requested by the client
func deliverFile(w http.ResponseWriter, r *http.Request, f *domain.File, files domain.FileUsecase, folder string) {
	switch r.URL.Query().Get("delivery") {
	case "", DeliveryFile:
		response.File(w, f)
	case DeliveryBase64:
		response.FileBase64(w, f)
	case DeliveryURL:
		stored, err := files.UploadGenerated(r.Context(), folder, f)
		if err != nil {
			response.Error(w, r, err)
			return
		}
		response.JSON(w, http.StatusOK, stored, nil)
	default:
		response.Error(w, r, fmt.Errorf("%w: delivery must be file, base64 or url", domain.ErrInvalidInput))
	}
}

// read a path parameter such as {id}
func urlParam(r *http.Request, key string) string {
	return chi.URLParam(r, key)
}
