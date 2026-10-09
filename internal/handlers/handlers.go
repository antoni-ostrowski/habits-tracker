// Package handlers holds shared HTTP wiring: the Deps bag plus helpers
// every domain uses. Domains live in subpackages and import this package,
// never the reverse.
package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	db "github.com/antoni-ostrowski/habit-tracker/internal/db/sqlc"

	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Deps carries process-wide dependencies, built once by the caller.
// New shared deps join here as fields; never request-scoped data.
type Deps struct {
	Queries  *db.Queries
	Pool     *pgxpool.Pool
	Sessions *scs.SessionManager
	Logger   *slog.Logger
	Tel      *Telemetry
}

type Telemetry struct {
	Tracer trace.Tracer
	Meter  metric.Meter
}

// WriteError logs err with request context and renders it. 4xx bodies carry
// the message (client-caused, safe); 5xx bodies stay generic while the real
// error goes to the log. It uses ErrorContext so the record correlates with
// the request's trace, and marks the span failed for 5xx (RecordError +
// Error status) so traces show red. 4xx stays green: client errors are
// expected behavior, not failures.
func WriteError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error, code int) {
	if logger == nil {
		logger = slog.Default()
	}
	logger.ErrorContext(r.Context(), "handler error",
		"method", r.Method, "path", r.URL.Path, "status", code, "err", err)
	if code >= 500 {
		span := trace.SpanFromContext(r.Context())
		span.RecordError(err)
		span.SetStatus(codes.Error, http.StatusText(code))
		http.Error(w, http.StatusText(code), code)
		return
	}
	http.Error(w, err.Error(), code)
}

// Route registers pattern on mux and renames the otelhttp server span to the
// matched pattern (r.Pattern). Without it the outer otelhttp handler names
// every span after its static operation string, collapsing all routes into
// one useless span name.
func Route(mux *http.ServeMux, pattern string, h http.Handler) {
	mux.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s := trace.SpanFromContext(r.Context()); s.IsRecording() {
			if p := r.Pattern; p != "" {
				s.SetName(p)
			}
		}
		h.ServeHTTP(w, r)
	}))
}

// ParseID reads the {id} path value as a positive integer.
func ParseID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}
