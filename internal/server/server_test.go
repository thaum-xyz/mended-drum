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
	return New(slog.New(slog.NewTextHandler(io.Discard, nil)), st, mealie.New("", ""), Config{})
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

func TestAuthAndCORS(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := New(slog.New(slog.NewTextHandler(io.Discard, nil)), st, mealie.New("", ""),
		Config{APIKey: "secret", AllowOrigin: "https://drum.krupa.net.pl"})

	// CORS preflight: no auth, 204, origin echoed.
	ro := httptest.NewRecorder()
	srv.ServeHTTP(ro, httptest.NewRequest(http.MethodOptions, "/inventory", nil))
	if ro.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS got %d, want 204", ro.Code)
	}
	if ro.Header().Get("Access-Control-Allow-Origin") != "https://drum.krupa.net.pl" {
		t.Fatalf("missing CORS allow-origin")
	}

	// Protected endpoint without key -> 401.
	rec1 := httptest.NewRecorder()
	srv.ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/inventory", nil))
	if rec1.Code != http.StatusUnauthorized {
		t.Fatalf("no key got %d, want 401", rec1.Code)
	}

	// Health endpoint stays open.
	rech := httptest.NewRecorder()
	srv.ServeHTTP(rech, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rech.Code != http.StatusOK {
		t.Fatalf("healthz got %d, want 200", rech.Code)
	}

	// Correct key -> 200.
	r2 := httptest.NewRequest(http.MethodGet, "/inventory", nil)
	r2.Header.Set("Authorization", "Bearer secret")
	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, r2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("with key got %d, want 200", rec2.Code)
	}
}
