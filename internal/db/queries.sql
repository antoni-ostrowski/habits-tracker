-- name: CreateUser :one
INSERT INTO users (id, username, password_hash)
VALUES ($1, $2, $3)
RETURNING id, username, password_hash, created_at;

-- name: GetUserByUsername :one
SELECT id, username, password_hash, created_at
FROM users
WHERE username = $1;

-- name: GetUserById :one
SELECT id, username, password_hash, created_at
FROM users
WHERE id = $1;

-- name: ListTodos :many
SELECT id, user_id, title, done
FROM todos
WHERE user_id = $1
ORDER BY id;

-- name: CreateTodo :one
INSERT INTO todos (user_id, title)
VALUES ($1, $2)
RETURNING id, user_id, title, done;

-- name: GetTodo :one
SELECT id, user_id, title, done
FROM todos
WHERE id = $1 AND user_id = $2;

-- name: UpdateTodo :exec
UPDATE todos
SET title = $3, done = $4
WHERE id = $1 AND user_id = $2;

-- name: DeleteTodo :exec
DELETE FROM todos
WHERE id = $1 AND user_id = $2;
