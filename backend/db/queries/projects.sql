-- name: CreateProject :one
INSERT INTO projects (organization_id, customer_id, name, code, description, status, currency, start_date, end_date)
VALUES (@organization_id, @customer_id, @name, @code, @description, @status, @currency, @start_date, @end_date)
RETURNING *;

-- name: UpdateProject :one
UPDATE projects
SET customer_id = @customer_id, name = @name, code = @code, description = @description, status = @status,
    start_date = @start_date, end_date = @end_date
WHERE id = @id
RETURNING *;

-- name: GetProject :one
SELECT p.*, c.name AS customer_name
FROM projects p JOIN customers c ON c.id = p.customer_id
WHERE p.id = @id;

-- name: ListProjects :many
SELECT p.*, c.name AS customer_name,
       coalesce(b.total, 0)::numeric AS budget_total,
       coalesce(e.approved, 0)::numeric AS actual_total,
       coalesce(e.pending, 0)::numeric AS pending_total
FROM projects p
JOIN customers c ON c.id = p.customer_id
LEFT JOIN LATERAL (SELECT sum(amount) AS total FROM budgets WHERE project_id = p.id) b ON true
LEFT JOIN LATERAL (
    SELECT sum(amount) FILTER (WHERE approval_status = 'approved') AS approved,
           sum(amount) FILTER (WHERE approval_status = 'pending') AS pending
    FROM expenses WHERE project_id = p.id
) e ON true
WHERE (sqlc.narg('pattern')::text IS NULL OR p.name ILIKE sqlc.narg('pattern') OR p.code ILIKE sqlc.narg('pattern')
       OR c.name ILIKE sqlc.narg('pattern'))
  AND (sqlc.narg('status')::text IS NULL OR p.status = sqlc.narg('status'))
  AND (sqlc.narg('customer_id')::uuid IS NULL OR p.customer_id = sqlc.narg('customer_id'))
ORDER BY p.start_date DESC, lower(p.name), p.id
LIMIT @row_limit OFFSET @row_offset;

-- name: CountProjects :one
SELECT count(*) FROM projects p JOIN customers c ON c.id = p.customer_id
WHERE (sqlc.narg('pattern')::text IS NULL OR p.name ILIKE sqlc.narg('pattern') OR p.code ILIKE sqlc.narg('pattern')
       OR c.name ILIKE sqlc.narg('pattern'))
  AND (sqlc.narg('status')::text IS NULL OR p.status = sqlc.narg('status'))
  AND (sqlc.narg('customer_id')::uuid IS NULL OR p.customer_id = sqlc.narg('customer_id'));

-- name: ProjectExpenseTotalsByCategory :many
SELECT e.category_id, ec.name AS category_name,
       coalesce(sum(e.amount) FILTER (WHERE e.approval_status = 'approved'), 0)::numeric AS approved,
       coalesce(sum(e.amount) FILTER (WHERE e.approval_status = 'pending'), 0)::numeric AS pending
FROM expenses e JOIN expense_categories ec ON ec.id = e.category_id
WHERE e.project_id = @project_id
GROUP BY e.category_id, ec.name;

-- name: ExpenseTotalsByProjectCategory :many
SELECT e.project_id::uuid AS project_id, e.category_id,
       coalesce(sum(e.amount) FILTER (WHERE e.approval_status = 'approved'), 0)::numeric AS approved,
       coalesce(sum(e.amount) FILTER (WHERE e.approval_status = 'pending'), 0)::numeric AS pending
FROM expenses e
WHERE e.project_id IS NOT NULL
  AND (sqlc.narg('project_id')::uuid IS NULL OR e.project_id = sqlc.narg('project_id'))
GROUP BY e.project_id, e.category_id;

-- name: ProjectBilling :one
SELECT coalesce(sum(total) FILTER (WHERE status NOT IN ('draft', 'void')), 0)::numeric AS billed,
       coalesce(sum(amount_paid), 0)::numeric AS collected,
       count(*) FILTER (WHERE status NOT IN ('draft', 'void'))::bigint AS invoice_count
FROM invoices WHERE project_id = @project_id;

-- name: ListCustomerProjects :many
SELECT id, name, code, status, start_date, end_date, currency FROM projects
WHERE customer_id = @customer_id ORDER BY start_date DESC, name;

-- name: ListBudgetLines :many
SELECT b.*, ec.name AS category_name
FROM budgets b LEFT JOIN expense_categories ec ON ec.id = b.category_id
WHERE b.project_id = @project_id
ORDER BY ec.name NULLS FIRST;

-- name: ListAllBudgets :many
SELECT b.*, ec.name AS category_name, p.name AS project_name, p.status AS project_status
FROM budgets b
JOIN projects p ON p.id = b.project_id
LEFT JOIN expense_categories ec ON ec.id = b.category_id
WHERE (sqlc.narg('project_id')::uuid IS NULL OR b.project_id = sqlc.narg('project_id'))
ORDER BY lower(p.name), ec.name NULLS FIRST;

-- name: GetBudgetForUpdate :one
SELECT * FROM budgets WHERE id = @id FOR UPDATE;

-- name: CreateBudget :one
INSERT INTO budgets (organization_id, project_id, category_id, currency, amount, notes, created_by)
VALUES (@organization_id, @project_id, @category_id, @currency, @amount, @notes, @created_by)
RETURNING *;

-- name: UpdateBudget :one
UPDATE budgets SET amount = @amount, notes = @notes, version = version + 1
WHERE id = @id
RETURNING *;

-- name: DeleteBudget :exec
DELETE FROM budgets WHERE id = @id;

-- name: ListExpenseCategories :many
SELECT * FROM expense_categories
WHERE organization_id = @organization_id AND (sqlc.narg('include_archived')::boolean IS TRUE OR NOT is_archived)
ORDER BY lower(name);

-- name: GetExpenseCategory :one
SELECT * FROM expense_categories WHERE id = @id;

-- name: CreateExpenseCategory :one
INSERT INTO expense_categories (organization_id, name) VALUES (@organization_id, @name) RETURNING *;
