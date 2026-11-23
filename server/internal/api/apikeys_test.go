package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSessionRoutesRefuseAPIKeys(t *testing.T) {
	a := &API{}
	called := false
	h := a.session("viewer", func(w http.ResponseWriter, r *http.Request) { called = true })
	r := httptest.NewRequest(http.MethodGet, "/api/admin/api-keys", nil)
	r.Header.Set("Authorization", "Bearer drix_abc")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || called {
		t.Fatalf("got %d", w.Code)
	}
	// Not a Drix key at all.
	h = a.role("viewer", func(w http.ResponseWriter, r *http.Request) { called = true })
	r.Header.Set("Authorization", "Basic Zm9vOmJhcg==")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized || called {
		t.Fatalf("got %d", w.Code)
	}
}
