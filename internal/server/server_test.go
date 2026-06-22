package server

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/thaum-xyz/mended-drum/internal/cocktaildb"
	"github.com/thaum-xyz/mended-drum/internal/mealie"
	"github.com/thaum-xyz/mended-drum/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run server tests against Postgres")
	}
	st, err := store.Open(dsn) // also migrates the schema
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if raw, err := sql.Open("pgx", dsn); err == nil {
		_, _ = raw.ExecContext(context.Background(), `TRUNCATE ingredient_stock, guest, guest_pref`)
		_ = raw.Close()
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func newLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func testServer(t *testing.T) http.Handler {
	return New(newLogger(), newTestStore(t), mealie.New("", ""), cocktaildb.New(), Config{})
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
	srv := New(newLogger(), newTestStore(t), mealie.New("", ""), cocktaildb.New(),
		Config{APIKey: "secret", AllowOrigin: "https://drum.krupa.net.pl"})

	// CORS preflight: no auth, 204, origin echoed, request headers reflected.
	ro := httptest.NewRecorder()
	opt := httptest.NewRequest(http.MethodOptions, "/inventory", nil)
	opt.Header.Set("Access-Control-Request-Headers", "authorization, x-session-id")
	srv.ServeHTTP(ro, opt)
	if ro.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS got %d, want 204", ro.Code)
	}
	if ro.Header().Get("Access-Control-Allow-Origin") != "https://drum.krupa.net.pl" {
		t.Fatalf("missing CORS allow-origin")
	}
	if ro.Header().Get("Access-Control-Allow-Headers") != "authorization, x-session-id" {
		t.Fatalf("allow-headers = %q, want reflected", ro.Header().Get("Access-Control-Allow-Headers"))
	}

	// Protected endpoint without key -> 401.
	rec1 := httptest.NewRecorder()
	srv.ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/inventory", nil))
	if rec1.Code != http.StatusUnauthorized {
		t.Fatalf("no key got %d, want 401", rec1.Code)
	}

	// Health stays open.
	rech := httptest.NewRecorder()
	srv.ServeHTTP(rech, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rech.Code != http.StatusOK {
		t.Fatalf("healthz got %d, want 200", rech.Code)
	}

	// Unknown probes (e.g. Open WebUI's /api/config) get 404, not a hostile 401.
	runk := httptest.NewRecorder()
	srv.ServeHTTP(runk, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if runk.Code != http.StatusNotFound {
		t.Fatalf("unknown path got %d, want 404", runk.Code)
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
	srv := New(newLogger(), newTestStore(t), mealie.New("", ""), cocktaildb.New(), Config{AllowOrigin: "*"})
	req := httptest.NewRequest(http.MethodOptions, "/inventory", nil)
	req.Header.Set("Origin", "https://anything.example")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://anything.example" {
		t.Fatalf("allow-origin = %q, want echoed request origin", got)
	}
}

func TestGuestRoundTrip(t *testing.T) {
	srv := testServer(t)

	post := httptest.NewRequest(http.MethodPost, "/guests/preferences",
		strings.NewReader(`{"handle":"Anna","kind":"allergy","value":"nuts"}`))
	rp := httptest.NewRecorder()
	srv.ServeHTTP(rp, post)
	if rp.Code != http.StatusOK {
		t.Fatalf("add pref %d: %s", rp.Code, rp.Body.String())
	}

	rg := httptest.NewRecorder()
	srv.ServeHTTP(rg, httptest.NewRequest(http.MethodGet, "/guests/get?handle=anna", nil))
	if rg.Code != http.StatusOK || !strings.Contains(rg.Body.String(), "nuts") {
		t.Fatalf("get guest %d: %s", rg.Code, rg.Body.String())
	}

	rs := httptest.NewRecorder()
	srv.ServeHTTP(rs, httptest.NewRequest(http.MethodGet, "/guests?q=anna", nil))
	if rs.Code != http.StatusOK || !strings.Contains(rs.Body.String(), "nuts") {
		t.Fatalf("search should include prefs, got %d: %s", rs.Code, rs.Body.String())
	}
}

func TestExternalLookup(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"drinks":[{"strDrink":"Negroni","strIngredient1":"Gin","strMeasure1":"1 oz","strIngredient2":"Campari","strMeasure2":"1 oz"}]}`))
	}))
	defer ts.Close()

	st := newTestStore(t)
	if _, err := st.Set(context.Background(), "Gin", "in_stock"); err != nil {
		t.Fatalf("seed inventory: %v", err)
	}
	srv := New(newLogger(), st, mealie.New("", ""), cocktaildb.NewWithBaseURL(ts.URL), Config{})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/recipes/external?name=negroni", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
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

	srv := New(newLogger(), newTestStore(t), mealie.New(mealieStub.URL, "tok"), cocktaildb.New(), Config{})

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
		`{"confirm":true,"name":"Margarita","source":"TheCocktailDB","ingredients":[{"name":"Tequila","measure":"50 ml"}],"instructions":"Shake."}`)))
	if ro.Code != http.StatusOK {
		t.Fatalf("create got %d: %s", ro.Code, ro.Body.String())
	}
	if !strings.Contains(patched, "mended-drum") || !strings.Contains(patched, "Tequila") {
		t.Fatalf("patched payload missing tag/ingredient: %s", patched)
	}
}
