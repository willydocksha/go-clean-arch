package response

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"strconv"

	"github.com/willydocksha/go-clean-arch/internal/domain"
)

// standard json envelope
type Body struct {
	Success bool   `json:"success"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
	Meta    any    `json:"meta,omitempty"`
}

// pagination metadata
type Pagination struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
	Total  int `json:"total"`
}

// base64 file payload that the frontend can decode into a blob
type Base64File struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int    `json:"size"`
	Base64      string `json:"base64"`
}

// write a successful json response
func JSON(w http.ResponseWriter, status int, data any, meta any) {
	write(w, status, Body{Success: true, Data: data, Meta: meta})
}

// stream raw bytes as a downloadable attachment
func File(w http.ResponseWriter, f *domain.File) {
	w.Header().Set("Content-Type", f.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(f.Data)))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": f.Name}))
	// let browsers read the filename header on cross-origin requests
	w.Header().Set("Access-Control-Expose-Headers", "Content-Disposition")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(f.Data)
}

// send a file as base64 inside the json envelope
func FileBase64(w http.ResponseWriter, f *domain.File) {
	JSON(w, http.StatusOK, Base64File{
		Filename:    f.Name,
		ContentType: f.ContentType,
		Size:        len(f.Data),
		Base64:      base64.StdEncoding.EncodeToString(f.Data),
	}, nil)
}

// write an error response, mapping domain errors to status codes
func Error(w http.ResponseWriter, r *http.Request, err error) {
	status, msg := mapError(err)

	// log unexpected errors with request context
	if status >= http.StatusInternalServerError && status != http.StatusServiceUnavailable {
		slog.ErrorContext(r.Context(), "request failed", "error", err, "path", r.URL.Path)
	}
	write(w, status, Body{Success: false, Error: msg})
}

// map domain errors to http status and safe message
func mapError(err error) (int, string) {
	var maxBytes *http.MaxBytesError
	switch {
	case errors.As(err, &maxBytes):
		return http.StatusRequestEntityTooLarge, "request body too large"
	case errors.Is(err, domain.ErrInvalidInput):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound, err.Error()
	case errors.Is(err, domain.ErrConflict):
		return http.StatusConflict, err.Error()
	case errors.Is(err, domain.ErrUnavailable):
		return http.StatusServiceUnavailable, err.Error()
	default:
		// hide internal details from clients
		return http.StatusInternalServerError, domain.ErrInternal.Error()
	}
}

// encode body as json
func write(w http.ResponseWriter, status int, body Body) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
