# Habit tracker

> vibed, personal tool only

Personal day habit tracker. Based our idea of star rating each day, by completing habits.

## Concept

- One habit list per user. Each habit is worth stars (0.25–5, quarter steps).
- List total caps at 5 stars. Every day caps at 5 stars, like movies.
- Day zero: pick the date counting starts from. Days show DAY 0, DAY 1, …
- Check habits off per day, today only. Past is record, future is preview.
- Check-ins snapshot the weight: editing a habit never rewrites history.

## Tech

`net/http`, `templ`, `htmx` + `alpine`, `sqlc`, PostgreSQL, `scs` sessions, OpenTelemetry. 


## Run

Needs PostgreSQL. Default: `postgres://postgres:postgres@localhost:5432/habits?sslmode=disable`

```bash
mise run generate      # sqlc + templ codegen
mise run db-apply      # apply schema (sqldef, no migration files)
mise run dev           # hot reload (starts dev db, applies schema)
```

Based from my [template](https://github.com/antoni-ostrowski/go-web-ref)
