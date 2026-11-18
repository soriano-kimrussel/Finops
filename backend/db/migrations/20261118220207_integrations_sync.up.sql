CREATE TABLE sync_connections (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id          uuid NOT NULL REFERENCES organizations (id),
    provider                 text NOT NULL DEFAULT 'quickbooks',
    mode                     text NOT NULL,
    status                   text NOT NULL,
    realm_id                 text,
    company_name             text,
    -- AES-256-GCM ciphertexts; the key lives in TOKEN_ENCRYPTION_KEY, never in the database.
    access_token_enc         bytea,
    refresh_token_enc        bytea,
    access_token_expires_at  timestamptz,
    refresh_token_expires_at timestamptz,
    connected_by             uuid REFERENCES users (id),
    connected_at             timestamptz,
    disconnected_at          timestamptz,
    last_synced_at           timestamptz,
    last_error               text,
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT sync_connections_org_id_key UNIQUE (organization_id, id),
    CONSTRAINT sync_connections_org_provider_key UNIQUE (organization_id, provider),
    CONSTRAINT sync_connections_provider_check CHECK (provider IN ('quickbooks')),
    CONSTRAINT sync_connections_mode_check CHECK (mode IN ('live', 'simulated')),
    CONSTRAINT sync_connections_status_check CHECK (status IN ('connected', 'needs_reauth', 'error', 'disconnected')),
    CONSTRAINT sync_connections_live_tokens CHECK (
        mode = 'simulated' OR status <> 'connected' OR (realm_id IS NOT NULL AND refresh_token_enc IS NOT NULL)
    )
);
CREATE UNIQUE INDEX sync_connections_realm_key ON sync_connections (provider, realm_id)
    WHERE realm_id IS NOT NULL AND status <> 'disconnected';
CREATE TRIGGER sync_connections_set_updated_at BEFORE UPDATE ON sync_connections FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE oauth_states (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id),
    user_id         uuid NOT NULL REFERENCES users (id),
    state_hash      bytea NOT NULL,
    expires_at      timestamptz NOT NULL,
    used_at         timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT oauth_states_hash_key UNIQUE (state_hash)
);

CREATE TABLE sync_jobs (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id),
    connection_id   uuid NOT NULL,
    kind            text NOT NULL,
    trigger         text NOT NULL DEFAULT 'manual',
    status          text NOT NULL DEFAULT 'queued',
    attempts        integer NOT NULL DEFAULT 0,
    max_attempts    integer NOT NULL DEFAULT 5,
    run_at          timestamptz NOT NULL DEFAULT now(),
    locked_by       text,
    locked_until    timestamptz,
    started_at      timestamptz,
    finished_at     timestamptz,
    last_error      text,
    stats           jsonb NOT NULL DEFAULT '{}'::jsonb,
    idempotency_key text NOT NULL,
    retry_of        uuid REFERENCES sync_jobs (id),
    requested_by    uuid REFERENCES users (id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT sync_jobs_connection_fkey FOREIGN KEY (organization_id, connection_id) REFERENCES sync_connections (organization_id, id),
    CONSTRAINT sync_jobs_idempotency_key UNIQUE (organization_id, idempotency_key),
    CONSTRAINT sync_jobs_kind_check CHECK (kind IN ('customers', 'invoices', 'payments', 'full')),
    CONSTRAINT sync_jobs_trigger_check CHECK (trigger IN ('manual', 'webhook', 'retry')),
    CONSTRAINT sync_jobs_status_check CHECK (status IN ('queued', 'running', 'retrying', 'succeeded', 'failed')),
    CONSTRAINT sync_jobs_attempts_check CHECK (attempts >= 0 AND max_attempts BETWEEN 1 AND 20),
    CONSTRAINT sync_jobs_running_lock CHECK (status <> 'running' OR (locked_by IS NOT NULL AND locked_until IS NOT NULL))
);
-- At most one active job per connection and kind: duplicate requests reuse the existing job.
CREATE UNIQUE INDEX sync_jobs_one_active ON sync_jobs (connection_id, kind) WHERE status IN ('queued', 'running', 'retrying');
CREATE INDEX sync_jobs_claim_idx ON sync_jobs (run_at) WHERE status IN ('queued', 'retrying');
CREATE INDEX sync_jobs_lease_idx ON sync_jobs (locked_until) WHERE status = 'running';
CREATE INDEX sync_jobs_org_created_idx ON sync_jobs (organization_id, created_at DESC);
CREATE TRIGGER sync_jobs_set_updated_at BEFORE UPDATE ON sync_jobs FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE sync_job_attempts (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id),
    job_id          uuid NOT NULL REFERENCES sync_jobs (id),
    attempt         integer NOT NULL,
    worker          text NOT NULL,
    started_at      timestamptz NOT NULL,
    finished_at     timestamptz NOT NULL DEFAULT now(),
    outcome         text NOT NULL,
    error           text,
    stats           jsonb NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT sync_job_attempts_key UNIQUE (job_id, attempt),
    CONSTRAINT sync_job_attempts_outcome_check CHECK (outcome IN ('succeeded', 'retrying', 'failed'))
);

