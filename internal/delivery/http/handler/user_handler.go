package handler

import (
	"net/http"

	"github.com/willydocksha/go-clean-arch/internal/delivery/http/response"
	"github.com/willydocksha/go-clean-arch/internal/domain"
)

// http adapter for user usecase
type UserHandler struct {
	usecase domain.UserUsecase
}

// constructor for user handler
func NewUserHandler(uc domain.UserUsecase) *UserHandler {
	return &UserHandler{usecase: uc}
}

// register user routes on the mux
func (h *UserHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/users", h.Create)
	mux.HandleFunc("GET /api/v1/users", h.List)
	mux.HandleFunc("GET /api/v1/users/{id}", h.GetByID)
	mux.HandleFunc("PATCH /api/v1/users/{id}", h.Update)
	mux.HandleFunc("DELETE /api/v1/users/{id}", h.Delete)
}

// post /api/v1/users
func (h *UserHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input domain.CreateUserInput
	if err := decodeJSON(w, r, &input); err != nil {
		response.Error(w, r, err)
		return
	}
	user, err := h.usecase.Create(r.Context(), input)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, user, nil)
}

// get /api/v1/users?limit=&offset=
func (h *UserHandler) List(w http.ResponseWriter, r *http.Request) {
	// parse pagination query params
	limit, err := queryInt(r, "limit", 10)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	offset, err := queryInt(r, "offset", 0)
	if err != nil {
		response.Error(w, r, err)
		return
	}

	// fetch page
	params := domain.ListParams{Limit: limit, Offset: offset}
	users, total, err := h.usecase.List(r.Context(), params)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, users, response.Pagination{Limit: limit, Offset: offset, Total: total})
}

// get /api/v1/users/{id}
func (h *UserHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	user, err := h.usecase.GetByID(r.Context(), r.PathValue("id"))
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, user, nil)
}

// patch /api/v1/users/{id}
func (h *UserHandler) Update(w http.ResponseWriter, r *http.Request) {
	var input domain.UpdateUserInput
	if err := decodeJSON(w, r, &input); err != nil {
		response.Error(w, r, err)
		return
	}
	user, err := h.usecase.Update(r.Context(), r.PathValue("id"), input)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, user, nil)
}

// delete /api/v1/users/{id}
func (h *UserHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.usecase.Delete(r.Context(), r.PathValue("id")); err != nil {
		response.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
