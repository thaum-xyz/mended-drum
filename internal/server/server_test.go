package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thaum-xyz/mended-drum/internal/cocktaildb"
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
	return New(slog.New(slog.NewTextHandler(io.Discard, nil)), st, mealie.New("", ""), cocktaildb.New(), Config{})
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
		cocktaildb.New(), Config{APIKey: "secret", AllowOrigin: "https://drum.krupa.net.pl"})

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

func TestCORSEchoesOrigin(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := New(slog.New(slog.NewTextHandler(io.Discard, nil)), st, mealie.New("", ""), cocktaildb.New(), Config{AllowOrigin: "*"})

	req := httptest.NewRequest(http.MethodOptions, "/inventory", nil)
	req.Header.Set("Origin", "https://anything.example")
	req.Header.Set("Access-Control-Request-Headers", "authorization, x-session-id")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://anything.example" {
		t.Fatalf("allow-origin = %q, want echoed request origin", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "authorization, x-session-id" {
		t.Fatalf("allow-headers = %q, want reflected request headers", got)
	}
}

func TestGuestRoundTrip(t *testing.T) {
	srv := testServer(t)

	// Recording a preference auto-creates the guest.
	post := httptest.NewRequest(http.MethodPost, "/guests/preferences",
		strings.NewReader(`{"handle":"Anna","kind":"allergy","value":"nuts"}`))
	rp := httptest.NewRecorder()
	srv.ServeHTTP(rp, post)
	if rp.Code != http.StatusOK {
		t.Fatalf("add pref %d: %s", rp.Code, rp.Body.String())
	}

	// Lookup is case-insensitive on the handle.
	get := httptest.NewRequest(http.MethodGet, "/guests/get?handle=anna", nil)
	rg := httptest.NewRecorder()
	srv.ServeHTTP(rg, get)
	if rg.Code != http.StatusOK {
		t.Fatalf("get guest %d", rg.Code)
	}
	if !strings.Contains(rg.Body.String(), "nuts") {
		t.Fatalf("expected allergy 'nuts' in profile, got %s", rg.Body.String())
	}

	// Search results must carry preferences too (not null).
	srch := httptest.NewRequest(http.MethodGet, "/guests?q=anna", nil)
	rs := httptest.NewRecorder()
	srv.ServeHTTP(rs, srch)
	if rs.Code != http.StatusOK || !strings.Contains(rs.Body.String(), "nuts") {
		t.Fatalf("search should include prefs, got %d: %s", rs.Code, rs.Body.String())
	}
}

func TestExternalLookup(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"drinks":[{"strDrink":"Negroni","strIngredient1":"Gin","strMeasure1":"1 oz","strIngredient2":"Campari","strMeasure2":"1 oz"}]}`))
	}))
	defer ts.Close()

	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Set(context.Background(), "Gin", "in_stock"); err != nil {
		t.Fatalf("seed inventory: %v", err)
	}

	srv := New(slog.New(slog.NewTextHandler(io.Discard, nil)), st, mealie.New("", ""),
		cocktaildb.NewWithBaseURL(ts.URL), Config{})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/recipes/external?name=negroni", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	// Gin in stock, Campari untracked -> not makeable, Campari listed missing.
	if body := rec.Body.String(); !strings.Contains(body, "Negroni") ||
		!strings.Contains(body, "Campari") || !strings.Contains(body, "untracked") {
		t.Fatalf("unexpected external body: %s", body)
	}
}

func TestCreateRecipe(t *testing.T) {
	var patched string
	mealieStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/recipes":
			_, _ = w.Write([]byte(`"margarita"`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/organizers/tags":
			_, _ = w.Write([]byte(`{"id":"t1","name":"mended-drum","slug":"mended-drum"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/recipes/margarita":
			_, _ = w.Write([]byte(`{"id":"r1","slug":"margarita","name":"Margarita","recipeIngredient":[],"recipeInstructions":[],"tags":[]}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/recipes/margarita":
			b, _ := io.ReadAll(r.Body)
			patched = string(b)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mealieStub.Close()

	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	srv := New(slog.New(slog.NewTextHandler(io.Discard, nil)), st,
		mealie.New(mealieStub.URL, "tok"), cocktaildb.New(), Config{})

	// Without confirm -> refused (the promocja gate).
	rn := httptest.NewRecorder()
	srv.ServeHTTP(rn, httptest.NewRequest(http.MethodPost, "/recipes",
		strings.NewReader(`{"name":"Margarita","ingredients":[{"name":"Tequila"}]}`)))
	if rn.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without confirm, got %d", rn.Code)
	}

	// With confirm -> created and tagged.
	ro := httptest.NewRecorder()
	srv.ServeHTTP(ro, httptest.NewRequest(http.MethodPost, "/recipes", strings.NewReader(
		`{"confirm":true,"name":"Margarita","source":"TheCocktailDB","ingredients":[{"name":"Tequila","measure":"50 ml"},{"name":"Lime juice","measure":"25 ml"}],"instructions":"Shake."}`)))
	if ro.Code != http.StatusOK {
		t.Fatalf("create got %d: %s", ro.Code, ro.Body.String())
	}
	if !strings.Contains(ro.Body.String(), "margarita") {
		t.Fatalf("expected slug in response: %s", ro.Body.String())
	}
	if !strings.Contains(patched, "mended-drum") || !strings.Contains(patched, "Tequila") {
		t.Fatalf("patched payload missing tag/ingredient: %s", patched)
	}
}
