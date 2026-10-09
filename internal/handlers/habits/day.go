// Package habits holds the habit-tracker domain routes: the day view with
// check-ins plus the settings page with every configuration mutation.
// Shape per endpoint: parse/validate transport, load data from the habit
// service, render templ. A future JSON renderer branches at the render
// step only (Accept header), the service stays untouched.
package habits

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/antoni-ostrowski/habit-tracker/internal/habit"
	"github.com/antoni-ostrowski/habit-tracker/internal/handlers"
	"github.com/antoni-ostrowski/habit-tracker/internal/handlers/auth"
	"github.com/antoni-ostrowski/habit-tracker/templates"
	"github.com/google/uuid"
)

// Register wires the habit domain onto mux. The day page is public and
// renders sign-in prompts when anonymous; everything else needs identity.
func Register(mux *http.ServeMux, d handlers.Deps) {
	handlers.Route(mux, "GET /{$}", auth.WithAuth(handlePage(d), d))
	handlers.Route(mux, "POST /checkins/toggle", auth.RequireAuth(handleToggle(d), d))
	RegisterSettings(mux, d)
}

// parseDay reads ?date=YYYY-MM-DD, defaulting to server today.
func parseDay(r *http.Request) (time.Time, error) {
	s := r.URL.Query().Get("date")
	if s == "" {
		return habit.ServerToday(), nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, errors.New("date must be YYYY-MM-DD")
	}
	return t, nil
}

// buildWeek builds the Mon-Sun strip containing selected.
func buildWeek(selected time.Time) []templates.WeekDay {
	start := habit.WeekStart(selected)
	week := make([]templates.WeekDay, 0, 7)
	for i := range 7 {
		d := start.AddDate(0, 0, i)
		week = append(week, templates.WeekDay{
			ISO:        d.Format("2006-01-02"),
			Dow:        d.Format("Mon"),
			Dom:        strconv.Itoa(d.Day()),
			IsToday:    habit.IsToday(d),
			IsSelected: d.Equal(habit.TruncateDay(selected)),
		})
	}
	return week
}

func handlePage(d handlers.Deps) auth.AuthedHandler {
	return func(w http.ResponseWriter, r *http.Request, a auth.AuthData) {
		if a.UserID == uuid.Nil {
			if err := templates.AnonymousDay().Render(r.Context(), w); err != nil {
				handlers.WriteError(w, r, d.Logger, err, http.StatusInternalServerError)
			}
			return
		}
		day, err := parseDay(r)
		if err != nil {
			handlers.WriteError(w, r, d.Logger, err, http.StatusBadRequest)
			return
		}
		ctx := r.Context()
		v, err := habit.LoadDay(ctx, d.Queries, a.UserID, day)
		if err != nil {
			handlers.WriteError(w, r, d.Logger, err, http.StatusInternalServerError)
			return
		}
		if err := templates.DayPage(v, buildWeek(day), a.Username).Render(ctx, w); err != nil {
			handlers.WriteError(w, r, d.Logger, err, http.StatusInternalServerError)
		}
	}
}

// handleToggle checks/unchecks one habit for a day and returns the day
// panel fragment. Today only; past/future are 403.
func handleToggle(d handlers.Deps) auth.AuthedHandler {
	return func(w http.ResponseWriter, r *http.Request, a auth.AuthData) {
		habitID, err := strconv.ParseInt(r.FormValue("habit_id"), 10, 64)
		if err != nil || habitID < 1 {
			handlers.WriteError(w, r, d.Logger, errors.New("invalid habit_id"), http.StatusBadRequest)
			return
		}
		day, err := time.Parse("2006-01-02", r.FormValue("day"))
		if err != nil {
			handlers.WriteError(w, r, d.Logger, errors.New("day must be YYYY-MM-DD"), http.StatusBadRequest)
			return
		}
		ctx := r.Context()
		if _, err := habit.ToggleCheckin(ctx, d.Queries, a.UserID, habitID, day); err != nil {
			toggleErr(w, r, d, err)
			return
		}
		v, err := habit.LoadDay(ctx, d.Queries, a.UserID, day)
		if err != nil {
			handlers.WriteError(w, r, d.Logger, err, http.StatusInternalServerError)
			return
		}
		if err := templates.DayPanel(v).Render(ctx, w); err != nil {
			handlers.WriteError(w, r, d.Logger, err, http.StatusInternalServerError)
		}
	}
}

func toggleErr(w http.ResponseWriter, r *http.Request, d handlers.Deps, err error) {
	switch {
	case errors.Is(err, habit.ErrNotEditable):
		handlers.WriteError(w, r, d.Logger, err, http.StatusForbidden)
	case errors.Is(err, habit.ErrNotFound):
		handlers.WriteError(w, r, d.Logger, err, http.StatusNotFound)
	default:
		handlers.WriteError(w, r, d.Logger, err, http.StatusInternalServerError)
	}
}
