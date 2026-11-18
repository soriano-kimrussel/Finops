-- name: GetConnection :one
SELECT * FROM sync_connections WHERE organization_id = @organization_id AND provider = 'quickbooks';

-- name: GetConnectionByID :one
SELECT * FROM sync_connections WHERE id = @id;

-- name: GetConnectionForUpdate :one
SELECT * FROM sync_connections WHERE id = @id FOR UPDATE;

-- name: UpsertConnection :one
INSERT INTO sync_connections (organization_id, provider, mode, status, realm_id, company_name, access_token_enc, refresh_token_enc,
                              access_token_expires_at, refresh_token_expires_at, connected_by, connected_at, last_error)
VALUES (@organization_id, 'quickbooks', @mode, 'connected', @realm_id, @company_name, @access_token_enc, @refresh_token_enc,
        @access_token_expires_at, @refresh_token_expires_at, @connected_by, now(), NULL)
ON CONFLICT (organization_id, provider) DO UPDATE
SET mode = EXCLUDED.mode, status = 'connected', realm_id = EXCLUDED.realm_id, company_name = EXCLUDED.company_name,
    access_token_enc = EXCLUDED.access_token_enc, refresh_token_enc = EXCLUDED.refresh_token_enc,
    access_token_expires_at = EXCLUDED.access_token_expires_at, refresh_token_expires_at = EXCLUDED.refresh_token_expires_at,
    connected_by = EXCLUDED.connected_by, connected_at = now(), disconnected_at = NULL, last_error = NULL
RETURNING *;

-- name: UpdateConnectionTokens :exec
UPDATE sync_connections
SET access_token_enc = @access_token_enc, refresh_token_enc = @refresh_token_enc,
    access_token_expires_at = @access_token_expires_at, refresh_token_expires_at = @refresh_token_expires_at
WHERE id = @id;

-- name: SetConnectionStatus :exec
UPDATE sync_connections SET status = @status, last_error = @last_error WHERE id = @id;

-- name: DisconnectConnection :one
UPDATE sync_connections
SET status = 'disconnected', access_token_enc = NULL, refresh_token_enc = NULL, access_token_expires_at = NULL,
    refresh_token_expires_at = NULL, disconnected_at = now()
WHERE id = @id
RETURNING *;

-- name: TouchConnectionSynced :exec
UPDATE sync_connections SET last_synced_at = now(), last_error = NULL, status = 'connected' WHERE id = @id AND status <> 'disconnected';

-- name: CreateOAuthState :exec
INSERT INTO oauth_states (organization_id, user_id, state_hash, expires_at) VALUES (@organization_id, @user_id, @state_hash, @expires_at);

-- name: ConsumeOAuthState :one
UPDATE oauth_states SET used_at = now()
WHERE state_hash = @state_hash AND used_at IS NULL AND expires_at > now()
RETURNING *;

-- name: EnqueueSyncJob :one
INSERT INTO sync_jobs (organization_id, connection_id, kind, trigger, idempotency_key, requested_by, max_attempts, retry_of)
VALUES (@organization_id, @connection_id, @kind, @trigger, @idempotency_key, @requested_by, @max_attempts, @retry_of)
RETURNING *;

-- name: GetActiveSyncJob :one
SELECT * FROM sync_jobs WHERE connection_id = @connection_id AND kind = @kind AND status IN ('queued', 'running', 'retrying');

-- name: GetSyncJobByKey :one
SELECT * FROM sync_jobs WHERE organization_id = @organization_id AND idempotency_key = @idempotency_key;

-- name: GetSyncJob :one
SELECT * FROM sync_jobs WHERE id = @id;

-- name: ListSyncJobs :many
SELECT j.*, c.mode AS connection_mode, u.name AS requested_by_name
FROM sync_jobs j
JOIN sync_connections c ON c.id = j.connection_id
LEFT JOIN users u ON u.id = j.requested_by
WHERE (sqlc.narg('status')::text IS NULL OR j.status = sqlc.narg('status'))
ORDER BY j.created_at DESC
LIMIT @row_limit OFFSET @row_offset;

-- name: CountSyncJobs :one
SELECT count(*) FROM sync_jobs j WHERE (sqlc.narg('status')::text IS NULL OR j.status = sqlc.narg('status'));

-- name: ListSyncJobAttempts :many
SELECT * FROM sync_job_attempts WHERE job_id = @job_id ORDER BY attempt;

-- name: ExtendSyncJobLease :execrows
UPDATE sync_jobs SET locked_until = now() + make_interval(secs => @lease_seconds::int)
WHERE id = @id AND status = 'running' AND locked_by = @locked_by;

