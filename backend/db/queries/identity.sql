-- name: CreateUser :one
INSERT INTO users (email, name, password_hash)
VALUES (@email, @name, @password_hash)
RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = @email;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = @id;

-- name: CreateSession :one
INSERT INTO sessions (user_id, token_hash, expires_at, ip, user_agent)
VALUES (@user_id, @token_hash, @expires_at, @ip, @user_agent)
RETURNING *;

-- name: GetSessionWithUser :one
SELECT sqlc.embed(s), sqlc.embed(u)
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = @token_hash AND s.expires_at > now() AND u.is_active;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = now(), expires_at = @expires_at WHERE id = @id;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = @token_hash;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at <= now();
