package handler

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/willydocksha/go-clean-arch/internal/delivery/http/response"
	"github.com/willydocksha/go-clean-arch/internal/domain"
)

// http adapter for s3 files and spreadsheet parsing
type FileHandler struct {
	files         domain.FileUsecase
	maxUploadSize int64
}

// constructor for file handler
func NewFileHandler(files domain.FileUsecase, maxUploadSize int64) *FileHandler {
	return &FileHandler{files: files, maxUploadSize: maxUploadSize}
}

// register file routes; keys contain slashes so they travel as ?key=
func (h *FileHandler) Register(r chi.Router) {
	r.Route("/files", func(r chi.Router) {
		r.Post("/", h.Upload)
		r.Delete("/", h.Delete)
		r.Post("/presign-upload", h.PresignUpload)
		r.Get("/url", h.PresignDownload)
		r.Get("/download", h.Download)
		r.Post("/parse", h.Parse)
	})
}

// post /api/v1/files (multipart "file", optional "folder") -> key + presigned url
func (h *FileHandler) Upload(w http.ResponseWriter, r *http.Request) {
	file, header, err := formFile(w, r, h.maxUploadSize)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	defer file.Close()

	stored, err := h.files.Upload(r.Context(), r.FormValue("folder"), header.Filename, file, header.Size)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, stored, nil)
}

// post /api/v1/files/presign-upload {"folder","filename","content_type"} -> put url for the browser
func (h *FileHandler) PresignUpload(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Folder      string `json:"folder"`
		Filename    string `json:"filename"`
		ContentType string `json:"content_type"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		response.Error(w, r, err)
		return
	}
	if in.Filename == "" {
		response.Error(w, r, fmt.Errorf("%w: filename is required", domain.ErrInvalidInput))
		return
	}
	target, err := h.files.PresignUpload(r.Context(), in.Folder, in.Filename, in.ContentType)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, target, nil)
}

// get /api/v1/files/url?key=&filename= -> presigned download url
func (h *FileHandler) PresignDownload(w http.ResponseWriter, r *http.Request) {
	stored, err := h.files.PresignDownload(r.Context(), r.URL.Query().Get("key"), r.URL.Query().Get("filename"))
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, stored, nil)
}

// get /api/v1/files/download?key=&delivery=file|base64 -> proxied content
func (h *FileHandler) Download(w http.ResponseWriter, r *http.Request) {
	file, err := h.files.Download(r.Context(), r.URL.Query().Get("key"))
	if err != nil {
		response.Error(w, r, err)
		return
	}
	if r.URL.Query().Get("delivery") == DeliveryBase64 {
		response.FileBase64(w, file)
		return
	}
	response.File(w, file)
}

// delete /api/v1/files?key=
func (h *FileHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.files.Delete(r.Context(), r.URL.Query().Get("key")); err != nil {
		response.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// post /api/v1/files/parse?limit=100 (multipart "file": csv/xlsx) -> rows as json
func (h *FileHandler) Parse(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit", 100)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	file, header, err := formFile(w, r, h.maxUploadSize)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	defer file.Close()

	sheet, err := h.files.Parse(file, header.Filename, limit)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, sheet, nil)
}
