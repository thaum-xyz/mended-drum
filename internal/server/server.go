// Package server wires the HTTP routes for the mended-drum tool server and
// exposes them as an OpenAPI tool server for Open WebUI.
package server

import (
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/thaum-xyz/mended-drum/internal/mealie"
	"github.com/thaum-xyz/mended-drum/internal/store"
)

const maxRecipes = 25

// Config holds runtime configuration for the HTTP layer.
type Config struct {
	APIKey      string // if set, required as "Authorization: Bearer <key>" on data endpoints
	AllowOrigin string // CORS Access-Control-Allow-Origin (defaults to "*")
}

type Server struct {
	log    *slog.Logger
	store  *store.Store
	mealie *mealie.Client
}

// New returns the HTTP handler for the tool server.
func New(log *slog.Logger, st *store.Store, mc *mealie.Client, cfg Config) http.Handler {
	s := &Server{log: log, store: st, mealie: mc}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.health)
	mux.HandleFunc("GET /openapi.json", s.openapi)
	mux.HandleFunc("GET /inventory", s.listInventory)
	mux.HandleFunc("PUT /inventory", s.setInventory)
	mux.HandleFunc("GET /recipes/search", s.searchRecipes)
	mux.HandleFunc("GET /recipes/{slug}", s.getRecipe)
	return middleware(log, cfg, mux)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) openapi(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(openAPISpec))
}

func (s *Server) listInventory(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.List(r.Context())
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "list inventory", err)
		return
	}
	if items == nil {
		items = []store.Stock{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"inventory": items})
}

type setInventoryReq struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

func (s *Server) setInventory(w http.ResponseWriter, r *http.Request) {
	var req setInventoryReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.fail(w, http.StatusBadRequest, "decode body", err)
		return
	}
	if strings.TrimSpace(req.Name) == "" || !store.ValidStatus(req.Status) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "name is required and status must be one of: in_stock, low, out",
		})
		return
	}
	st, err := s.store.Set(r.Context(), req.Name, req.Status)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "set inventory", err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

type recipeResult struct {
	Slug        string        `json:"slug"`
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Tags        []string      `json:"tags,omitempty"`
	Allergens   []string      `json:"allergens"`
	Makeable    bool          `json:"makeable"`
	Missing     []missingItem `json:"missing"`
	Ingredients []ingredient  `json:"ingredients,omitempty"`
	Steps       []string      `json:"steps,omitempty"`
}

type missingItem struct {
	Name   string `json:"name"`
	Reason string `json:"reason"` // "out" or "untracked"
}

type ingredient struct {
	Name      string `json:"name"`
	Text      string `json:"text,omitempty"`
	Available bool   `json:"available"`
}

func (s *Server) searchRecipes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	onlyMakeable := r.URL.Query().Get("only_makeable") == "true"
	max := 10
	if v := r.URL.Query().Get("max"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			max = n
		}
	}
	if max > maxRecipes {
		max = maxRecipes
	}

	summaries, err := s.mealie.SearchRecipes(r.Context(), q, max)
	if err != nil {
		s.fail(w, http.StatusBadGateway, "mealie search", err)
		return
	}
	statuses, err := s.store.Statuses(r.Context())
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "inventory statuses", err)
		return
	}

	results := []recipeResult{}
	for _, sum := range summaries {
		rec, err := s.mealie.GetRecipe(r.Context(), sum.Slug)
		if err != nil {
			s.log.Warn("skip recipe", "slug", sum.Slug, "err", err)
			continue
		}
		res := evaluate(rec, statuses, false)
		if onlyMakeable && !res.Makeable {
			continue
		}
		results = append(results, res)
	}
	writeJSON(w, http.StatusOK, map[string]any{"recipes": results})
}

func (s *Server) getRecipe(w http.ResponseWriter, r *http.Request) {
	rec, err := s.mealie.GetRecipe(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.fail(w, http.StatusBadGateway, "mealie get recipe", err)
		return
	}
	statuses, err := s.store.Statuses(r.Context())
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "inventory statuses", err)
		return
	}
	writeJSON(w, http.StatusOK, evaluate(rec, statuses, true))
}

// evaluate joins a recipe against current stock, computing makeability, missing
// ingredients and allergens. detail adds the full ingredient/step lists.
func evaluate(rec *mealie.Recipe, statuses map[string]store.Status, detail bool) recipeResult {
	res := recipeResult{
		Slug:        rec.Slug,
		Name:        rec.Name,
		Description: rec.Description,
		Allergens:   []string{},
		Missing:     []missingItem{},
		Makeable:    true,
	}
	for _, t := range rec.Tags {
		if a, ok := allergenOf(t); ok {
			res.Allergens = append(res.Allergens, a)
			continue
		}
		res.Tags = append(res.Tags, t.Name)
	}
	for _, ing := range rec.RecipeIngredient {
		name := ingredientName(ing)
		if name == "" {
			continue
		}
		st, tracked := statuses[store.Key(name)]
		available := tracked && (st == store.InStock || st == store.Low)
		if !available {
			res.Makeable = false
			reason := "untracked"
			if tracked && st == store.Out {
				reason = "out"
			}
			res.Missing = append(res.Missing, missingItem{Name: name, Reason: reason})
		}
		if detail {
			res.Ingredients = append(res.Ingredients, ingredient{
				Name:      name,
				Text:      ing.Display,
				Available: available,
			})
		}
	}
	if detail {
		for _, step := range rec.RecipeInstructions {
			if strings.TrimSpace(step.Text) != "" {
				res.Steps = append(res.Steps, step.Text)
			}
		}
	}
	return res
}

func ingredientName(ing mealie.Ingredient) string {
	if ing.Food != nil && strings.TrimSpace(ing.Food.Name) != "" {
		return strings.TrimSpace(ing.Food.Name)
	}
	if strings.TrimSpace(ing.Note) != "" {
		return strings.TrimSpace(ing.Note)
	}
	return strings.TrimSpace(ing.Display)
}

func allergenOf(t mealie.Tag) (string, bool) {
	const prefix = "alergen:"
	n := strings.ToLower(strings.TrimSpace(t.Name))
	if strings.HasPrefix(n, prefix) {
		return strings.TrimSpace(n[len(prefix):]), true
	}
	return "", false
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) fail(w http.ResponseWriter, code int, msg string, err error) {
	s.log.Error(msg, "err", err)
	writeJSON(w, code, map[string]string{"error": msg})
}

// publicPath reports whether a path is reachable without the API key.
func publicPath(p string) bool {
	switch p {
	case "/healthz", "/readyz", "/openapi.json":
		return true
	}
	return false
}

// middleware adds CORS, optional bearer-key auth and request logging.
func middleware(log *slog.Logger, cfg Config, next http.Handler) http.Handler {
	origin := cfg.AllowOrigin
	if origin == "" {
		origin = "*"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Max-Age", "86400")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if cfg.APIKey != "" && !publicPath(r.URL.Path) {
			want := "Bearer " + cfg.APIKey
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(want)) != 1 {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
		}
		next.ServeHTTP(w, r)
		log.Info("request", "method", r.Method, "path", r.URL.Path)
	})
}
