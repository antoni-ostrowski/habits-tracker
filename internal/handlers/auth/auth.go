// Package auth holds sign-up, sign-in, and sign-out plus the password
// hashing helpers. Sessions carry user_id and username; handlers.Deps
// stays untouched.
package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	db "github.com/antoni-ostrowski/habit-tracker/internal/db/sqlc"
	"github.com/antoni-ostrowski/habit-tracker/internal/handlers"
	"github.com/antoni-ostrowski/habit-tracker/templates"

	"github.com/a-h/templ"
	"github.com/alexedwards/scs/pgxstore"
	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func NewSessionManager(pool *pgxpool.Pool) *scs.SessionManager {
	sessions := scs.New()
	sessions.Store = pgxstore.New(pool)
	sessions.Lifetime = 7 * 24 * 60 * time.Minute
	return sessions
}

func NewUserID() (uuid.UUID, error) {
	return uuid.NewV7()
}

// HashPassword bcrypt-hashes password for storage. Never store plaintext.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// CheckPassword reports whether password matches the stored hash.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// Register wires the auth routes onto mux.
func Register(mux *http.ServeMux, d handlers.Deps) {
	handlers.Route(mux, "GET /signup", showSignup(d))
	handlers.Route(mux, "POST /signup", handleSignup(d))
	handlers.Route(mux, "GET /signin", showSignin(d))
	handlers.Route(mux, "POST /signin", handleSignin(d))
	handlers.Route(mux, "POST /signout", handleSignout(d))
}

func showSignup(d handlers.Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := templates.Signup("").Render(r.Context(), w); err != nil {
			handlers.WriteError(w, r, d.Logger, err, http.StatusInternalServerError)
		}
	}
}

func handleSignup(d handlers.Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		username := strings.TrimSpace(r.FormValue("username"))
		password := r.FormValue("password")
		if username == "" {
			renderAuth(w, r, d.Logger, http.StatusUnprocessableEntity, templates.Signup("username cannot be empty"))
			return
		}
		if len(password) < 8 {
			renderAuth(w, r, d.Logger, http.StatusUnprocessableEntity, templates.Signup("password must be at least 8 characters"))
			return
		}
		hash, err := HashPassword(password)
		if err != nil {
			handlers.WriteError(w, r, d.Logger, err, http.StatusInternalServerError)
			return
		}
		id, err := NewUserID()
		if err != nil {
			handlers.WriteError(w, r, d.Logger, err, http.StatusInternalServerError)
			return
		}
		user, err := d.Queries.CreateUser(ctx, db.CreateUserParams{
			ID: id, Username: username, PasswordHash: hash,
		})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				renderAuth(w, r, d.Logger, http.StatusConflict, templates.Signup("username is taken"))
				return
			}
			handlers.WriteError(w, r, d.Logger, err, http.StatusInternalServerError)
			return
		}
		if err := login(d, r, user); err != nil {
			handlers.WriteError(w, r, d.Logger, err, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Clear-Site-Data", `"cache"`)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

func showSignin(d handlers.Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := templates.Signin("").Render(r.Context(), w); err != nil {
			handlers.WriteError(w, r, d.Logger, err, http.StatusInternalServerError)
		}
	}
}

func handleSignin(d handlers.Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user, err := d.Queries.GetUserByUsername(ctx, strings.TrimSpace(r.FormValue("username")))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				renderAuth(w, r, d.Logger, http.StatusUnauthorized, templates.Signin("invalid username or password"))
				return
			}
			handlers.WriteError(w, r, d.Logger, err, http.StatusInternalServerError)
			return
		}
		if !CheckPassword(user.PasswordHash, r.FormValue("password")) {
			renderAuth(w, r, d.Logger, http.StatusUnauthorized, templates.Signin("invalid username or password"))
			return
		}
		if err := login(d, r, user); err != nil {
			handlers.WriteError(w, r, d.Logger, err, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Clear-Site-Data", `"cache"`)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

func handleSignout(d handlers.Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := d.Sessions.Destroy(r.Context()); err != nil {
			handlers.WriteError(w, r, d.Logger, err, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Clear-Site-Data", `"cache"`)
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
	}
}

// login stores the user in a fresh session. RenewToken first so a login
// never inherits a pre-login session (fixation).
func login(d handlers.Deps, r *http.Request, user db.User) error {
	ctx := r.Context()
	if err := d.Sessions.RenewToken(ctx); err != nil {
		return err
	}
	d.Sessions.Put(ctx, "user_id", user.ID.String())
	d.Sessions.Put(ctx, "username", user.Username)
	return nil
}

func renderAuth(w http.ResponseWriter, r *http.Request, logger *slog.Logger, code int, page templ.Component) {
	w.WriteHeader(code)
	if err := page.Render(r.Context(), w); err != nil {
		handlers.WriteError(w, r, logger, err, http.StatusInternalServerError)
	}
}

type AuthData struct {
	UserID   uuid.UUID
	Username string
}

func (a AuthData) GetUserData(ctx context.Context, d handlers.Deps) (db.User, error) {
	return d.Queries.GetUserById(ctx, a.UserID)
}

type AuthedHandler func(w http.ResponseWriter, r *http.Request, a AuthData)

// WithAuth supplies request identity to next and nothing else. Anonymous
// requests get zero AuthData, never a redirect. This is the single place
// that reads the session for identity.
func WithAuth(next AuthedHandler, d handlers.Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var a AuthData
		if id, err := uuid.Parse(d.Sessions.GetString(ctx, "user_id")); err == nil {
			a = AuthData{UserID: id, Username: d.Sessions.GetString(ctx, "username")}
		}
		next(w, r, a)
	}
}

// RequireAuth enforces identity: anonymous htmx requests get 401 with
// HX-Redirect (the client navigates); plain requests get a 303 redirect.
// Each redirect is logged so auth gates stay auditable.
func RequireAuth(next AuthedHandler, d handlers.Deps) http.HandlerFunc {
	return WithAuth(func(w http.ResponseWriter, r *http.Request, a AuthData) {
		if a.UserID == uuid.Nil {
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Redirect", "/signin")
				d.Logger.InfoContext(r.Context(), "auth required", "method", r.Method, "path", r.URL.Path,
					"status", http.StatusUnauthorized)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			d.Logger.InfoContext(r.Context(), "auth required", "method", r.Method, "path", r.URL.Path,
				"status", http.StatusSeeOther)
			http.Redirect(w, r, "/signin", http.StatusSeeOther)
			return
		}
		next(w, r, a)
	}, d)
}
