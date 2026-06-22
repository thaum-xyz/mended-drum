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

	"github.com/thaum-xyz/mended-drum/internal/cocktaildb"
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
	log        *slog.Logger
	store      *store.Store
	mealie     *mealie.Client
	cocktaildb *cocktaildb.Client
}

// New returns the HTTP handler for the tool server.
func New(log *slog.Logger, st *store.Store, mc *mealie.Client, cdb *cocktaildb.Client, cfg Config) http.Handler {
	s := &Server{log: log, store: st, mealie: mc, cocktaildb: cdb}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.health)
	mux.HandleFunc("GET /openapi.json", s.openapi)
	mux.HandleFunc("GET /inventory", s.listInventory)
	mux.HandleFunc("PUT /inventory", s.setInventory)
	mux.HandleFunc("GET /recipes/search", s.searchRecipes)
	mux.HandleFunc("GET /recipes/external", s.externalRecipe)
	mux.HandleFunc("GET /recipes/{slug}", s.getRecipe)
	mux.HandleFunc("POST /recipes", s.createRecipe)
	mux.HandleFunc("GET /guests", s.searchGuests)
	mux.HandleFunc("PUT /guests", s.upsertGuest)
	mux.HandleFunc("GET /guests/get", s.getGuest)
	mux.HandleFunc("POST /guests/preferences", s.addGuestPreference)
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

type externalResult struct {
	Name         string               `json:"name"`
	Source       string               `json:"source"`
	IBA          string               `json:"iba,omitempty"`
	Glass        string               `json:"glass,omitempty"`
	Alcoholic    string               `json:"alcoholic,omitempty"`
	Instructions string               `json:"instructions,omitempty"`
	InMealie     bool                 `json:"in_mealie"`
	Makeable     bool                 `json:"makeable"`
	Ingredients  []externalIngredient `json:"ingredients"`
	Missing      []missingItem        `json:"missing"`
}

type externalIngredient struct {
	Name      string `json:"name"`
	Measure   string `json:"measure,omitempty"`
	Available bool   `json:"available"`
}

func (s *Server) externalRecipe(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if strings.TrimSpace(name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	recipes, err := s.cocktaildb.Search(r.Context(), name)
	if err != nil {
		s.fail(w, http.StatusBadGateway, "external lookup", err)
		return
	}
	statuses, err := s.store.Statuses(r.Context())
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "inventory statuses", err)
		return
	}
	out := []externalResult{}
	for _, rc := range recipes {
		res := externalResult{
			Name:         rc.Name,
			Source:       "TheCocktailDB",
			IBA:          rc.IBA,
			Glass:        rc.Glass,
			Alcoholic:    rc.Alcoholic,
			Instructions: rc.Instructions,
			InMealie:     false,
			Makeable:     true,
			Ingredients:  []externalIngredient{},
			Missing:      []missingItem{},
		}
		for _, ing := range rc.Ingredients {
			st, tracked := statuses[store.Key(ing.Name)]
			available := tracked && (st == store.InStock || st == store.Low)
			if !available {
				res.Makeable = false
				reason := "untracked"
				if tracked && st == store.Out {
					reason = "out"
				}
				res.Missing = append(res.Missing, missingItem{Name: ing.Name, Reason: reason})
			}
			res.Ingredients = append(res.Ingredients, externalIngredient{
				Name:      ing.Name,
				Measure:   ing.Measure,
				Available: available,
			})
		}
		out = append(out, res)
	}
	writeJSON(w, http.StatusOK, map[string]any{"external": out})
}

type createRecipeIngredient struct {
	Name    string `json:"name"`
	Measure string `json:"measure"`
}

type createRecipeReq struct {
	Confirm      bool                     `json:"confirm"`
	Name         string                   `json:"name"`
	Ingredients  []createRecipeIngredient `json:"ingredients"`
	Instructions string                   `json:"instructions"`
	Allergens    []string                 `json:"allergens"`
	Source       string                   `json:"source"`
}

// identityTag marks every recipe added by the assistant, for identification.
const identityTag = "mended-drum"

func (s *Server) createRecipe(w http.ResponseWriter, r *http.Request) {
	var req createRecipeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.fail(w, http.StatusBadRequest, "decode body", err)
		return
	}
	if !req.Confirm {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "refusing to write: set confirm=true only after the bartender explicitly approves saving this recipe",
		})
		return
	}
	if strings.TrimSpace(req.Name) == "" || len(req.Ingredients) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and at least one ingredient are required"})
		return
	}

	slug, err := s.mealie.CreateRecipe(r.Context(), strings.TrimSpace(req.Name))
	if err != nil {
		s.fail(w, http.StatusBadGateway, "mealie create recipe", err)
		return
	}

	tagNames := []string{identityTag}
	for _, a := range req.Allergens {
		if a = strings.ToLower(strings.TrimSpace(a)); a != "" {
			tagNames = append(tagNames, "alergen:"+a)
		}
	}
	tags := []mealie.Tag{}
	for _, tn := range tagNames {
		t, err := s.mealie.EnsureTag(r.Context(), tn)
		if err != nil {
			s.log.Warn("ensure tag", "tag", tn, "err", err)
			continue
		}
		tags = append(tags, t)
	}

	// Ingredient notes = names (so the makeable join works); measures go into the
	// instructions, matching the bar's existing recipe convention.
	type ingredientPayload struct {
		Quantity float64 `json:"quantity"`
		Unit     any     `json:"unit"`
		Food     any     `json:"food"`
		Note     string  `json:"note"`
		Display  string  `json:"display"`
	}
	ings := make([]ingredientPayload, 0, len(req.Ingredients))
	var measured strings.Builder
	measured.WriteString("Składniki:\n")
	for _, ing := range req.Ingredients {
		name := strings.TrimSpace(ing.Name)
		if name == "" {
			continue
		}
		ings = append(ings, ingredientPayload{Note: name, Display: name})
		line := name
		if m := strings.TrimSpace(ing.Measure); m != "" {
			line = m + " " + name
		}
		measured.WriteString("- " + line + "\n")
	}

	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = "asystent"
	}
	text := measured.String()
	if instr := strings.TrimSpace(req.Instructions); instr != "" {
		text += "\n" + instr
	}
	text += "\n\n(Dodane przez Mended Drum — źródło: " + source + ")"

	// Mealie updates via GET-modify-PUT of the full object (a partial PATCH 500s).
	raw, err := s.mealie.GetRecipeRaw(r.Context(), slug)
	if err != nil {
		s.fail(w, http.StatusBadGateway, "mealie get stub recipe", err)
		return
	}
	raw["recipeIngredient"] = ings
	raw["recipeInstructions"] = []map[string]any{{"title": "", "summary": "", "text": text, "ingredientReferences": []any{}}}
	raw["tags"] = tags
	if err := s.mealie.PutRecipe(r.Context(), slug, raw); err != nil {
		s.fail(w, http.StatusBadGateway, "mealie update recipe", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"slug": slug, "tags": tagNames, "saved": true})
}

