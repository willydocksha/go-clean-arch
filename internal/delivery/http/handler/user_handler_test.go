package handler_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	deliveryhttp "github.com/willydocksha/go-clean-arch/internal/delivery/http"
	"github.com/willydocksha/go-clean-arch/internal/delivery/http/handler"
	"github.com/willydocksha/go-clean-arch/internal/repository/memory"
	"github.com/willydocksha/go-clean-arch/internal/usecase"
)

// spin up a test server with in-memory wiring
func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	uc := usecase.NewUserUsecase(memory.NewUserRepository(), time.Second)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := deliveryhttp.NewRouter(
		deliveryhttp.RouterConfig{Logger: log, AllowedOrigins: []string{"*"}},
		handler.NewHealthHandler(nil),
		handler.NewUserHandler(uc),
	)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv
}

// send a request and return status and decoded body
func do(t *testing.T, method, url, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

// full crud flow through the http layer
func TestUserCRUD(t *testing.T) {
	srv := newServer(t)
	base := srv.URL + "/api/v1/users"

	// create
	code, body := do(t, http.MethodPost, base, `{"name":"Willy","email":"willy@example.com"}`)
	if code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d %v", code, body)
	}
	id := body["data"].(map[string]any)["id"].(string)

	// duplicate -> 409
	if code, _ := do(t, http.MethodPost, base, `{"name":"X","email":"willy@example.com"}`); code != http.StatusConflict {
		t.Fatalf("duplicate: want 409, got %d", code)
	}

	// malformed json -> 400
	if code, _ := do(t, http.MethodPost, base, `{bad`); code != http.StatusBadRequest {
		t.Fatalf("bad json: want 400, got %d", code)
	}

	// get
	if code, _ := do(t, http.MethodGet, base+"/"+id, ""); code != http.StatusOK {
		t.Fatalf("get: want 200, got %d", code)
	}

	// list
	code, body = do(t, http.MethodGet, base+"?limit=5", "")
	if code != http.StatusOK || body["meta"].(map[string]any)["total"].(float64) != 1 {
		t.Fatalf("list: unexpected %d %v", code, body)
	}

	// patch
	code, body = do(t, http.MethodPatch, base+"/"+id, `{"name":"Docksha"}`)
	if code != http.StatusOK || body["data"].(map[string]any)["name"] != "Docksha" {
		t.Fatalf("patch: unexpected %d %v", code, body)
	}

	// delete then 404
	if code, _ := do(t, http.MethodDelete, base+"/"+id, ""); code != http.StatusNoContent {
		t.Fatalf("delete: want 204, got %d", code)
	}
	if code, _ := do(t, http.MethodGet, base+"/"+id, ""); code != http.StatusNotFound {
		t.Fatalf("get after delete: want 404, got %d", code)
	}
}

// liveness endpoint responds ok
func TestHealthz(t *testing.T) {
	srv := newServer(t)
	if code, _ := do(t, http.MethodGet, srv.URL+"/healthz", ""); code != http.StatusOK {
		t.Fatalf("healthz: want 200, got %d", code)
	}
}
