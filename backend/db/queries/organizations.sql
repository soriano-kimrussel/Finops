-- name: CreateOrganization :one
INSERT INTO organizations (id, name, base_currency, timezone)
VALUES (@id, @name, @base_currency, @timezone)
RETURNING *;

-- name: GetOrganization :one
SELECT * FROM organizations WHERE id = @id;

-- name: UpdateOrganization :one
UPDATE organizations SET name = @name, timezone = @timezone
WHERE id = @id
RETURNING *;

-- name: ListUserOrganizations :many
SELECT o.id, o.name, o.base_currency, o.timezone, m.role
FROM memberships m
JOIN organizations o ON o.id = m.organization_id
WHERE m.user_id = @user_id
ORDER BY o.name;

-- name: GetCurrency :one
SELECT * FROM currencies WHERE code = @code;

-- name: ListCurrencies :many
SELECT * FROM currencies ORDER BY code;

-- name: GetMembershipForUser :one
SELECT * FROM memberships WHERE organization_id = @organization_id AND user_id = @user_id;

-- name: GetMembership :one
SELECT * FROM memberships WHERE id = @id;

-- name: GetMembershipForUpdate :one
SELECT * FROM memberships WHERE id = @id FOR UPDATE;

-- name: CreateMembership :one
INSERT INTO memberships (organization_id, user_id, role)
VALUES (@organization_id, @user_id, @role)
RETURNING *;

-- name: UpdateMembershipRole :one
UPDATE memberships SET role = @role WHERE id = @id RETURNING *;

-- name: DeleteMembership :exec
DELETE FROM memberships WHERE id = @id;

-- name: LockOwnerMemberships :many
SELECT id FROM memberships WHERE organization_id = @organization_id AND role = 'owner' FOR UPDATE;

-- name: ListMembers :many
SELECT m.id, m.user_id, m.role, m.created_at, u.name, u.email
FROM memberships m
JOIN users u ON u.id = m.user_id
WHERE m.organization_id = @organization_id
ORDER BY u.name;

-- name: InsertAuditLog :exec
INSERT INTO audit_logs (organization_id, actor_user_id, actor_type, action, entity_type, entity_id, changes, request_id, ip)
VALUES (@organization_id, @actor_user_id, @actor_type, @action, @entity_type, @entity_id, @changes, @request_id, @ip);

-- name: ListAuditLogs :many
SELECT a.*, u.name AS actor_name, u.email AS actor_email
FROM audit_logs a
LEFT JOIN users u ON u.id = a.actor_user_id
WHERE a.organization_id = @organization_id
  AND (sqlc.narg('entity_type')::text IS NULL OR a.entity_type = sqlc.narg('entity_type'))
  AND (sqlc.narg('entity_id')::uuid IS NULL OR a.entity_id = sqlc.narg('entity_id'))
  AND (sqlc.narg('action')::text IS NULL OR a.action = sqlc.narg('action'))
  AND (sqlc.narg('actor_user_id')::uuid IS NULL OR a.actor_user_id = sqlc.narg('actor_user_id'))
  AND (sqlc.narg('from')::timestamptz IS NULL OR a.created_at >= sqlc.narg('from'))
  AND (sqlc.narg('to')::timestamptz IS NULL OR a.created_at < sqlc.narg('to'))
  AND (sqlc.narg('before_created_at')::timestamptz IS NULL
       OR (a.created_at, a.id) < (sqlc.narg('before_created_at')::timestamptz, sqlc.narg('before_id')::uuid))
ORDER BY a.created_at DESC, a.id DESC
LIMIT @row_limit;
