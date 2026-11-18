-- name: CreateExpense :one
INSERT INTO expenses (organization_id, category_id, project_id, vendor_id, expense_date, amount, currency,
                      description, submitted_by)
VALUES (@organization_id, @category_id, @project_id, @vendor_id, @expense_date, @amount, @currency,
        @description, @submitted_by)
RETURNING *;

-- name: UpdateExpense :one
UPDATE expenses
SET category_id = @category_id, project_id = @project_id, vendor_id = @vendor_id, expense_date = @expense_date,
    amount = @amount, currency = @currency, description = @description, version = version + 1
WHERE id = @id
RETURNING *;

-- name: GetExpenseForUpdate :one
SELECT * FROM expenses WHERE id = @id FOR UPDATE;

-- name: GetExpense :one
SELECT e.*, v.name AS vendor_name, ec.name AS category_name, p.name AS project_name, su.name AS submitted_by_name, ru.name AS reviewed_by_name
FROM expenses e
JOIN vendors v ON v.id = e.vendor_id
JOIN expense_categories ec ON ec.id = e.category_id
LEFT JOIN projects p ON p.id = e.project_id
LEFT JOIN users su ON su.id = e.submitted_by
LEFT JOIN users ru ON ru.id = e.reviewed_by
WHERE e.id = @id;

-- name: ReviewExpense :one
UPDATE expenses
SET approval_status = @approval_status, reviewed_by = @reviewed_by, reviewed_at = now(), review_note = @review_note,
    version = version + 1
WHERE id = @id
RETURNING *;

-- name: DeleteExpense :exec
DELETE FROM expenses WHERE id = @id;

-- name: ListExpenses :many
SELECT e.id, v.name AS vendor_name, e.expense_date, e.amount, e.currency, e.description, e.approval_status,
       e.category_id, ec.name AS category_name, e.project_id, p.name AS project_name,
       e.submitted_by, su.name AS submitted_by_name, e.created_at
FROM expenses e
JOIN expense_categories ec ON ec.id = e.category_id
JOIN vendors v ON v.id = e.vendor_id
LEFT JOIN projects p ON p.id = e.project_id
LEFT JOIN users su ON su.id = e.submitted_by
WHERE (sqlc.narg('pattern')::text IS NULL OR v.name ILIKE sqlc.narg('pattern') OR e.description ILIKE sqlc.narg('pattern'))
  AND (sqlc.narg('status')::text IS NULL OR e.approval_status = sqlc.narg('status'))
  AND (sqlc.narg('category_id')::uuid IS NULL OR e.category_id = sqlc.narg('category_id'))
  AND (sqlc.narg('project_id')::uuid IS NULL OR e.project_id = sqlc.narg('project_id'))
  AND (sqlc.narg('from')::date IS NULL OR e.expense_date >= sqlc.narg('from'))
  AND (sqlc.narg('to')::date IS NULL OR e.expense_date <= sqlc.narg('to'))
ORDER BY e.expense_date DESC, e.created_at DESC
LIMIT @row_limit OFFSET @row_offset;

-- name: CountExpenses :one
SELECT count(*) AS total, coalesce(sum(e.amount) FILTER (WHERE e.currency = @currency), 0)::numeric AS amount_sum
FROM expenses e
JOIN vendors v ON v.id = e.vendor_id
WHERE (sqlc.narg('pattern')::text IS NULL OR v.name ILIKE sqlc.narg('pattern') OR e.description ILIKE sqlc.narg('pattern'))
  AND (sqlc.narg('status')::text IS NULL OR e.approval_status = sqlc.narg('status'))
  AND (sqlc.narg('category_id')::uuid IS NULL OR e.category_id = sqlc.narg('category_id'))
  AND (sqlc.narg('project_id')::uuid IS NULL OR e.project_id = sqlc.narg('project_id'))
  AND (sqlc.narg('from')::date IS NULL OR e.expense_date >= sqlc.narg('from'))
  AND (sqlc.narg('to')::date IS NULL OR e.expense_date <= sqlc.narg('to'));

-- name: UpsertVendor :one
INSERT INTO vendors (organization_id, name) VALUES (@organization_id, @name)
ON CONFLICT (organization_id, lower(name)) DO UPDATE SET name = vendors.name
RETURNING id;

-- name: ListVendors :many
SELECT v.id, v.name, count(e.id)::bigint AS expense_count
FROM vendors v LEFT JOIN expenses e ON e.vendor_id = v.id
GROUP BY v.id, v.name
ORDER BY lower(v.name);

-- name: GetVendor :one
SELECT * FROM vendors WHERE id = @id;
