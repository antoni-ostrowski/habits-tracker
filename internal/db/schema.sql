CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE IF NOT EXISTS users (
	id UUID PRIMARY KEY,
	username TEXT UNIQUE NOT NULL,
	password_hash TEXT NOT NULL,
	day_zero DATE,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sessions (
	token TEXT PRIMARY KEY,
	data BYTEA NOT NULL,
	expiry TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS sessions_expiry_idx ON sessions (expiry);

-- Habits: one list per user, the whole day plan. weight_q is
-- quarter-stars (4 = 1 star). Deleted habits keep their row
-- (deleted_at set) so check-in history still joins to a name; only
-- alive habits show in the day view.
CREATE TABLE IF NOT EXISTS habits (
  id BIGSERIAL PRIMARY KEY,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  weight_q INTEGER NOT NULL DEFAULT 4 CHECK (weight_q BETWEEN 1 AND 20),
  deleted_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS habits_user_id_idx ON habits (user_id);

-- Check-ins: one row per done habit per day. stars_q snapshots the
-- habit's weight at check time, so later weight edits never rewrite
-- history. Unchecking deletes the row.
CREATE TABLE IF NOT EXISTS checkins (
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  habit_id BIGINT NOT NULL REFERENCES habits(id) ON DELETE CASCADE,
  day DATE NOT NULL,
  stars_q INTEGER NOT NULL CHECK (stars_q BETWEEN 1 AND 20),
  PRIMARY KEY (user_id, habit_id, day)
);

CREATE INDEX IF NOT EXISTS checkins_user_day_idx ON checkins (user_id, day);
