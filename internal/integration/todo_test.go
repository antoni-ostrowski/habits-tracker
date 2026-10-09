package integration_test

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	db "github.com/antoni-ostrowski/habit-tracker/internal/db/sqlc"

	"github.com/google/uuid"
)

// seedSecondUser inserts a second user for isolation tests.
func seedSecondUser(t *testing.T) uuid.UUID {
	t.Helper()
	return seedUser(t, "")
}

func mkTodo(user uuid.UUID, title string) db.CreateTodoParams {
	return db.CreateTodoParams{UserID: user, Title: title}
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }

// GET / shows only the acting user's todos.
func TestPage_Isolation(t *testing.T) {
	app, q, _, user := setup(t)
	cookie := login(t, app, q, user)

	other := seedSecondUser(t)
	if _, err := q.CreateTodo(context.Background(), mkTodo(other, "their-todo-xyz")); err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateTodo(context.Background(), mkTodo(user, "my-todo-xyz")); err != nil {
		t.Fatal(err)
	}

	rec := Do(t, app, http.MethodGet, "/", nil, cookie, nil)
	WantCode(t, rec, http.StatusOK)
	WantBody(t, rec, "my-todo-xyz")
	WantNoBody(t, rec, "their-todo-xyz")
}

// POST /todos stores a trimmed row and renders the fragment with it.
func TestAdd_CreatesTodo(t *testing.T) {
	app, q, _, user := setup(t)
	cookie := login(t, app, q, user)

	rec := Do(t, app, http.MethodPost, "/todos", url.Values{"title": {"  buy milk  "}}, cookie, nil)
	WantCode(t, rec, http.StatusOK)
	WantBody(t, rec, "buy milk", `id="todo-list"`)
	WantNoBody(t, rec, "<!doctype html>") // fragment, not the full page

	todos := listDB(t, q, user)
	if len(todos) != 1 {
		t.Fatalf("db has %d todos, want 1: %#v", len(todos), todos)
	}
	if todos[0].Title != "buy milk" {
		t.Errorf("db title = %q, want trimmed %q", todos[0].Title, "buy milk")
	}
	if todos[0].Done {
		t.Error("db done = true, want false")
	}
	if todos[0].UserID != user {
		t.Errorf("db user = %v, want %v", todos[0].UserID, user)
	}
}

// POST /todos with a blank title is 422 and stores nothing.
func TestAdd_RejectsBlankTitle(t *testing.T) {
	app, q, _, user := setup(t)
	cookie := login(t, app, q, user)

	for _, title := range []string{"", "   ", "\t\n "} {
		rec := Do(t, app, http.MethodPost, "/todos", url.Values{"title": {title}}, cookie, nil)
		WantCode(t, rec, http.StatusUnprocessableEntity)
	}
	if todos := listDB(t, q, user); len(todos) != 0 {
		t.Fatalf("db = %#v, want empty after rejected adds", todos)
	}
}

// POST /todos/{id}/toggle flips done in the DB and renders the new state.
func TestToggle_FlipsDone(t *testing.T) {
	app, q, _, user := setup(t)
	cookie := login(t, app, q, user)

	Do(t, app, http.MethodPost, "/todos", url.Values{"title": {"alpha"}}, cookie, nil)
	Do(t, app, http.MethodPost, "/todos", url.Values{"title": {"bravo"}}, cookie, nil)

	rec := Do(t, app, http.MethodPost, "/todos/1/toggle", nil, cookie, nil)
	WantCode(t, rec, http.StatusOK)
	WantBody(t, rec, `line-through`, "alpha", "bravo")

	todos := listDB(t, q, user)
	if len(todos) != 2 || !todos[0].Done || todos[1].Done {
		t.Fatalf("after toggle db = %#v", todos)
	}

	rec = Do(t, app, http.MethodPost, "/todos/1/toggle", nil, cookie, nil)
	WantCode(t, rec, http.StatusOK)
	if todos := listDB(t, q, user); todos[0].Done {
		t.Fatalf("after second toggle db = %#v, want not done", todos)
	}
}

// Missing id is 404, malformed id is 400, another user's row is 404 with
// both users' rows untouched.
func TestToggle_Errors(t *testing.T) {
	app, q, _, user := setup(t)
	cookie := login(t, app, q, user)

	Do(t, app, http.MethodPost, "/todos", url.Values{"title": {"a"}}, cookie, nil)

	rec := Do(t, app, http.MethodPost, "/todos/999999/toggle", nil, cookie, nil)
	WantCode(t, rec, http.StatusNotFound)

	rec = Do(t, app, http.MethodPost, "/todos/abc/toggle", nil, cookie, nil)
	WantCode(t, rec, http.StatusBadRequest)

	other := seedSecondUser(t)
	otherTodo, err := q.CreateTodo(context.Background(), mkTodo(other, "not mine"))
	if err != nil {
		t.Fatal(err)
	}
	rec = Do(t, app, http.MethodPost, "/todos/"+itoa(otherTodo.ID)+"/toggle", nil, cookie, nil)
	WantCode(t, rec, http.StatusNotFound)

	mine := listDB(t, q, user)
	if len(mine) != 1 || mine[0].Done {
		t.Fatalf("my db = %#v, want one open todo", mine)
	}
	theirs := listDB(t, q, other)
	if len(theirs) != 1 || theirs[0].Done {
		t.Fatalf("their db = %#v, want one open todo", theirs)
	}
}

// DELETE /todos/{id} removes only the targeted row.
func TestDelete_RemovesTargetedTodo(t *testing.T) {
	app, q, _, user := setup(t)
	cookie := login(t, app, q, user)

	Do(t, app, http.MethodPost, "/todos", url.Values{"title": {"alpha"}}, cookie, nil)
	Do(t, app, http.MethodPost, "/todos", url.Values{"title": {"bravo"}}, cookie, nil)

	rec := Do(t, app, http.MethodDelete, "/todos/1", nil, cookie, nil)
	WantCode(t, rec, http.StatusOK)
	WantBody(t, rec, "bravo")
	WantNoBody(t, rec, "alpha")

	todos := listDB(t, q, user)
	if len(todos) != 1 || todos[0].Title != "bravo" {
		t.Fatalf("db = %#v, want only bravo", todos)
	}
}

// Anonymous htmx requests get 401 + HX-Redirect so the client navigates.
func TestRequireAuth_HtmxRedirect(t *testing.T) {
	app, _, _, _ := setup(t)

	rec := DoHtmx(t, app, http.MethodPost, "/todos", url.Values{"title": {"x"}}, nil)
	WantCode(t, rec, http.StatusUnauthorized)
	if h := rec.Header().Get("HX-Redirect"); h != "/signin" {
		t.Fatalf("HX-Redirect = %q, want /signin", h)
	}
}

// Anonymous requests see sign-in links; mutations redirect to sign-in.
func TestAnonymous(t *testing.T) {
	app, q, _, _ := setup(t)

	rec := Do(t, app, http.MethodGet, "/", nil, nil, nil)
	WantCode(t, rec, http.StatusOK)
	WantBody(t, rec, "no todos yet", "/signin")
	WantNoBody(t, rec, `hx-post="/todos"`)

	rec = Do(t, app, http.MethodPost, "/todos", url.Values{"title": {"x"}}, nil, nil)
	WantCode(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/signin" {
		t.Fatalf("location = %q, want /signin", loc)
	}

	if todos := listDB(t, q, uuid.Nil); len(todos) != 0 {
		t.Fatalf("db = %#v, want empty", todos)
	}
}
