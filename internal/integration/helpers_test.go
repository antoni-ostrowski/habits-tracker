// Shared helpers for the integration suite (request driving, DB setup,
// response assertions). Single-package suite, so they live here next to the
// tests instead of a separate helpers package.
package integration_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	poolOnce sync.Once
	tstPool  *pgxpool.Pool
	tstErr   error
)

// Pool connects to PostgreSQL once per test binary and applies schemaFile.
// Skips unless INTEGRATION_TESTS=1.
func Pool(t *testing.T, schemaFile string) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("INTEGRATION_TESTS") != "1" {
		t.Skip("set INTEGRATION_TESTS=1 to run PostgreSQL integration tests")
	}

	ctx := context.Background()
	poolOnce.Do(func() {
		databaseURL := os.Getenv("DATABASE_URL")
		if databaseURL == "" {
			databaseURL = "postgres://postgres:postgres@localhost:5432/todos?sslmode=disable"
		}
		tstPool, tstErr = pgxpool.New(ctx, databaseURL)
		if tstErr != nil {
			return
		}
		if tstErr = tstPool.Ping(ctx); tstErr != nil {
			return
		}
		schema, err := os.ReadFile(schemaFile)
		if err != nil {
			tstErr = err
			return
		}
		if _, tstErr = tstPool.Exec(ctx, "DROP TABLE IF EXISTS todos, sessions, users;"+string(schema)); tstErr != nil {
			return
		}
	})
	if tstErr != nil {
		t.Fatalf("integration pool: %v", tstErr)
	}
	return tstPool
}

// Truncate empties tables. RESTART IDENTITY keeps IDs deterministic.
func Truncate(t *testing.T, p *pgxpool.Pool, tables ...string) {
	t.Helper()
	q := "TRUNCATE " + strings.Join(tables, ", ") + " RESTART IDENTITY"
	if _, err := p.Exec(context.Background(), q); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

// Do sends one in-memory request through app. A non-nil form is sent as
// urlencoded (what HTMX posts look like); cookie carries the session;
// headers adds extra request headers (nil for none).
func Do(t *testing.T, app http.Handler, method, target string, form url.Values, cookie *http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, target, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	return rec
}

// DoHtmx is Do with HX-Request set, mimicking a real htmx-issued request.
func DoHtmx(t *testing.T, app http.Handler, method, target string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	return Do(t, app, method, target, form, cookie, map[string]string{"HX-Request": "true"})
}

// WantCode asserts the status, printing the body on mismatch.
func WantCode(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d, body: %q", rec.Code, want, rec.Body.String())
	}
}

// WantBody asserts the body contains each substring. Never exact HTML.
func WantBody(t *testing.T, rec *httptest.ResponseRecorder, want ...string) {
	t.Helper()
	for _, s := range want {
		if !strings.Contains(rec.Body.String(), s) {
			t.Fatalf("body does not contain %q\nbody: %q", s, rec.Body.String())
		}
	}
}

// WantNoBody asserts the body contains none of the substrings.
func WantNoBody(t *testing.T, rec *httptest.ResponseRecorder, notWant ...string) {
	t.Helper()
	for _, s := range notWant {
		if strings.Contains(rec.Body.String(), s) {
			t.Fatalf("body should not contain %q\nbody: %q", s, rec.Body.String())
		}
	}
}