-- name: CompleteSyncJob :execrows
UPDATE sync_jobs SET status = 'succeeded', finished_at = now(), locked_by = NULL, locked_until = NULL, last_error = NULL, stats = @stats
WHERE id = @id AND status = 'running' AND locked_by = @locked_by;

-- name: RetrySyncJobLater :execrows
UPDATE sync_jobs SET status = 'retrying', run_at = @run_at, locked_by = NULL, locked_until = NULL, last_error = @last_error, stats = @stats
WHERE id = @id AND status = 'running' AND locked_by = @locked_by;

-- name: FailSyncJob :execrows
UPDATE sync_jobs SET status = 'failed', finished_at = now(), locked_by = NULL, locked_until = NULL, last_error = @last_error, stats = @stats
WHERE id = @id AND status = 'running' AND locked_by = @locked_by;

-- name: InsertSyncJobAttempt :exec
INSERT INTO sync_job_attempts (organization_id, job_id, attempt, worker, started_at, outcome, error, stats)
VALUES (@organization_id, @job_id, @attempt, @worker, @started_at, @outcome, @error, @stats)
ON CONFLICT (job_id, attempt) DO NOTHING;

-- name: UpsertExternalRecord :one
INSERT INTO external_records (organization_id, connection_id, entity_type, external_id, doc_number, display_name, customer_external_id,
                              invoice_external_id, txn_date, due_date, amount, balance, currency, raw, payload_hash,
                              external_updated_at, first_seen_job, last_seen_job)
VALUES (@organization_id, @connection_id, @entity_type, @external_id, @doc_number, @display_name, @customer_external_id,
        @invoice_external_id, @txn_date, @due_date, @amount, @balance, @currency, @raw, @payload_hash,
        @external_updated_at, @job_id, @job_id)
ON CONFLICT (organization_id, connection_id, entity_type, external_id) DO UPDATE
SET doc_number = EXCLUDED.doc_number, display_name = EXCLUDED.display_name, customer_external_id = EXCLUDED.customer_external_id,
    invoice_external_id = EXCLUDED.invoice_external_id, txn_date = EXCLUDED.txn_date, due_date = EXCLUDED.due_date,
    amount = EXCLUDED.amount, balance = EXCLUDED.balance, currency = EXCLUDED.currency, raw = EXCLUDED.raw,
    payload_hash = EXCLUDED.payload_hash, external_updated_at = EXCLUDED.external_updated_at, last_seen_job = EXCLUDED.last_seen_job
WHERE external_records.payload_hash IS DISTINCT FROM EXCLUDED.payload_hash
RETURNING (xmax = 0)::boolean AS inserted;

-- name: ListExternalRecords :many
SELECT * FROM external_records
WHERE (sqlc.narg('entity_type')::text IS NULL OR entity_type = sqlc.narg('entity_type'))
ORDER BY entity_type, txn_date DESC NULLS LAST, external_id
LIMIT @row_limit OFFSET @row_offset;

-- name: GetCustomerByExternalID :one
SELECT * FROM customers WHERE source = @source AND external_id = @external_id;

-- name: FindCustomerByName :one
SELECT * FROM customers WHERE lower(name) = lower(@name) ORDER BY created_at LIMIT 1;

-- name: LinkCustomerExternal :exec
UPDATE customers SET external_id = @external_id WHERE id = @id AND (external_id IS NULL OR external_id = @external_id);

-- name: CustomerByLinkedExternalID :one
SELECT * FROM customers WHERE external_id = @external_id ORDER BY (source = 'quickbooks') DESC LIMIT 1;

-- name: SyncSnapshotCustomers :many
SELECT id, name, email, currency FROM customers ORDER BY created_at, id;

-- name: SyncSnapshotInvoices :many
SELECT i.id, i.number, i.customer_id, c.name AS customer_name, i.issue_date, i.due_date, i.total, i.amount_paid, i.currency, i.status
FROM invoices i JOIN customers c ON c.id = i.customer_id
WHERE i.status IN ('sent', 'partially_paid', 'paid') AND i.source = 'internal'
ORDER BY i.issue_date, i.number;

-- name: SyncSnapshotPayments :many
SELECT p.id, p.invoice_id, i.number AS invoice_number, p.amount, p.paid_on, p.currency, p.method
FROM payments p JOIN invoices i ON i.id = p.invoice_id
WHERE p.status = 'completed' AND p.source = 'internal'
ORDER BY p.paid_on, p.id;
