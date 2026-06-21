// Package server wires the HTTP routes for the mended-drum tool server.
package server

import (
	"log/slog"
	"net/http"
)

// New returns the HTTP handler for the tool server.
func New(logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /readyz", health)
	// TODO(phase-1): expose inventory, recipes and guest tools as an OpenAPI spec
	// for Open WebUI (Mealie-backed recipes, 3-state inventory, guest prefs).
	return logging(logger, mux)
}

func health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func logging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		logger.Info("request", "method", r.Method, "path", r.URL.Path)
	})
}
