-- name: CreateReconciliationRun :one
INSERT INTO reconciliation_runs (organization_id, trigger, started_by) VALUES (@organization_id, @trigger, @started_by) RETURNING *;

-- name: FinishReconciliationRun :exec
UPDATE reconciliation_runs SET finished_at = now(), counts = @counts WHERE id = @id;

-- name: LatestReconciliationRun :one
SELECT * FROM reconciliation_runs ORDER BY started_at DESC LIMIT 1;

-- name: ReconInternalInvoices :many
SELECT i.id, i.number, i.issue_date, i.total, i.currency, i.status, i.source, i.external_id, c.name AS customer_name
FROM invoices i JOIN customers c ON c.id = i.customer_id
WHERE i.status IN ('sent', 'partially_paid', 'paid');

-- name: ReconInternalPayments :many
SELECT p.id, p.invoice_id, p.amount, p.paid_on, p.currency, p.external_id, p.source
FROM payments p WHERE p.status = 'completed';

-- name: ReconExternalRecords :many
SELECT * FROM external_records WHERE entity_type IN ('invoice', 'payment');

-- name: CurrentReconciliationRecords :many
SELECT * FROM reconciliation_records WHERE review_status <> 'superseded';

-- name: InsertReconciliationRecord :exec
INSERT INTO reconciliation_records (organization_id, run_id, entity_type, match_key, fingerprint, match_status, external_record_id,
                                    invoice_id, payment_id, reference, counterparty, currency, external_amount, internal_amount,
                                    difference, external_date, internal_date, review_status)
VALUES (@organization_id, @run_id, @entity_type, @match_key, @fingerprint, @match_status, @external_record_id,
        @invoice_id, @payment_id, @reference, @counterparty, @currency, @external_amount, @internal_amount,
        @difference, @external_date, @internal_date, @review_status);

-- name: TouchReconciliationRecord :exec
UPDATE reconciliation_records SET run_id = @run_id WHERE id = @id;

-- name: SupersedeReconciliationRecord :exec
UPDATE reconciliation_records SET review_status = 'superseded' WHERE id = @id;

-- name: GetReconciliationRecordForUpdate :one
SELECT * FROM reconciliation_records WHERE id = @id FOR UPDATE;

-- name: DecideReconciliationRecord :one
UPDATE reconciliation_records
SET decision = @decision, decision_note = @decision_note, reviewed_by = @reviewed_by, reviewed_at = now(),
    review_status = 'reviewed', invoice_id = coalesce(sqlc.narg('invoice_id'), invoice_id)
WHERE id = @id
RETURNING *;

-- name: ListReconciliationRecords :many
SELECT r.*, u.name AS reviewed_by_name
FROM reconciliation_records r LEFT JOIN users u ON u.id = r.reviewed_by
WHERE (sqlc.narg('review_status')::text IS NULL OR r.review_status = sqlc.narg('review_status'))
  AND (sqlc.narg('match_status')::text IS NULL OR r.match_status = sqlc.narg('match_status'))
  AND (sqlc.narg('entity_type')::text IS NULL OR r.entity_type = sqlc.narg('entity_type'))
  AND (sqlc.narg('include_superseded')::boolean IS TRUE OR r.review_status <> 'superseded')
ORDER BY CASE r.review_status WHEN 'open' THEN 0 WHEN 'reviewed' THEN 1 WHEN 'not_required' THEN 2 ELSE 3 END,
         CASE r.match_status WHEN 'amount_mismatch' THEN 0 WHEN 'missing_internal' THEN 1 WHEN 'missing_external' THEN 2 WHEN 'date_mismatch' THEN 3 ELSE 4 END,
         r.external_date DESC NULLS LAST, r.id
LIMIT @row_limit OFFSET @row_offset;

-- name: CountReconciliationRecords :one
SELECT count(*) FROM reconciliation_records r
WHERE (sqlc.narg('review_status')::text IS NULL OR r.review_status = sqlc.narg('review_status'))
  AND (sqlc.narg('match_status')::text IS NULL OR r.match_status = sqlc.narg('match_status'))
  AND (sqlc.narg('entity_type')::text IS NULL OR r.entity_type = sqlc.narg('entity_type'))
  AND (sqlc.narg('include_superseded')::boolean IS TRUE OR r.review_status <> 'superseded');

-- name: ReconciliationSummary :many
SELECT match_status, review_status, count(*)::bigint AS n
FROM reconciliation_records WHERE review_status <> 'superseded'
GROUP BY match_status, review_status;

-- name: InvoiceNumberExists :one
SELECT EXISTS (SELECT 1 FROM invoices WHERE organization_id = @organization_id AND number = @number);

-- name: LockReconciliation :exec
SELECT pg_advisory_xact_lock(hashtextextended('reconcile:' || @organization_id::text, 0));

-- name: GetExternalRecord :one
SELECT * FROM external_records WHERE id = @id;

-- name: ReconciliationHistory :many
SELECT r.*, u.name AS reviewed_by_name
FROM reconciliation_records r LEFT JOIN users u ON u.id = r.reviewed_by
WHERE r.match_key = @match_key
ORDER BY r.created_at;
