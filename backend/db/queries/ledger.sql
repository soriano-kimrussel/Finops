-- name: CreateAccount :exec
INSERT INTO accounts (organization_id, code, name, type, is_system)
VALUES (@organization_id, @code, @name, @type, true)
ON CONFLICT (organization_id, code) DO NOTHING;

-- name: GetAccountByCode :one
SELECT * FROM accounts WHERE organization_id = @organization_id AND code = @code;

-- name: InsertTransaction :exec
INSERT INTO transactions (organization_id, account_id, occurred_on, direction, amount, currency, description, source_type, source_id)
VALUES (@organization_id, @account_id, @occurred_on, @direction, @amount, @currency, @description, @source_type, @source_id);

-- name: ListRecentTransactions :many
SELECT t.*, a.name AS account_name
FROM transactions t JOIN accounts a ON a.id = t.account_id
WHERE (sqlc.narg('from')::date IS NULL OR t.occurred_on >= sqlc.narg('from'))
  AND (sqlc.narg('to')::date IS NULL OR t.occurred_on <= sqlc.narg('to'))
ORDER BY t.occurred_on DESC, t.created_at DESC
LIMIT @row_limit;
