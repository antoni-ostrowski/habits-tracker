package integration_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/antoni-ostrowski/habit-tracker/internal/server"
)

// TestSmoke_RunServes boots the real server.Run stack (OTel path, pool,
// sessions, all domains) on a live port and asserts the app serves over a
// real socket. This is the one test covering main's wiring; everything else
// uses NewHandler in-process for speed and precision.
func TestSmoke_RunServes(t *testing.T) {
	testPool(t) // ensures schema; skips without INTEGRATION_TESTS=1

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://postgres:postgres@localhost:5432/habits?sslmode=disable"
	}
	prev := slog.Default()
	defer slog.SetDefault(prev)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() {
		runErr <- server.Run(ctx, server.Config{
			DatabaseURL: databaseURL,
			StaticDir:   "../../static",
			Addr:        "127.0.0.1:18081",
			Service:     "smoke",
		})
	}()

	// Wait for ready: anonymous GET / renders sign-in links.
	var body string
	for range 100 {
		resp, err := http.Get("http://127.0.0.1:18081/")
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				body = string(b)
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if body == "" {
		t.Fatal("server never became ready")
	}
	for _, want := range []string{"sign in", "/signin"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}

	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Run did not shut down")
	}
}
