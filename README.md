# Habit tracker

Personal day habit tracker. `net/http`, `templ`, `htmx` + `alpine`, `sqlc`, PostgreSQL, `scs` sessions, OpenTelemetry. Brutalist dark UI, no rounded corners.

## Concept

- One habit list per user. Each habit is worth stars (0.25–5, quarter steps).
- List total caps at 5 stars. Every day caps at 5 stars, like movies.
- Day zero: pick the date counting starts from. Days show DAY 0, DAY 1, …
- Check habits off per day, today only. Past is record, future is preview.
- Check-ins snapshot the weight: editing a habit never rewrites history.

## Run

Needs PostgreSQL. Default: `postgres://postgres:postgres@localhost:5432/habits?sslmode=disable`

```bash
mise run generate      # sqlc + templ codegen
mise run db-apply      # apply schema (sqldef, no migration files)
mise run dev           # hot reload (starts dev db, applies schema)
```

`DATABASE_URL` overrides the default.

## Routes

```text
GET /                     day view (?date=YYYY-MM-DD, default today)
POST /checkins/toggle     check/uncheck (today only)
GET /settings             habits + plans + week mapping
POST /habits              add habit (name, weight)
POST /habits/{id}         edit habit
DELETE /habits/{id}       soft delete (history stays)
POST /day-zero            set tracking start (day=YYYY-MM-DD, not future)
GET+POST /signup|/signin  auth forms
POST /signout             log out
GET /healthz              probe (DB ping, no auth)
```

Mutations require login. Anonymous htmx requests get an `HX-Redirect` to sign-in.

## Architecture

```text
main.go      env + signals, then server.Run
server       wiring: mux + middleware + http.Server
habit        domain service: pure data, no http (internal/habit)
handlers/…   one package per domain, thin: parse → service → templ
sqlc         generated DB code from SQL
```

Each handler loads data from the domain service and renders templ directly.
A future TUI/JSON client branches on `Accept: application/json` at the
render step; the service stays untouched.

## Auth

Username + password with bcrypt. Sessions in Postgres via `scs`. Sign-up and sign-in log the user in, sign-out destroys the session.

## Database

`internal/db/schema.sql` is the source of truth for both sqldef and sqlc. Edit it, then `mise run generate` → `db-plan` → `db-apply`.

Weights are integer quarter-stars (`weight_q`, `stars_q`); Go consts
(`QuarterMin`, `QuarterMaxPerHabit`, `DayTotalQ`) in `internal/habit` are
the single source, DB CHECKs mirror them.

## Styling

Tailwind via standalone CLI, no Node. `mise run dev` rebuilds CSS on change, `mise run build` minifies. htmx + Alpine.js vendored under `static/js`.

## Testing

Integration only: HTTP requests against real PostgreSQL, asserting response + DB state.

```bash
mise run test-int   # disposable Postgres, full suite
```

## Layout

```text
main.go
internal/server/         Run + NewServer + NewHandler
internal/habit/          domain service (consts, day view, plans, check-ins)
internal/db/             schema.sql, queries.sql, sqlc/
internal/handlers/       handlers.go + auth/ + habits/ + static/
internal/obs/            OTel setup + log handler
internal/integration/    HTTP + DB tests
static/                  css input, vendored htmx + alpine
templates/               layout, day, settings, auth
```

New domain = new subpackage with `Register(mux, Deps)`, called from `server.NewHandler`.

## Tasks

```bash
mise run generate | css | dev | build
mise run db | db-plan | db-apply
mise run test | test-race | test-int | check
```
