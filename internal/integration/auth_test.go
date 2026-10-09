package integration_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/antoni-ostrowski/habit-tracker/internal/handlers/auth"
)

// doSignup posts the signup form, returning the response and the session
// cookie when one was set.
func doSignup(t *testing.T, app http.Handler, username, password string) (*http.Cookie, *httptest.ResponseRecorder) {
	t.Helper()
	rec := Do(t, app, http.MethodPost, "/signup",
		url.Values{"username": {username}, "password": {password}}, nil, nil)
	var cookie *http.Cookie
	if cookies := rec.Result().Cookies(); len(cookies) > 0 {
		cookie = cookies[0]
	}
	return cookie, rec
}

func userCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := testPool(t).QueryRow(context.Background(), `SELECT count(*) FROM users`).Scan(&n); err != nil {
		t.Fatalf("count users: %v", err)
	}
	return n
}

// POST /signup stores a hashed password, logs the user in, and the session
// immediately works for habit mutations.
func TestSignup_CreatesUserAndLogsIn(t *testing.T) {
	app, q, _, _ := setup(t)

	cookie, rec := doSignup(t, app, "alice", "password123")
	WantCode(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Fatalf("location = %q, want /", loc)
	}
	if h := rec.Header().Get("Clear-Site-Data"); h != `"cache"` {
		t.Fatalf("Clear-Site-Data = %q, want cache prune", h)
	}
	if cookie == nil {
		t.Fatal("no session cookie set on signup")
	}

	user, err := q.GetUserByUsername(context.Background(), "alice")
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if user.PasswordHash == "password123" {
		t.Error("password stored in plaintext")
	}
	if !auth.CheckPassword(user.PasswordHash, "password123") {
		t.Error("stored hash does not verify")
	}

	rec = Do(t, app, http.MethodPost, "/habits", url.Values{"name": {"first"}, "weight": {"1"}}, cookie, nil)
	WantCode(t, rec, http.StatusOK)
	WantBody(t, rec, "first")

	rec = Do(t, app, http.MethodGet, "/", nil, cookie, nil)
	WantCode(t, rec, http.StatusOK)
	WantBody(t, rec, "alice")
}

// A taken username is 409 and stores nothing new.
func TestSignup_Duplicate(t *testing.T) {
	app, _, _, _ := setup(t)

	if _, rec := doSignup(t, app, "alice", "password123"); rec.Code != http.StatusSeeOther {
		t.Fatalf("first signup status = %d, want 303", rec.Code)
	}
	rec := Do(t, app, http.MethodPost, "/signup",
		url.Values{"username": {"alice"}, "password": {"password123"}}, nil, nil)
	WantCode(t, rec, http.StatusConflict)
	WantBody(t, rec, "taken")
	if n := userCount(t); n != 2 { // setup user + alice
		t.Fatalf("users = %d, want 2", n)
	}
}

// Blank username and short passwords are 422 and store nothing.
func TestSignup_Validation(t *testing.T) {
	app, _, _, _ := setup(t)

	for _, tt := range []struct{ username, password string }{
		{"", "password123"},
		{"   ", "password123"},
		{"bob", ""},
		{"bob", "short"},
	} {
		rec := Do(t, app, http.MethodPost, "/signup",
			url.Values{"username": {tt.username}, "password": {tt.password}}, nil, nil)
		WantCode(t, rec, http.StatusUnprocessableEntity)
	}
	if n := userCount(t); n != 1 { // only the setup user
		t.Fatalf("users = %d, want 1", n)
	}
}

// POST /signin with correct credentials logs in through the real form.
func TestSignin(t *testing.T) {
	app, _, _, _ := setup(t)
	seedUser(t, "carol")

	rec := Do(t, app, http.MethodPost, "/signin",
		url.Values{"username": {"carol"}, "password": {"password123"}}, nil, nil)
	WantCode(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Fatalf("location = %q, want /", loc)
	}
	if h := rec.Header().Get("Clear-Site-Data"); h != `"cache"` {
		t.Fatalf("Clear-Site-Data = %q, want cache prune", h)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no session cookie set on signin")
	}

	mut := Do(t, app, http.MethodPost, "/habits", url.Values{"name": {"via signin"}, "weight": {"1"}}, cookies[0], nil)
	WantCode(t, mut, http.StatusOK)
	WantBody(t, mut, "via signin")
}

// Wrong password and unknown users are 401 without revealing which failed.
func TestSignin_RejectsBadCredentials(t *testing.T) {
	app, _, _, _ := setup(t)
	seedUser(t, "carol")

	for _, tt := range []struct{ username, password string }{
		{"carol", "wrongpassword"},
		{"nobody", "password123"},
		{"", ""},
	} {
		rec := Do(t, app, http.MethodPost, "/signin",
			url.Values{"username": {tt.username}, "password": {tt.password}}, nil, nil)
		WantCode(t, rec, http.StatusUnauthorized)
		WantBody(t, rec, "invalid username or password")
	}
}

// POST /signout destroys the session; the old cookie stops working.
func TestSignout(t *testing.T) {
	app, _, _, _ := setup(t)

	cookie, signupRec := doSignup(t, app, "dave", "password123")
	if signupRec.Code != http.StatusSeeOther || cookie == nil {
		t.Fatalf("signup failed: status = %d, cookie = %v", signupRec.Code, cookie)
	}

	rec := Do(t, app, http.MethodPost, "/signout", nil, cookie, nil)
	WantCode(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/signin" {
		t.Fatalf("location = %q, want /signin", loc)
	}
	if h := rec.Header().Get("Clear-Site-Data"); h != `"cache"` {
		t.Fatalf("Clear-Site-Data = %q, want cache prune", h)
	}

	rec = Do(t, app, http.MethodPost, "/habits", url.Values{"name": {"x"}, "weight": {"1"}}, cookie, nil)
	WantCode(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/signin" {
		t.Fatalf("location = %q, want /signin", loc)
	}
}
