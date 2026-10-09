// Package habit is the domain service: pure data in, pure data out, no
// http anywhere. Handlers parse/validate transport, call into here, then
// render templ. A future JSON/TUI renderer plugs in at the handler's
// render step without touching this package.
//
// Weights are integer quarter-stars everywhere (4 = 1 star, 20 = 5):
// no float drift, DB CHECKs mirror the Go consts.
package habit

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	db "github.com/antoni-ostrowski/habit-tracker/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Quarter-star consts. Change here, everything follows. NOT env: these
// are domain rules, and DB CHECK constraints mirror them.
const (
	QuarterMin         = 1  // 0.25 stars
	QuarterMaxPerHabit = 20 // 5 stars
	DayTotalQ          = 20 // a day (and the habit list) caps at 5 stars
)

var (
	ErrValidation  = errors.New("invalid input")
	ErrCapExceeded = errors.New("habit list would exceed 5 stars")
	ErrNotEditable = errors.New("only today is editable")
	ErrNotFound    = errors.New("not found")
)

// ParseStars parses user input like "1", "1.5", "0.25" into quarter-stars.
// Only exact quarters accepted; anything else is an error, never rounded.
func ParseStars(s string) (int32, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("weight is required")
	}
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	whole, frac, _ := strings.Cut(s, ".")
	w, err := strconv.Atoi(whole)
	if err != nil || neg {
		return 0, fmt.Errorf("weight %q is not a valid star value", s)
	}
	var fq int
	switch frac {
	case "", "0", "00":
		fq = 0
	case "25":
		fq = 1
	case "5", "50":
		fq = 2
	case "75":
		fq = 3
	default:
		return 0, fmt.Errorf("weight %q must be in 0.25 steps", s)
	}
	q := int32(w*4 + fq)
	if q < QuarterMin || q > QuarterMaxPerHabit {
		return 0, fmt.Errorf("%w: weight must be between 0.25 and 5 stars", ErrValidation)
	}
	return q, nil
}

// FormatQ renders quarter-stars for humans: 5 -> "1.25".
func FormatQ(q int32) string {
	whole, rem := q/4, q%4
	switch rem {
	case 1:
		return fmt.Sprintf("%d.25", whole)
	case 2:
		return fmt.Sprintf("%d.5", whole)
	case 3:
		return fmt.Sprintf("%d.75", whole)
	default:
		return strconv.FormatInt(int64(whole), 10)
	}
}

// ServerToday is the current day on the server clock, truncated to a DATE.
func ServerToday() time.Time {
	return TruncateDay(time.Now())
}

// TruncateDay drops the clock part; DATE columns carry no timezone.
func TruncateDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// IsToday reports whether day is the server-local today.
func IsToday(day time.Time) bool {
	return TruncateDay(day).Equal(ServerToday())
}

// Weekday returns 0=Sunday..6=Saturday, matching time.Weekday.
func Weekday(day time.Time) int {
	return int(day.Weekday())
}

// WeekStart returns the Monday of day's week.
func WeekStart(day time.Time) time.Time {
	d := TruncateDay(day)
	return d.AddDate(0, 0, -((Weekday(d) + 6) % 7))
}

// pgDate converts to the pgtype.Date sqlc generated for DATE columns.
func pgDate(t time.Time) pgtype.Date {
	return pgtype.Date{Time: TruncateDay(t), Valid: true}
}

// DayNumber counts days since the user's day zero: day zero itself is
// DAY 0, the next day DAY 1. Nil when no day zero set or day precedes it.
func DayNumber(dayZero pgtype.Date, day time.Time) *int {
	if !dayZero.Valid {
		return nil
	}
	n := int(TruncateDay(day).Sub(TruncateDay(dayZero.Time)).Hours() / 24)
	if n < 0 {
		return nil
	}
	return &n
}

// SetDayZero sets the tracking start. Must not be in the future;
// changeable anytime (labels recalc, logged scores untouched).
func SetDayZero(ctx context.Context, q *db.Queries, userID uuid.UUID, day time.Time) error {
	day = TruncateDay(day)
	if day.After(ServerToday()) {
		return fmt.Errorf("%w: day zero cannot be in the future", ErrValidation)
	}
	return q.SetDayZero(ctx, db.SetDayZeroParams{ID: userID, DayZero: pgDate(day)})
}

// Habits.

func ListHabits(ctx context.Context, q *db.Queries, userID uuid.UUID) ([]db.Habit, error) {
	return q.ListHabits(ctx, userID)
}

func AddHabit(ctx context.Context, q *db.Queries, userID uuid.UUID, name string, weightQ int32) (db.Habit, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return db.Habit{}, fmt.Errorf("%w: habit name cannot be empty", ErrValidation)
	}
	if weightQ < QuarterMin || weightQ > QuarterMaxPerHabit {
		return db.Habit{}, fmt.Errorf("%w: weight must be between 0.25 and 5 stars", ErrValidation)
	}
	total, err := q.HabitTotalQ(ctx, userID)
	if err != nil {
		return db.Habit{}, err
	}
	if total+int64(weightQ) > DayTotalQ {
		return db.Habit{}, fmt.Errorf("%w: list at %s, adding %s overflows",
			ErrCapExceeded, FormatQ(int32(total)), FormatQ(weightQ))
	}
	return q.CreateHabit(ctx, db.CreateHabitParams{UserID: userID, Name: name, WeightQ: weightQ})
}

