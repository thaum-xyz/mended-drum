package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thaum-xyz/mended-drum/internal/mealie"
	"github.com/thaum-xyz/mended-drum/internal/store"
)

func testServer(t *testing.T) http.Handler {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(slog.New(slog.NewTextHandler(io.Discard, nil)), st, mealie.New("", ""))
}

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	testServer(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestInventoryRoundTrip(t *testing.T) {
	srv := testServer(t)

	put := httptest.NewRequest(http.MethodPut, "/inventory", strings.NewReader(`{"name":"Gin","status":"in_stock"}`))
	recPut := httptest.NewRecorder()
	srv.ServeHTTP(recPut, put)
	if recPut.Code != http.StatusOK {
		t.Fatalf("put status %d: %s", recPut.Code, recPut.Body.String())
	}

	recGet := httptest.NewRecorder()
	srv.ServeHTTP(recGet, httptest.NewRequest(http.MethodGet, "/inventory", nil))
	if recGet.Code != http.StatusOK {
		t.Fatalf("get status %d", recGet.Code)
	}
	if !strings.Contains(recGet.Body.String(), "Gin") {
		t.Fatalf("expected Gin in inventory, got %s", recGet.Body.String())
	}
}

func TestSetInventoryValidation(t *testing.T) {
	srv := testServer(t)
	bad := httptest.NewRequest(http.MethodPut, "/inventory", strings.NewReader(`{"name":"gin","status":"plenty"}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, bad)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid status, got %d", rec.Code)
	}
}
