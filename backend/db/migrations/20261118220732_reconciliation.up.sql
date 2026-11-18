CREATE TABLE reconciliation_runs (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id),
    trigger         text NOT NULL,
    started_by      uuid REFERENCES users (id),
    started_at      timestamptz NOT NULL DEFAULT now(),
    finished_at     timestamptz,
    counts          jsonb NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT reconciliation_runs_org_id_key UNIQUE (organization_id, id),
    CONSTRAINT reconciliation_runs_trigger_check CHECK (trigger IN ('manual', 'sync'))
);
CREATE INDEX reconciliation_runs_org_started_idx ON reconciliation_runs (organization_id, started_at DESC);

-- One row per comparison outcome. A reviewed decision is never overwritten: when the
-- underlying data changes, the old row is marked superseded and a new open row is created.
CREATE TABLE reconciliation_records (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id    uuid NOT NULL REFERENCES organizations (id),
    run_id             uuid NOT NULL,
    entity_type        text NOT NULL,
    match_key          text NOT NULL,
    fingerprint        bytea NOT NULL,
    match_status       text NOT NULL,
    external_record_id uuid,
    invoice_id         uuid,
    payment_id         uuid,
    reference          text,
    counterparty       text,
    currency           char(3),
    external_amount    numeric(19, 4),
    internal_amount    numeric(19, 4),
    difference         numeric(19, 4),
    external_date      date,
    internal_date      date,
    review_status      text NOT NULL,
    decision           text,
    decision_note      text,
    reviewed_by        uuid REFERENCES users (id),
    reviewed_at        timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT reconciliation_records_run_fkey FOREIGN KEY (organization_id, run_id) REFERENCES reconciliation_runs (organization_id, id),
    CONSTRAINT reconciliation_records_external_fkey FOREIGN KEY (organization_id, external_record_id) REFERENCES external_records (organization_id, id),
    CONSTRAINT reconciliation_records_invoice_fkey FOREIGN KEY (organization_id, invoice_id) REFERENCES invoices (organization_id, id),
    CONSTRAINT reconciliation_records_payment_fkey FOREIGN KEY (organization_id, payment_id) REFERENCES payments (organization_id, id),
    CONSTRAINT reconciliation_records_fingerprint_key UNIQUE (organization_id, match_key, fingerprint),
    CONSTRAINT reconciliation_records_entity_check CHECK (entity_type IN ('invoice', 'payment')),
    CONSTRAINT reconciliation_records_status_check CHECK (match_status IN ('matched', 'amount_mismatch', 'date_mismatch', 'missing_internal', 'missing_external')),
    CONSTRAINT reconciliation_records_review_check CHECK (review_status IN ('open', 'reviewed', 'not_required', 'superseded')),
    CONSTRAINT reconciliation_records_decision_check CHECK (decision IS NULL OR decision IN ('accepted', 'ignored', 'follow_up', 'imported')),
    CONSTRAINT reconciliation_records_reviewed_fields CHECK (
        (decision IS NULL AND reviewed_by IS NULL AND reviewed_at IS NULL)
        OR (decision IS NOT NULL AND reviewed_by IS NOT NULL AND reviewed_at IS NOT NULL)
    ),
    CONSTRAINT reconciliation_records_has_side CHECK (external_record_id IS NOT NULL OR invoice_id IS NOT NULL OR payment_id IS NOT NULL)
);
CREATE UNIQUE INDEX reconciliation_records_current_key ON reconciliation_records (organization_id, match_key) WHERE review_status <> 'superseded';
CREATE INDEX reconciliation_records_queue_idx ON reconciliation_records (organization_id, review_status, match_status);
CREATE TRIGGER reconciliation_records_set_updated_at BEFORE UPDATE ON reconciliation_records FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Decisions are history: once recorded they can only be superseded or, for follow-ups, revised.
CREATE FUNCTION reconciliation_records_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'reconciliation records cannot be deleted' USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.review_status = 'superseded' THEN
        RAISE EXCEPTION 'superseded reconciliation records are immutable' USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.decision IS NOT NULL AND OLD.decision <> 'follow_up' AND NEW.review_status <> 'superseded'
        AND (NEW.decision IS DISTINCT FROM OLD.decision OR NEW.decision_note IS DISTINCT FROM OLD.decision_note) THEN
        RAISE EXCEPTION 'a recorded reconciliation decision cannot be changed' USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER reconciliation_records_history BEFORE UPDATE OR DELETE ON reconciliation_records
    FOR EACH ROW EXECUTE FUNCTION reconciliation_records_guard();

DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['reconciliation_runs', 'reconciliation_records']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('CREATE POLICY %I ON %I USING (organization_id = app_current_org()) WITH CHECK (organization_id = app_current_org())',
            t || '_tenant', t);
    END LOOP;
END
$$;

REVOKE DELETE ON reconciliation_records, reconciliation_runs FROM finops_app;
