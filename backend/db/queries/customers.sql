-- name: CreateCustomer :one
INSERT INTO customers (organization_id, name, contact_name, email, phone, address_line1, address_line2, city, region,
                       postal_code, country, currency, status, notes, source, external_id)
VALUES (@organization_id, @name, @contact_name, @email, @phone, @address_line1, @address_line2, @city, @region,
        @postal_code, @country, @currency, @status, @notes, @source, @external_id)
RETURNING *;

-- name: UpdateCustomer :one
UPDATE customers
SET name = @name, contact_name = @contact_name, email = @email, phone = @phone, address_line1 = @address_line1,
    address_line2 = @address_line2, city = @city, region = @region, postal_code = @postal_code, country = @country,
    currency = @currency, status = @status, notes = @notes
WHERE id = @id
RETURNING *;

-- name: GetCustomer :one
SELECT * FROM customers WHERE id = @id;

-- name: GetCustomerForUpdate :one
SELECT * FROM customers WHERE id = @id FOR UPDATE;

-- name: CustomerHasInvoices :one
SELECT EXISTS (SELECT 1 FROM invoices WHERE customer_id = @customer_id AND status <> 'draft');

-- name: ListCustomers :many
SELECT c.*,
       coalesce(s.outstanding, 0)::numeric AS outstanding,
       coalesce(s.invoice_count, 0)::bigint AS invoice_count
FROM customers c
LEFT JOIN LATERAL (
    SELECT sum(i.total - i.amount_paid) FILTER (WHERE i.status IN ('sent', 'partially_paid')) AS outstanding,
           count(*) FILTER (WHERE i.status <> 'void') AS invoice_count
    FROM invoices i WHERE i.customer_id = c.id
) s ON true
WHERE (sqlc.narg('pattern')::text IS NULL OR c.name ILIKE sqlc.narg('pattern') OR c.email ILIKE sqlc.narg('pattern')
       OR c.contact_name ILIKE sqlc.narg('pattern'))
  AND (sqlc.narg('status')::text IS NULL OR c.status = sqlc.narg('status'))
ORDER BY lower(c.name), c.id
LIMIT @row_limit OFFSET @row_offset;

-- name: CountCustomers :one
SELECT count(*) FROM customers c
WHERE (sqlc.narg('pattern')::text IS NULL OR c.name ILIKE sqlc.narg('pattern') OR c.email ILIKE sqlc.narg('pattern')
       OR c.contact_name ILIKE sqlc.narg('pattern'))
  AND (sqlc.narg('status')::text IS NULL OR c.status = sqlc.narg('status'));

-- name: CustomerSummary :many
SELECT currency,
       coalesce(sum(total) FILTER (WHERE status NOT IN ('draft', 'void')), 0)::numeric AS invoiced,
       coalesce(sum(amount_paid), 0)::numeric AS paid,
       coalesce(sum(total - amount_paid) FILTER (WHERE status IN ('sent', 'partially_paid')), 0)::numeric AS outstanding,
       coalesce(sum(total - amount_paid) FILTER (WHERE status IN ('sent', 'partially_paid') AND due_date < @today::date), 0)::numeric AS overdue
FROM invoices
WHERE customer_id = @customer_id
GROUP BY currency
ORDER BY currency;
