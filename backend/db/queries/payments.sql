-- name: GetPaymentByIdempotencyKey :one
SELECT * FROM payments WHERE organization_id = @organization_id AND idempotency_key = @idempotency_key;

-- name: InsertPayment :one
INSERT INTO payments (organization_id, invoice_id, amount, currency, paid_on, method, reference, idempotency_key,
                      request_fingerprint, recorded_by, source, external_id)
VALUES (@organization_id, @invoice_id, @amount, @currency, @paid_on, @method, @reference, @idempotency_key,
        @request_fingerprint, @recorded_by, @source, @external_id)
RETURNING *;

-- name: GetPaymentForUpdate :one
SELECT * FROM payments WHERE id = @id FOR UPDATE;

-- name: GetPayment :one
SELECT p.*, i.number AS invoice_number, c.name AS customer_name
FROM payments p JOIN invoices i ON i.id = p.invoice_id JOIN customers c ON c.id = i.customer_id
WHERE p.id = @id;

-- name: ReversePayment :one
UPDATE payments SET status = 'reversed', reversed_at = now(), reversed_by = @reversed_by, reversal_reason = @reversal_reason
WHERE id = @id RETURNING *;

-- name: ListInvoicePayments :many
SELECT p.*, u.name AS recorded_by_name
FROM payments p LEFT JOIN users u ON u.id = p.recorded_by
WHERE p.invoice_id = @invoice_id
ORDER BY p.paid_on, p.created_at;

-- name: ListPayments :many
SELECT p.id, p.invoice_id, p.amount, p.currency, p.paid_on, p.method, p.reference, p.status, p.source, p.created_at,
       i.number AS invoice_number, c.id AS customer_id, c.name AS customer_name
FROM payments p
JOIN invoices i ON i.id = p.invoice_id
JOIN customers c ON c.id = i.customer_id
WHERE (sqlc.narg('from')::date IS NULL OR p.paid_on >= sqlc.narg('from'))
  AND (sqlc.narg('to')::date IS NULL OR p.paid_on <= sqlc.narg('to'))
  AND (sqlc.narg('method')::text IS NULL OR p.method = sqlc.narg('method'))
  AND (sqlc.narg('status')::text IS NULL OR p.status = sqlc.narg('status'))
  AND (sqlc.narg('pattern')::text IS NULL OR i.number ILIKE sqlc.narg('pattern') OR c.name ILIKE sqlc.narg('pattern')
       OR p.reference ILIKE sqlc.narg('pattern'))
ORDER BY p.paid_on DESC, p.created_at DESC
LIMIT @row_limit OFFSET @row_offset;

-- name: CountPayments :one
SELECT count(*)
FROM payments p JOIN invoices i ON i.id = p.invoice_id JOIN customers c ON c.id = i.customer_id
WHERE (sqlc.narg('from')::date IS NULL OR p.paid_on >= sqlc.narg('from'))
  AND (sqlc.narg('to')::date IS NULL OR p.paid_on <= sqlc.narg('to'))
  AND (sqlc.narg('method')::text IS NULL OR p.method = sqlc.narg('method'))
  AND (sqlc.narg('status')::text IS NULL OR p.status = sqlc.narg('status'))
  AND (sqlc.narg('pattern')::text IS NULL OR i.number ILIKE sqlc.narg('pattern') OR c.name ILIKE sqlc.narg('pattern')
       OR p.reference ILIKE sqlc.narg('pattern'));
