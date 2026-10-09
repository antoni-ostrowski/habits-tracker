package integration_test

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	db "github.com/antoni-ostrowski/habit-tracker/internal/db/sqlc"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func todayISO() string { return time.Now().Format("2006-01-02") }

func pgDay(t *testing.T, iso string) pgtype.Date {
	t.Helper()
	day, err := time.Parse("2006-01-02", iso)
	if err != nil {
		t.Fatalf("parse day: %v", err)
	}
	return pgtype.Date{Time: day, Valid: true}
}

// addHabit posts the settings add-habit form.
func addHabit(t *testing.T, app http.Handler, cookie *http.Cookie, name, weight string) {
	t.Helper()
	rec := Do(t, app, http.MethodPost, "/habits",
		url.Values{"name": {name}, "weight": {weight}}, cookie, nil)
	WantCode(t, rec, http.StatusOK)
	WantBody(t, rec, name)
}

func habitID(t *testing.T, q *db.Queries, user uuid.UUID, name string) int64 {
	t.Helper()
	habits, err := q.ListHabits(context.Background(), user)
	if err != nil {
		t.Fatalf("ListHabits: %v", err)
	}
	for _, h := range habits {
		if h.Name == name {
			return h.ID
		}
	}
	t.Fatalf("habit %q not found in %#v", name, habits)
	return 0
}

func toggle(t *testing.T, app http.Handler, cookie *http.Cookie, habitID int64, day string) {
	t.Helper()
	rec := Do(t, app, http.MethodPost, "/checkins/toggle",
		url.Values{"habit_id": {strconv.FormatInt(habitID, 10)}, "day": {day}}, cookie, nil)
	WantCode(t, rec, http.StatusOK)
}

// GET / shows only the acting user's habits.
func TestDay_Isolation(t *testing.T) {
	app, q, _, user := setup(t)
	cookie := login(t, app, q, user)

	other := seedUser(t, "")
	addHabit(t, app, login(t, app, q, other), "theirs-xyz", "1")
	addHabit(t, app, cookie, "mine-xyz", "1")

	rec := Do(t, app, http.MethodGet, "/", nil, cookie, nil)
	WantCode(t, rec, http.StatusOK)
	WantBody(t, rec, "mine-xyz")
	WantNoBody(t, rec, "theirs-xyz")
}

// The habit list caps at 5 stars total; overflow is 422, stores nothing.
func TestAddHabit_CapEnforced(t *testing.T) {
	app, q, _, user := setup(t)
	cookie := login(t, app, q, user)

	addHabit(t, app, cookie, "gym", "4")
	addHabit(t, app, cookie, "read", "1")

	rec := Do(t, app, http.MethodPost, "/habits",
		url.Values{"name": {"tiny"}, "weight": {"0.25"}}, cookie, nil)
	WantCode(t, rec, http.StatusUnprocessableEntity)
	WantBody(t, rec, "exceed")

	habits, err := q.ListHabits(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	if len(habits) != 2 {
		t.Fatalf("db = %#v, want 2 habits after rejected add", habits)
	}
}

// Blank names and non-quarter weights are 422 and store nothing.
func TestAddHabit_Validation(t *testing.T) {
	app, q, _, user := setup(t)
	cookie := login(t, app, q, user)

	for _, tt := range []struct{ name, weight string }{
		{"", "1"},
		{"   ", "1"},
		{"x", ""},
		{"x", "abc"},
		{"x", "1.1"},
		{"x", "0"},
		{"x", "5.25"},
		{"x", "-1"},
	} {
		rec := Do(t, app, http.MethodPost, "/habits",
			url.Values{"name": {tt.name}, "weight": {tt.weight}}, cookie, nil)
		WantCode(t, rec, http.StatusUnprocessableEntity)
	}
	if habits, err := q.ListHabits(context.Background(), user); err != nil || len(habits) != 0 {
		t.Fatalf("db = %#v, err = %v, want empty", habits, err)
	}
}

// Toggle checks (snapshotting weight) and unchecks, rendering the panel.
func TestToggle_CheckUncheck(t *testing.T) {
	app, q, _, user := setup(t)
	cookie := login(t, app, q, user)

	addHabit(t, app, cookie, "run", "1.5")
	id := habitID(t, q, user, "run")

	rec := Do(t, app, http.MethodPost, "/checkins/toggle",
		url.Values{"habit_id": {strconv.FormatInt(id, 10)}, "day": {todayISO()}}, cookie, nil)
	WantCode(t, rec, http.StatusOK)
	WantBody(t, rec, "run", "1.5", `id="day-panel"`)
	WantNoBody(t, rec, "<!doctype html>")

	row, err := q.GetCheckin(context.Background(), db.GetCheckinParams{
		UserID: user, HabitID: id, Day: pgDay(t, todayISO()),
	})
	if err != nil {
		t.Fatalf("GetCheckin: %v", err)
	}
	if row.StarsQ != 6 {
		t.Fatalf("stars = %d, want 6 (snapshot of 1.5)", row.StarsQ)
	}

	toggle(t, app, cookie, id, todayISO())
	if _, err := q.GetCheckin(context.Background(), db.GetCheckinParams{
		UserID: user, HabitID: id, Day: pgDay(t, todayISO()),
	}); err == nil {
		t.Fatal("checkin row still present after uncheck")
	}
}

// Past and future days are read-only: 403, nothing stored.
func TestToggle_OnlyToday(t *testing.T) {
	app, q, _, user := setup(t)
	cookie := login(t, app, q, user)

	addHabit(t, app, cookie, "run", "1")
	id := habitID(t, q, user, "run")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")

	for _, day := range []string{yesterday, tomorrow} {
		rec := Do(t, app, http.MethodPost, "/checkins/toggle",
			url.Values{"habit_id": {strconv.FormatInt(id, 10)}, "day": {day}}, cookie, nil)
		WantCode(t, rec, http.StatusForbidden)
	}

	var n int
	if err := testPool(t).QueryRow(context.Background(),
		`SELECT count(*) FROM checkins WHERE user_id = $1`, user).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("checkins = %d, want 0 after forbidden toggles", n)
	}
}

