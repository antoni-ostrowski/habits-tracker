// Package server composes the whole HTTP serving stack: every domain
// registered on one mux behind session middleware and the otelhttp server
// span, plus the configured http.Server. main.go and the integration suite
// share it so route wiring can't drift; only the deps (pool, logger,
// static dir) differ per environment.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	db "github.com/antoni-ostrowski/habit-tracker/internal/db/sqlc"
	"github.com/antoni-ostrowski/habit-tracker/internal/handlers"
	"github.com/antoni-ostrowski/habit-tracker/internal/handlers/auth"
	"github.com/antoni-ostrowski/habit-tracker/internal/handlers/habits"
	"github.com/antoni-ostrowski/habit-tracker/internal/handlers/health"
	"github.com/antoni-ostrowski/habit-tracker/internal/handlers/static"
	"github.com/antoni-ostrowski/habit-tracker/internal/obs"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
)

// Config carries everything Run needs that differs per environment.
// main.go fills it from env; tests fill it with test values.
type Config struct {
	DatabaseURL string
	StaticDir   string
	Addr        string
	Service     string
}

// Run boots the full stack — OTel SDK, logger, pool, sessions, server — and
// serves until ctx is done, then drains gracefully. main.go calls it with
// env config; the smoke test calls it with test config to prove the real
// wiring serves. Without an OTLP endpoint the SDK stays noop (one warning)
// and everything else runs unchanged.
func Run(ctx context.Context, cfg Config) error {
	otelShutdown, err := obs.SetupOTelSDK(ctx, cfg.Service)
	if err != nil && !errors.Is(err, obs.ErrNoEndpoint) {
		return fmt.Errorf("setup otel SDK: %w", err)
	}
	logger := slog.New(obs.NewLogHandler(cfg.Service))
	slog.SetDefault(logger)
	if err != nil {
		logger.Warn("otel disabled", "reason", err)
	}
	defer func() {
		if otelShutdown == nil {
			return
		}
		if err := otelShutdown(context.Background()); err != nil {
			logger.Error("shutdown otel SDK", "error", err)
		}
	}()

	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("create database pool: %w", err)
	}
	defer pool.Close()

	sessions := auth.NewSessionManager(pool)
	deps := handlers.Deps{
		Queries:  db.New(pool),
		Pool:     pool,
		Sessions: sessions, Logger: logger,
		Tel: &handlers.Telemetry{Tracer: otel.Tracer(cfg.Service), Meter: otel.Meter(cfg.Service)},
	}
	srv := NewServer(deps, cfg.StaticDir, cfg.Addr)

	srvErr := make(chan error, 1)
	go func() {
		logger.Info("listening", "address", "http://"+cfg.Addr)
		srvErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-srvErr:
		// Startup failed: nothing to drain, and the pool never served
		// traffic, so exiting directly is safe.
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		// Drain in-flight work within budget.
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdown); err != nil {
			return err
		}
		logger.Info("stopped")
		return nil
	}
}

// NewServer builds the production-ready http.Server around NewHandler.
func NewServer(d handlers.Deps, staticDir, addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           NewHandler(d, staticDir),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
}

// NewHandler registers every domain and wraps the mux. staticDir is "static"
// in prod, the test tree path in tests.
func NewHandler(d handlers.Deps, staticDir string) http.Handler {
	mux := http.NewServeMux()
	health.Register(mux, d)
	habits.Register(mux, d)
	auth.Register(mux, d)
	static.Register(mux, staticDir)
	return otelhttp.NewHandler(d.Sessions.LoadAndSave(mux), "server")
}
