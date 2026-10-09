package integration_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"testing"

	db "github.com/antoni-ostrowski/habit-tracker/internal/db/sqlc"
	"github.com/antoni-ostrowski/habit-tracker/internal/handlers"
	"github.com/antoni-ostrowski/habit-tracker/internal/handlers/auth"
	"github.com/antoni-ostrowski/habit-tracker/internal/server"

	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
)

const schemaFile = "../db/schema.sql"

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return Pool(t, schemaFile)
}

// testPassword is the plaintext for every seeded user (see seedUser).
const testPassword = "password123"

// login signs in through the real /signin form and returns the session
// cookie. Todo tests use the real sign-in deliberately, so auth changes
// break them too — no forged sessions.
func login(t *testing.T, app http.Handler, q *db.Queries, user uuid.UUID) *http.Cookie {
	t.Helper()
	u, err := q.GetUserById(context.Background(), user)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	rec := Do(t, app, http.MethodPost, "/signin",
		url.Values{"username": {u.Username}, "password": {testPassword}}, nil, nil)
	WantCode(t, rec, http.StatusSeeOther)
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("signin: no session cookie set")
	}
	return cookies[0]
}
func seedUser(t *testing.T, username string) uuid.UUID {
	t.Helper()
	hash, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	id, err := auth.NewUserID()
	if err != nil {
		t.Fatalf("new user id: %v", err)
	}
	if username == "" {
		username = "test-" + id.String()
	}
	if _, err := testPool(t).Exec(context.Background(),
		`INSERT INTO users (id, username, password_hash) VALUES ($1, $2, $3)`,
		id, username, hash); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

// setup builds the whole app through app.NewHandler, exactly like main.go —
// only the deps (test pool, discard logger, static path) differ. Register
// new domains in app.NewHandler once; both callers follow.
func setup(t *testing.T) (http.Handler, *db.Queries, *scs.SessionManager, uuid.UUID) {
	t.Helper()
	p := Pool(t, schemaFile)
	Truncate(t, p, "checkins", "habits", "sessions", "users")
	user := seedUser(t, "")
	q := db.New(p)
	sessions := auth.NewSessionManager(p)
	// No OTel SDK in tests: discard logs, noop tracer/meter (global defaults).
	d := handlers.Deps{
		Queries:  q,
		Pool:     p,
		Sessions: sessions,
		Logger:   slog.New(slog.DiscardHandler),
		Tel:      &handlers.Telemetry{Tracer: otel.Tracer("test"), Meter: otel.Meter("test")},
	}
	return server.NewHandler(d, "../../static"), q, sessions, user
}