// Editing a habit's weight never rewrites logged scores.
func TestWeightEdit_FreezesHistory(t *testing.T) {
	app, q, _, user := setup(t)
	cookie := login(t, app, q, user)

	addHabit(t, app, cookie, "run", "1")
	id := habitID(t, q, user, "run")
	toggle(t, app, cookie, id, todayISO())

	rec := Do(t, app, http.MethodPost, "/habits/"+strconv.FormatInt(id, 10),
		url.Values{"name": {"run"}, "weight": {"2"}}, cookie, nil)
	WantCode(t, rec, http.StatusOK)

	row, err := q.GetCheckin(context.Background(), db.GetCheckinParams{
		UserID: user, HabitID: id, Day: pgDay(t, todayISO()),
	})
	if err != nil {
		t.Fatalf("GetCheckin: %v", err)
	}
	if row.StarsQ != 4 {
		t.Fatalf("stars = %d, want frozen 4 after weight edit", row.StarsQ)
	}

	rec = Do(t, app, http.MethodGet, "/", nil, cookie, nil)
	WantCode(t, rec, http.StatusOK)
	WantBody(t, rec, "1 / 5")

	// Recheck after uncheck snapshots the new weight.
	toggle(t, app, cookie, id, todayISO())
	toggle(t, app, cookie, id, todayISO())
	row, err = q.GetCheckin(context.Background(), db.GetCheckinParams{
		UserID: user, HabitID: id, Day: pgDay(t, todayISO()),
	})
	if err != nil {
		t.Fatalf("GetCheckin: %v", err)
	}
	if row.StarsQ != 8 {
		t.Fatalf("stars = %d, want 8 after recheck at new weight", row.StarsQ)
	}
}

// Setting day zero from the day panel returns the panel showing DAY 0.
func TestDayZero_SetFromPanel(t *testing.T) {
	app, q, _, user := setup(t)
	cookie := login(t, app, q, user)

	rec := Do(t, app, http.MethodGet, "/", nil, cookie, nil)
	WantCode(t, rec, http.StatusOK)
	WantNoBody(t, rec, ">day 0<")

	rec = Do(t, app, http.MethodPost, "/day-zero",
		url.Values{"day": {todayISO()}, "next": {"day"}}, cookie, nil)
	WantCode(t, rec, http.StatusOK)
	WantBody(t, rec, ">day 0<", `id="day-panel"`)

	rec = Do(t, app, http.MethodGet, "/", nil, cookie, nil)
	WantCode(t, rec, http.StatusOK)
	WantBody(t, rec, ">day 0<")
}

// The counter increases on later days.
func TestDayZero_CountsUp(t *testing.T) {
	app, q, _, user := setup(t)
	cookie := login(t, app, q, user)

	threeAgo := time.Now().AddDate(0, 0, -3).Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	rec := Do(t, app, http.MethodPost, "/day-zero",
		url.Values{"day": {threeAgo}}, cookie, nil)
	WantCode(t, rec, http.StatusOK)
	WantBody(t, rec, "day 0 =")

	rec = Do(t, app, http.MethodGet, "/", nil, cookie, nil)
	WantCode(t, rec, http.StatusOK)
	WantBody(t, rec, ">day 3<")

	rec = Do(t, app, http.MethodGet, "/?date="+yesterday, nil, cookie, nil)
	WantCode(t, rec, http.StatusOK)
	WantBody(t, rec, ">day 2<")
}