func (s *Server) searchGuests(w http.ResponseWriter, r *http.Request) {
	guests, err := s.store.SearchGuests(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "search guests", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"guests": guests})
}

type upsertGuestReq struct {
	Handle string `json:"handle"`
	Notes  string `json:"notes"`
}

func (s *Server) upsertGuest(w http.ResponseWriter, r *http.Request) {
	var req upsertGuestReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.fail(w, http.StatusBadRequest, "decode body", err)
		return
	}
	if strings.TrimSpace(req.Handle) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "handle is required"})
		return
	}
	if err := s.store.UpsertGuest(r.Context(), req.Handle, req.Notes); err != nil {
		s.fail(w, http.StatusInternalServerError, "upsert guest", err)
		return
	}
	g, err := s.store.GetGuest(r.Context(), req.Handle)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "get guest", err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) getGuest(w http.ResponseWriter, r *http.Request) {
	handle := r.URL.Query().Get("handle")
	if strings.TrimSpace(handle) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "handle is required"})
		return
	}
	g, err := s.store.GetGuest(r.Context(), handle)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "get guest", err)
		return
	}
	if g == nil {
		writeJSON(w, http.StatusOK, map[string]any{"found": false, "handle": handle})
		return
	}
	writeJSON(w, http.StatusOK, g)
}

type addPrefReq struct {
	Handle string `json:"handle"`
	Kind   string `json:"kind"`
	Value  string `json:"value"`
}

func (s *Server) addGuestPreference(w http.ResponseWriter, r *http.Request) {
	var req addPrefReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.fail(w, http.StatusBadRequest, "decode body", err)
		return
	}
	if strings.TrimSpace(req.Handle) == "" || !store.ValidPrefKind(req.Kind) || strings.TrimSpace(req.Value) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "handle, value and kind (like|dislike|allergy) are required",
		})
		return
	}
	if err := s.store.AddPreference(r.Context(), req.Handle, req.Kind, req.Value); err != nil {
		s.fail(w, http.StatusInternalServerError, "add preference", err)
		return
	}
	g, err := s.store.GetGuest(r.Context(), req.Handle)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "get guest", err)
		return
	}
	writeJSON(w, http.StatusOK, g)
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

// protectedPath reports whether a path requires the API key. Only the tool
// endpoints are gated; everything else (health, openapi, and unknown probes
// such as Open WebUI's /api/config) falls through to the router — returning a
// clean 404 for unknown paths rather than a hostile 401 that breaks clients.
func protectedPath(p string) bool {
	return strings.HasPrefix(p, "/inventory") ||
		strings.HasPrefix(p, "/recipes") ||
		strings.HasPrefix(p, "/guests")
}

// setCORS reflects the caller's Origin so the tool server works regardless of
// which host Open WebUI is served from. A configured non-"*" AllowOrigin pins
// that value instead.
func setCORS(w http.ResponseWriter, r *http.Request, cfg Config) {
	w.Header().Set("Vary", "Origin")
	w.Header().Add("Vary", "Access-Control-Request-Headers")
	w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, OPTIONS")
	// Reflect whatever headers the caller asks for (Open WebUI sends extras like
	// x-session-id); fall back to the basics for non-preflight requests.
	if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
		w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
	} else {
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	}
	w.Header().Set("Access-Control-Max-Age", "86400")

	reqOrigin := r.Header.Get("Origin")
	switch {
	case cfg.AllowOrigin != "" && cfg.AllowOrigin != "*":
		w.Header().Set("Access-Control-Allow-Origin", cfg.AllowOrigin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	case reqOrigin != "":
		w.Header().Set("Access-Control-Allow-Origin", reqOrigin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	default:
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// middleware adds CORS, optional bearer-key auth and logs every request
// (method, path, status, origin) — including OPTIONS/401/404 — for diagnostics.
func middleware(log *slog.Logger, cfg Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setCORS(w, r, cfg)
		origin := r.Header.Get("Origin")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			log.Info("request", "method", r.Method, "path", r.URL.Path, "status", http.StatusNoContent, "origin", origin)
			return
		}
		if cfg.APIKey != "" && protectedPath(r.URL.Path) {
			want := "Bearer " + cfg.APIKey
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(want)) != 1 {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				log.Warn("request", "method", r.Method, "path", r.URL.Path, "status", http.StatusUnauthorized,
					"origin", origin, "had_auth", r.Header.Get("Authorization") != "")
				return
			}
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.status, "origin", origin)
	})
}