func EditHabit(ctx context.Context, q *db.Queries, userID uuid.UUID, id int64, name string, weightQ int32) (db.Habit, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return db.Habit{}, fmt.Errorf("%w: habit name cannot be empty", ErrValidation)
	}
	if weightQ < QuarterMin || weightQ > QuarterMaxPerHabit {
		return db.Habit{}, fmt.Errorf("%w: weight must be between 0.25 and 5 stars", ErrValidation)
	}
	old, err := q.GetHabit(ctx, db.GetHabitParams{ID: id, UserID: userID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Habit{}, ErrNotFound
		}
		return db.Habit{}, err
	}
	total, err := q.HabitTotalQ(ctx, userID)
	if err != nil {
		return db.Habit{}, err
	}
	if total-int64(old.WeightQ)+int64(weightQ) > DayTotalQ {
		return db.Habit{}, fmt.Errorf("%w: list at %s, change overflows",
			ErrCapExceeded, FormatQ(int32(total)))
	}
	h, err := q.UpdateHabit(ctx, db.UpdateHabitParams{ID: id, UserID: userID, Name: name, WeightQ: weightQ})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Habit{}, ErrNotFound
		}
		return db.Habit{}, err
	}
	return h, nil
}

// DeleteHabit soft-deletes: the row (and its check-ins) stay for history.
func DeleteHabit(ctx context.Context, q *db.Queries, userID uuid.UUID, id int64) error {
	if _, err := q.GetHabit(ctx, db.GetHabitParams{ID: id, UserID: userID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return q.SoftDeleteHabit(ctx, db.SoftDeleteHabitParams{ID: id, UserID: userID})
}

// SettingsData is everything the settings page renders. Handlers set Err
// on validation failure and re-render; it never persists.
type SettingsData struct {
	Habits      []db.Habit
	HabitTotalQ int32
	DayZero     pgtype.Date
	Err         string
}

func LoadSettings(ctx context.Context, q *db.Queries, userID uuid.UUID) (SettingsData, error) {
	var d SettingsData
	var err error
	if d.Habits, err = q.ListHabits(ctx, userID); err != nil {
		return SettingsData{}, err
	}
	total, err := q.HabitTotalQ(ctx, userID)
	if err != nil {
		return SettingsData{}, err
	}
	d.HabitTotalQ = int32(total)
	user, err := q.GetUserById(ctx, userID)
	if err != nil {
		return SettingsData{}, err
	}
	d.DayZero = user.DayZero
	return d, nil
}

// Day view: the user's alive habits + check state for the day.

type DayHabit struct {
	Habit   db.Habit
	Checked bool
	StarsQ  int32 // snapshot when checked, current weight otherwise
}

type DayView struct {
	Day       time.Time
	Editable  bool // day == server today; past/future read-only
	DayNumber *int // days since day zero; nil when unset/pre-zero
	Habits    []DayHabit
	TotalQ    int32
}

func LoadDay(ctx context.Context, q *db.Queries, userID uuid.UUID, day time.Time) (DayView, error) {
	day = TruncateDay(day)
	v := DayView{Day: day, Editable: IsToday(day)}
	user, err := q.GetUserById(ctx, userID)
	if err != nil {
		return DayView{}, err
	}
	v.DayNumber = DayNumber(user.DayZero, day)
	checks, err := q.ListCheckinsForDay(ctx, db.ListCheckinsForDayParams{
		UserID: userID, Day: pgDate(day),
	})
	if err != nil {
		return DayView{}, err
	}
	byHabit := make(map[int64]int32, len(checks))
	for _, c := range checks {
		byHabit[c.HabitID] = c.StarsQ
	}
	habits, err := q.ListHabits(ctx, userID)
	if err != nil {
		return DayView{}, err
	}
	for _, h := range habits {
		stars, checked := h.WeightQ, false
		if s, ok := byHabit[h.ID]; ok {
			stars, checked = s, true
		}
		if checked {
			v.TotalQ += stars
		}
		v.Habits = append(v.Habits, DayHabit{Habit: h, Checked: checked, StarsQ: stars})
	}
	return v, nil
}

// ToggleCheckin checks (snapshotting the current weight) or unchecks.
// Today only: past is record, future is preview.
func ToggleCheckin(ctx context.Context, q *db.Queries, userID uuid.UUID, habitID int64, day time.Time) (checked bool, err error) {
	day = TruncateDay(day)
	if !IsToday(day) {
		return false, ErrNotEditable
	}
	h, err := q.GetHabit(ctx, db.GetHabitParams{ID: habitID, UserID: userID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrNotFound
		}
		return false, err
	}
	if _, err := q.GetCheckin(ctx, db.GetCheckinParams{
		UserID: userID, HabitID: habitID, Day: pgDate(day),
	}); err == nil {
		if err := q.DeleteCheckin(ctx, db.DeleteCheckinParams{
			UserID: userID, HabitID: habitID, Day: pgDate(day),
		}); err != nil {
			return false, err
		}
		return false, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if err := q.CreateCheckin(ctx, db.CreateCheckinParams{
		UserID: userID, HabitID: habitID, Day: pgDate(day), StarsQ: h.WeightQ,
	}); err != nil {
		return false, err
	}
	return true, nil
}
