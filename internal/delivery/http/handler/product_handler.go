package handler

import (
	"net/http"
	"strings"

	"github.com/willydocksha/go-clean-arch/internal/delivery/http/response"
	"github.com/willydocksha/go-clean-arch/internal/domain"
)

// http adapter for products
type ProductHandler struct {
	products      domain.ProductUsecase
	files         domain.FileUsecase
	maxUploadSize int64
}

// constructor for product handler
func NewProductHandler(products domain.ProductUsecase, files domain.FileUsecase, maxUploadSize int64) *ProductHandler {
	return &ProductHandler{products: products, files: files, maxUploadSize: maxUploadSize}
}

// register product routes (literal paths win over {id})
func (h *ProductHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/products", h.List)
	mux.HandleFunc("POST /api/v1/products", h.Create)
	mux.HandleFunc("GET /api/v1/products/export", h.Export)
	mux.HandleFunc("GET /api/v1/products/import/template", h.ImportTemplate)
	mux.HandleFunc("POST /api/v1/products/import", h.Import)
	mux.HandleFunc("GET /api/v1/products/{id}", h.GetByID)
	mux.HandleFunc("PUT /api/v1/products/{id}", h.Replace)
	mux.HandleFunc("PATCH /api/v1/products/{id}/status", h.UpdateStatus)
	mux.HandleFunc("DELETE /api/v1/products/{id}", h.Delete)
}

// get /api/v1/products?q=&category=&status=&sort=price&order=desc&limit=&offset=
func (h *ProductHandler) List(w http.ResponseWriter, r *http.Request) {
	f, err := parseProductFilter(r)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	items, total, err := h.products.List(r.Context(), f)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, items, response.Pagination{Limit: f.Limit, Offset: f.Offset, Total: total})
}

// get /api/v1/products/{id}
func (h *ProductHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	p, err := h.products.GetByID(r.Context(), r.PathValue("id"))
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, p, nil)
}

// post /api/v1/products
func (h *ProductHandler) Create(w http.ResponseWriter, r *http.Request) {
	var in domain.ProductInput
	if err := decodeJSON(w, r, &in); err != nil {
		response.Error(w, r, err)
		return
	}
	p, err := h.products.Create(r.Context(), in)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, p, nil)
}

// put /api/v1/products/{id} (all fields required)
func (h *ProductHandler) Replace(w http.ResponseWriter, r *http.Request) {
	var in domain.ProductInput
	if err := decodeJSON(w, r, &in); err != nil {
		response.Error(w, r, err)
		return
	}
	p, err := h.products.Replace(r.Context(), r.PathValue("id"), in)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, p, nil)
}

// patch /api/v1/products/{id}/status {"status":"inactive"}
func (h *ProductHandler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status string `json:"status"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		response.Error(w, r, err)
		return
	}
	p, err := h.products.UpdateStatus(r.Context(), r.PathValue("id"), in.Status)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, p, nil)
}

// delete /api/v1/products/{id}
func (h *ProductHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.products.Delete(r.Context(), r.PathValue("id")); err != nil {
		response.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// get /api/v1/products/export?format=csv|xlsx&delivery=file|base64|url (+ list filters)
func (h *ProductHandler) Export(w http.ResponseWriter, r *http.Request) {
	f, err := parseProductFilter(r)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	file, err := h.products.Export(r.Context(), f, defaultString(r.URL.Query().Get("format"), "xlsx"))
	if err != nil {
		response.Error(w, r, err)
		return
	}
	deliverFile(w, r, file, h.files, "exports/products")
}

// get /api/v1/products/import/template?format=csv|xlsx&delivery=file|base64|url
func (h *ProductHandler) ImportTemplate(w http.ResponseWriter, r *http.Request) {
	file, err := h.products.ImportTemplate(defaultString(r.URL.Query().Get("format"), "xlsx"))
	if err != nil {
		response.Error(w, r, err)
		return
	}
	deliverFile(w, r, file, h.files, "templates")
}

// post /api/v1/products/import (multipart "file": .csv or .xlsx)
func (h *ProductHandler) Import(w http.ResponseWriter, r *http.Request) {
	file, header, err := formFile(w, r, h.maxUploadSize)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	defer file.Close()

	res, err := h.products.Import(r.Context(), file, header.Filename)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, res, nil)
}

// parse list filters from query string
func parseProductFilter(r *http.Request) (domain.ProductFilter, error) {
	q := r.URL.Query()
	limit, err := queryInt(r, "limit", 10)
	if err != nil {
		return domain.ProductFilter{}, err
	}
	offset, err := queryInt(r, "offset", 0)
	if err != nil {
		return domain.ProductFilter{}, err
	}
	return domain.ProductFilter{
		Search:   q.Get("q"),
		Category: q.Get("category"),
		Status:   q.Get("status"),
		SortBy:   q.Get("sort"),
		SortDesc: !strings.EqualFold(q.Get("order"), "asc"),
		Limit:    limit,
		Offset:   offset,
	}, nil
}

// fallback for empty strings
func defaultString(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
