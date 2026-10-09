-- name: CreateUser :one
INSERT INTO users (id, username, password_hash)
VALUES ($1, $2, $3)
RETURNING id, username, password_hash, day_zero, created_at;

-- name: GetUserByUsername :one
SELECT id, username, password_hash, day_zero, created_at
FROM users
WHERE username = $1;

-- name: GetUserById :one
SELECT id, username, password_hash, day_zero, created_at
FROM users
WHERE id = $1;

-- name: SetDayZero :exec
UPDATE users
SET day_zero = $2
WHERE id = $1;

-- Habits (alive only: deleted_at IS NULL).

-- name: ListHabits :many
SELECT id, user_id, name, weight_q, deleted_at, created_at
FROM habits
WHERE user_id = $1 AND deleted_at IS NULL
ORDER BY id;

-- name: HabitTotalQ :one
SELECT COALESCE(SUM(weight_q), 0)::BIGINT
FROM habits
WHERE user_id = $1 AND deleted_at IS NULL;

-- name: CreateHabit :one
INSERT INTO habits (user_id, name, weight_q)
VALUES ($1, $2, $3)
RETURNING id, user_id, name, weight_q, deleted_at, created_at;

-- name: GetHabit :one
SELECT id, user_id, name, weight_q, deleted_at, created_at
FROM habits
WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL;

-- name: UpdateHabit :one
UPDATE habits
SET name = $3, weight_q = $4
WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
RETURNING id, user_id, name, weight_q, deleted_at, created_at;

-- name: SoftDeleteHabit :exec
UPDATE habits
SET deleted_at = now()
WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL;

-- Check-ins.

-- name: ListCheckinsForDay :many
SELECT user_id, habit_id, day, stars_q
FROM checkins
WHERE user_id = $1 AND day = $2;

-- name: GetCheckin :one
SELECT user_id, habit_id, day, stars_q
FROM checkins
WHERE user_id = $1 AND habit_id = $2 AND day = $3;

-- name: CreateCheckin :exec
INSERT INTO checkins (user_id, habit_id, day, stars_q)
VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING;

-- name: DeleteCheckin :exec
DELETE FROM checkins
WHERE user_id = $1 AND habit_id = $2 AND day = $3;
