// Command mended-drum runs the Mended Drum bar-assistant tool server: a 3-state
// ingredient inventory plus the Mealie-backed recipe book, exposed as an
// OpenAPI tool server for Open WebUI.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thaum-xyz/mended-drum/internal/cocktaildb"
	"github.com/thaum-xyz/mended-drum/internal/mealie"
	"github.com/thaum-xyz/mended-drum/internal/server"
	"github.com/thaum-xyz/mended-drum/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	st, err := store.Open(envOr("DB_PATH", "/data/mended-drum.db"))
	if err != nil {
		logger.Error("open store", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	mc := mealie.New(os.Getenv("MEALIE_BASE_URL"), os.Getenv("MEALIE_API_TOKEN"))
	cdb := cocktaildb.New()

	cfg := server.Config{
		APIKey:      os.Getenv("TOOLS_API_KEY"),
		AllowOrigin: envOr("CORS_ALLOW_ORIGIN", "*"),
	}

	addr := ":" + envOr("PORT", "8080")
	srv := &http.Server{
		Addr:              addr,
		Handler:           server.New(logger, st, mc, cdb, cfg),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "err", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