-- Raw records fetched from the external system. They are staged here and compared during
-- reconciliation; invoices and payments never overwrite internal financial records silently.
CREATE TABLE external_records (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     uuid NOT NULL REFERENCES organizations (id),
    connection_id       uuid NOT NULL,
    entity_type         text NOT NULL,
    external_id         text NOT NULL,
    doc_number          text,
    display_name        text,
    customer_external_id text,
    invoice_external_id text,
    txn_date            date,
    due_date            date,
    amount              numeric(19, 4),
    balance             numeric(19, 4),
    currency            char(3),
    raw                 jsonb NOT NULL,
    payload_hash        bytea NOT NULL,
    external_updated_at timestamptz,
    first_seen_job      uuid REFERENCES sync_jobs (id),
    last_seen_job       uuid REFERENCES sync_jobs (id),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT external_records_org_id_key UNIQUE (organization_id, id),
    CONSTRAINT external_records_connection_fkey FOREIGN KEY (organization_id, connection_id) REFERENCES sync_connections (organization_id, id),
    CONSTRAINT external_records_key UNIQUE (organization_id, connection_id, entity_type, external_id),
    CONSTRAINT external_records_entity_check CHECK (entity_type IN ('customer', 'invoice', 'payment')),
    CONSTRAINT external_records_amount_check CHECK (amount IS NULL OR amount >= 0)
);
CREATE INDEX external_records_doc_idx ON external_records (organization_id, entity_type, doc_number);
CREATE TRIGGER external_records_set_updated_at BEFORE UPDATE ON external_records FOR EACH ROW EXECUTE FUNCTION set_updated_at();

DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['sync_connections', 'oauth_states', 'sync_jobs', 'sync_job_attempts', 'external_records']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('CREATE POLICY %I ON %I USING (organization_id = app_current_org()) WITH CHECK (organization_id = app_current_org())',
            t || '_tenant', t);
    END LOOP;
END
$$;

REVOKE UPDATE, DELETE ON sync_job_attempts FROM finops_app;

-- Workers serve every tenant, so claiming work must look across organizations. These two
-- functions are the only cross-tenant entry points. They run as the schema owner, return just
-- enough to set the tenant context, and the caller does all real work under RLS.
CREATE FUNCTION claim_sync_job(p_worker text, p_lease_seconds integer)
RETURNS TABLE (job_id uuid, organization_id uuid, attempt integer, max_attempts integer)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
    UPDATE sync_jobs j
    SET status = 'running', attempts = j.attempts + 1, locked_by = p_worker,
        locked_until = now() + make_interval(secs => p_lease_seconds), started_at = coalesce(j.started_at, now())
    WHERE j.id = (
        SELECT c.id FROM sync_jobs c
        WHERE (c.status IN ('queued', 'retrying') AND c.run_at <= now())
           OR (c.status = 'running' AND c.locked_until < now())
        ORDER BY c.run_at
        FOR UPDATE SKIP LOCKED
        LIMIT 1
    )
    RETURNING j.id, j.organization_id, j.attempts, j.max_attempts
$$;

CREATE FUNCTION find_quickbooks_connection(p_realm text)
RETURNS TABLE (connection_id uuid, organization_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
    SELECT id, organization_id FROM sync_connections
    WHERE provider = 'quickbooks' AND realm_id = p_realm AND status = 'connected'
$$;

REVOKE ALL ON FUNCTION claim_sync_job(text, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION find_quickbooks_connection(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION claim_sync_job(text, integer) TO finops_app;
GRANT EXECUTE ON FUNCTION find_quickbooks_connection(text) TO finops_app;
