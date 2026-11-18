-- name: CreateInvoiceSequence :exec
INSERT INTO invoice_number_sequences (organization_id) VALUES (@organization_id) ON CONFLICT DO NOTHING;

-- Row lock on the sequence serializes numbering; a rollback also rolls back the counter, so
-- invoice numbers are gapless.
-- name: NextInvoiceNumber :one
UPDATE invoice_number_sequences SET next_value = next_value + 1
WHERE organization_id = @organization_id
RETURNING prefix, (next_value - 1)::bigint AS value;

-- name: CreateInvoice :one
INSERT INTO invoices (organization_id, customer_id, project_id, number, status, currency, issue_date, due_date,
                      discount_type, discount_value, tax_rate, subtotal, discount_total, tax_total, total, notes,
                      source, external_id, created_by)
VALUES (@organization_id, @customer_id, @project_id, @number, @status, @currency, @issue_date, @due_date,
        @discount_type, @discount_value, @tax_rate, @subtotal, @discount_total, @tax_total, @total, @notes,
        @source, @external_id, @created_by)
RETURNING *;

-- name: UpdateInvoiceTerms :one
UPDATE invoices
SET customer_id = @customer_id, project_id = @project_id, currency = @currency, issue_date = @issue_date,
    due_date = @due_date, discount_type = @discount_type, discount_value = @discount_value, tax_rate = @tax_rate,
    subtotal = @subtotal, discount_total = @discount_total, tax_total = @tax_total, total = @total, notes = @notes,
    version = version + 1
WHERE id = @id
RETURNING *;

-- name: InsertInvoiceItem :exec
INSERT INTO invoice_items (organization_id, invoice_id, position, description, quantity, unit_price, line_total)
VALUES (@organization_id, @invoice_id, @position, @description, @quantity, @unit_price, @line_total);

-- name: DeleteInvoiceItems :exec
DELETE FROM invoice_items WHERE invoice_id = @invoice_id;

-- name: ListInvoiceItems :many
SELECT * FROM invoice_items WHERE invoice_id = @invoice_id ORDER BY position;

-- name: GetInvoice :one
SELECT i.*, c.name AS customer_name, c.email AS customer_email, p.name AS project_name
FROM invoices i
JOIN customers c ON c.id = i.customer_id
LEFT JOIN projects p ON p.id = i.project_id
WHERE i.id = @id;

-- name: GetInvoiceForUpdate :one
SELECT * FROM invoices WHERE id = @id FOR UPDATE;

-- name: MarkInvoiceSent :one
UPDATE invoices SET status = 'sent', sent_at = now(), version = version + 1 WHERE id = @id RETURNING *;

-- name: VoidInvoice :one
UPDATE invoices SET status = 'void', voided_at = now(), void_reason = @void_reason, version = version + 1
WHERE id = @id RETURNING *;

-- name: SetInvoicePaid :one
UPDATE invoices SET amount_paid = @amount_paid, status = @status, version = version + 1
WHERE id = @id RETURNING *;

-- name: ListInvoices :many
SELECT i.id, i.number, i.status, i.currency, i.issue_date, i.due_date, i.total, i.amount_paid, i.source,
       i.customer_id, c.name AS customer_name, i.project_id, p.name AS project_name,
       (i.status IN ('sent', 'partially_paid') AND i.due_date < @today::date)::boolean AS is_overdue
FROM invoices i
JOIN customers c ON c.id = i.customer_id
LEFT JOIN projects p ON p.id = i.project_id
WHERE (sqlc.narg('status')::text IS NULL
       OR (sqlc.narg('status') = 'overdue' AND i.status IN ('sent', 'partially_paid') AND i.due_date < @today::date)
       OR (sqlc.narg('status') = 'outstanding' AND i.status IN ('sent', 'partially_paid'))
       OR i.status = sqlc.narg('status'))
  AND (sqlc.narg('customer_id')::uuid IS NULL OR i.customer_id = sqlc.narg('customer_id'))
  AND (sqlc.narg('project_id')::uuid IS NULL OR i.project_id = sqlc.narg('project_id'))
  AND (sqlc.narg('pattern')::text IS NULL OR i.number ILIKE sqlc.narg('pattern') OR c.name ILIKE sqlc.narg('pattern'))
  AND (sqlc.narg('from')::date IS NULL OR i.issue_date >= sqlc.narg('from'))
  AND (sqlc.narg('to')::date IS NULL OR i.issue_date <= sqlc.narg('to'))
ORDER BY i.issue_date DESC, i.number DESC
LIMIT @row_limit OFFSET @row_offset;

-- name: CountInvoices :one
SELECT count(*)
FROM invoices i JOIN customers c ON c.id = i.customer_id
WHERE (sqlc.narg('status')::text IS NULL
       OR (sqlc.narg('status') = 'overdue' AND i.status IN ('sent', 'partially_paid') AND i.due_date < @today::date)
       OR (sqlc.narg('status') = 'outstanding' AND i.status IN ('sent', 'partially_paid'))
       OR i.status = sqlc.narg('status'))
  AND (sqlc.narg('customer_id')::uuid IS NULL OR i.customer_id = sqlc.narg('customer_id'))
  AND (sqlc.narg('project_id')::uuid IS NULL OR i.project_id = sqlc.narg('project_id'))
  AND (sqlc.narg('pattern')::text IS NULL OR i.number ILIKE sqlc.narg('pattern') OR c.name ILIKE sqlc.narg('pattern'))
  AND (sqlc.narg('from')::date IS NULL OR i.issue_date >= sqlc.narg('from'))
  AND (sqlc.narg('to')::date IS NULL OR i.issue_date <= sqlc.narg('to'));