// Future day zero is 422 and stores nothing.
func TestDayZero_FutureRejected(t *testing.T) {
	app, q, _, user := setup(t)
	cookie := login(t, app, q, user)

	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	rec := Do(t, app, http.MethodPost, "/day-zero",
		url.Values{"day": {tomorrow}}, cookie, nil)
	WantCode(t, rec, http.StatusUnprocessableEntity)
	WantBody(t, rec, "future")

	rec = Do(t, app, http.MethodGet, "/", nil, cookie, nil)
	WantCode(t, rec, http.StatusOK)
	WantNoBody(t, rec, ">day 0<")
}

// Days before day zero show no counter.
func TestDayZero_HidesBeforeZero(t *testing.T) {
	app, q, _, user := setup(t)
	cookie := login(t, app, q, user)

	Do(t, app, http.MethodPost, "/day-zero",
		url.Values{"day": {todayISO()}}, cookie, nil)
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	rec := Do(t, app, http.MethodGet, "/?date="+yesterday, nil, cookie, nil)
	WantCode(t, rec, http.StatusOK)
	WantNoBody(t, rec, ">day ")
}

// Deleting a habit keeps its check-in rows; the day panel drops it.
func TestDeleteHabit_KeepsHistory(t *testing.T) {
	app, q, _, user := setup(t)
	cookie := login(t, app, q, user)

	addHabit(t, app, cookie, "run", "1")
	id := habitID(t, q, user, "run")
	toggle(t, app, cookie, id, todayISO())

	rec := Do(t, app, http.MethodDelete, "/habits/"+strconv.FormatInt(id, 10), nil, cookie, nil)
	WantCode(t, rec, http.StatusOK)
	WantNoBody(t, rec, "run")

	var n int
	if err := testPool(t).QueryRow(context.Background(),
		`SELECT count(*) FROM checkins WHERE user_id = $1`, user).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("checkins = %d, want history row preserved", n)
	}

	rec = Do(t, app, http.MethodGet, "/", nil, cookie, nil)
	WantCode(t, rec, http.StatusOK)
	WantNoBody(t, rec, "run")
}

// Toggling another user's habit is 404, nothing stored.
func TestToggle_ForeignHabit(t *testing.T) {
	app, q, _, user := setup(t)
	cookie := login(t, app, q, user)

	other := seedUser(t, "")
	addHabit(t, app, login(t, app, q, other), "theirs", "1")
	theirs := habitID(t, q, other, "theirs")

	rec := Do(t, app, http.MethodPost, "/checkins/toggle",
		url.Values{"habit_id": {strconv.FormatInt(theirs, 10)}, "day": {todayISO()}}, cookie, nil)
	WantCode(t, rec, http.StatusNotFound)
}

// Settings needs auth; anonymous htmx gets 401 + HX-Redirect.
func TestSettings_RequiresAuth(t *testing.T) {
	app, _, _, _ := setup(t)

	rec := Do(t, app, http.MethodGet, "/settings", nil, nil, nil)
	WantCode(t, rec, http.StatusSeeOther)

	rec = DoHtmx(t, app, http.MethodPost, "/habits",
		url.Values{"name": {"x"}, "weight": {"1"}}, nil)
	WantCode(t, rec, http.StatusUnauthorized)
	if h := rec.Header().Get("HX-Redirect"); h != "/signin" {
		t.Fatalf("HX-Redirect = %q, want /signin", h)
	}
}

// Settings page renders habits + tracking start, no plans.
func TestSettings_Page(t *testing.T) {
	app, q, _, user := setup(t)
	cookie := login(t, app, q, user)

	rec := Do(t, app, http.MethodGet, "/settings", nil, cookie, nil)
	WantCode(t, rec, http.StatusOK)
	WantBody(t, rec, "settings", "habits", "tracking start")
	WantNoBody(t, rec, "PLANS", "WEEK PLAN")
}

// Bad ?date= is 400.
func TestDay_BadDate(t *testing.T) {
	app, q, _, user := setup(t)
	cookie := login(t, app, q, user)

	rec := Do(t, app, http.MethodGet, "/?date=nope", nil, cookie, nil)
	WantCode(t, rec, http.StatusBadRequest)
}
