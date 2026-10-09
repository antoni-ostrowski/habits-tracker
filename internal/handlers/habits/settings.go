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

// Register wires the settings routes onto mux. handlePage, handleToggle
// and the day view live in day.go; this file owns configuration.
func RegisterSettings(mux *http.ServeMux, d handlers.Deps) {
	handlers.Route(mux, "GET /settings", auth.RequireAuth(handleSettings(d), d))
	handlers.Route(mux, "POST /habits", auth.RequireAuth(handleAddHabit(d), d))
	handlers.Route(mux, "POST /habits/{id}", auth.RequireAuth(handleEditHabit(d), d))
	handlers.Route(mux, "DELETE /habits/{id}", auth.RequireAuth(handleDeleteHabit(d), d))
	handlers.Route(mux, "POST /day-zero", auth.RequireAuth(handleDayZero(d), d))
}

func handleSettings(d handlers.Deps) auth.AuthedHandler {
	return func(w http.ResponseWriter, r *http.Request, a auth.AuthData) {
		data, err := habit.LoadSettings(r.Context(), d.Queries, a.UserID)
		if err != nil {
			handlers.WriteError(w, r, d.Logger, err, http.StatusInternalServerError)
			return
		}
		if err := templates.SettingsPage(data, a.Username).Render(r.Context(), w); err != nil {
			handlers.WriteError(w, r, d.Logger, err, http.StatusInternalServerError)
		}
	}
}

// renderSettings loads fresh settings data and renders the body fragment.
// errMsg (user-caused, safe to show) renders as the alert banner.
func renderSettings(w http.ResponseWriter, r *http.Request, d handlers.Deps, userID uuid.UUID, code int, errMsg string) {
	data, err := habit.LoadSettings(r.Context(), d.Queries, userID)
	if err != nil {
		handlers.WriteError(w, r, d.Logger, err, http.StatusInternalServerError)
		return
	}
	data.Err = errMsg
	if code != http.StatusOK {
		w.WriteHeader(code)
	}
	if err := templates.SettingsBody(data).Render(r.Context(), w); err != nil {
		handlers.WriteError(w, r, d.Logger, err, http.StatusInternalServerError)
	}
}

// settingsErr maps service errors: user-caused re-render the body with
// 422 + banner, not-found is 404, everything else 500.
func settingsErr(w http.ResponseWriter, r *http.Request, d handlers.Deps, userID uuid.UUID, err error) {
	switch {
	case errors.Is(err, habit.ErrNotFound):
		handlers.WriteError(w, r, d.Logger, err, http.StatusNotFound)
	case errors.Is(err, habit.ErrValidation),
		errors.Is(err, habit.ErrCapExceeded):
		renderSettings(w, r, d, userID, http.StatusUnprocessableEntity, err.Error())
	default:
		handlers.WriteError(w, r, d.Logger, err, http.StatusInternalServerError)
	}
}

func handleAddHabit(d handlers.Deps) auth.AuthedHandler {
	return func(w http.ResponseWriter, r *http.Request, a auth.AuthData) {
		weightQ, err := habit.ParseStars(r.FormValue("weight"))
		if err != nil {
			renderSettings(w, r, d, a.UserID, http.StatusUnprocessableEntity, err.Error())
			return
		}
		if _, err := habit.AddHabit(r.Context(), d.Queries, a.UserID, r.FormValue("name"), weightQ); err != nil {
			settingsErr(w, r, d, a.UserID, err)
			return
		}
		renderSettings(w, r, d, a.UserID, http.StatusOK, "")
	}
}

func handleEditHabit(d handlers.Deps) auth.AuthedHandler {
	return func(w http.ResponseWriter, r *http.Request, a auth.AuthData) {
		id, err := handlers.ParseID(r)
		if err != nil {
			handlers.WriteError(w, r, d.Logger, err, http.StatusBadRequest)
			return
		}
		weightQ, err := habit.ParseStars(r.FormValue("weight"))
		if err != nil {
			renderSettings(w, r, d, a.UserID, http.StatusUnprocessableEntity, err.Error())
			return
		}
		if _, err := habit.EditHabit(r.Context(), d.Queries, a.UserID, id, r.FormValue("name"), weightQ); err != nil {
			settingsErr(w, r, d, a.UserID, err)
			return
		}
		renderSettings(w, r, d, a.UserID, http.StatusOK, "")
	}
}

func handleDeleteHabit(d handlers.Deps) auth.AuthedHandler {
	return func(w http.ResponseWriter, r *http.Request, a auth.AuthData) {
		id, err := handlers.ParseID(r)
		if err != nil {
			handlers.WriteError(w, r, d.Logger, err, http.StatusBadRequest)
			return
		}
		if err := habit.DeleteHabit(r.Context(), d.Queries, a.UserID, id); err != nil {
			settingsErr(w, r, d, a.UserID, err)
			return
		}
		renderSettings(w, r, d, a.UserID, http.StatusOK, "")
	}
}

// handleDayZero sets the tracking start day. Empty/malformed day is 422,
// future day rejected by the service.
func handleDayZero(d handlers.Deps) auth.AuthedHandler {
	return func(w http.ResponseWriter, r *http.Request, a auth.AuthData) {
		day, err := time.Parse("2006-01-02", r.FormValue("day"))
		if err != nil {
			renderSettings(w, r, d, a.UserID, http.StatusUnprocessableEntity, "day must be YYYY-MM-DD")
			return
		}
		if err := habit.SetDayZero(r.Context(), d.Queries, a.UserID, day); err != nil {
			settingsErr(w, r, d, a.UserID, err)
			return
		}
		// From the day panel (next=day) return the panel; from settings
		// return the settings body. One endpoint, two swap targets.
		if r.FormValue("next") == "day" {
			v, err := habit.LoadDay(r.Context(), d.Queries, a.UserID, day)
			if err != nil {
				handlers.WriteError(w, r, d.Logger, err, http.StatusInternalServerError)
				return
			}
			if err := templates.DayPanel(v).Render(r.Context(), w); err != nil {
				handlers.WriteError(w, r, d.Logger, err, http.StatusInternalServerError)
			}
			return
		}
		renderSettings(w, r, d, a.UserID, http.StatusOK, "")
	}
}

func formID(r *http.Request, key string) (int64, error) {
	id, err := strconv.ParseInt(r.FormValue(key), 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("invalid " + key)
	}
	return id, nil
}
