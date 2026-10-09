// Package health is the liveness/readiness probe: GET /healthz pings
// the database and answers 200 "ok" or 503. No auth: load balancers
// and compose call it unauthenticated.
package health

import (
	"net/http"

	"github.com/antoni-ostrowski/habit-tracker/internal/handlers"
)

// Register wires the probe onto mux.
func Register(mux *http.ServeMux, d handlers.Deps) {
	handlers.Route(mux, "GET /healthz", http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if err := d.Pool.Ping(r.Context()); err != nil {
				handlers.WriteError(w, r, d.Logger, err, http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("ok"))
		}))
}
