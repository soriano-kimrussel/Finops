-- Money is NUMERIC(19,4): up to 15 integer digits, 4 fractional digits. Values are rounded to the
-- currency's minor units by the application before they are stored.

CREATE TABLE customers (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id),
    name            text NOT NULL,
    contact_name    text,
    email           citext,
    phone           text,
    address_line1   text,
    address_line2   text,
    city            text,
    region          text,
    postal_code     text,
    country         text,
    currency        char(3) NOT NULL REFERENCES currencies (code),
    status          text NOT NULL DEFAULT 'active',
    notes           text,
    source          text NOT NULL DEFAULT 'internal',
    external_id     text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT customers_org_id_key UNIQUE (organization_id, id),
    CONSTRAINT customers_external_key UNIQUE (organization_id, source, external_id),
    CONSTRAINT customers_name_length CHECK (length(btrim(name)) BETWEEN 1 AND 200),
    CONSTRAINT customers_status_check CHECK (status IN ('active', 'inactive')),
    CONSTRAINT customers_source_check CHECK (source IN ('internal', 'quickbooks')),
    CONSTRAINT customers_external_id_required CHECK (source = 'internal' OR external_id IS NOT NULL)
);
CREATE INDEX customers_org_name_idx ON customers (organization_id, lower(name));
CREATE INDEX customers_name_trgm_idx ON customers USING gin (name gin_trgm_ops);
CREATE TRIGGER customers_set_updated_at BEFORE UPDATE ON customers FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE projects (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id),
    customer_id     uuid NOT NULL,
    name            text NOT NULL,
    code            text,
    description     text,
    status          text NOT NULL DEFAULT 'active',
    currency        char(3) NOT NULL REFERENCES currencies (code),
    start_date      date NOT NULL,
    end_date        date,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT projects_org_id_key UNIQUE (organization_id, id),
    CONSTRAINT projects_org_id_currency_key UNIQUE (organization_id, id, currency),
    CONSTRAINT projects_code_key UNIQUE (organization_id, code),
    CONSTRAINT projects_customer_fkey FOREIGN KEY (organization_id, customer_id) REFERENCES customers (organization_id, id),
    CONSTRAINT projects_name_length CHECK (length(btrim(name)) BETWEEN 1 AND 200),
    CONSTRAINT projects_status_check CHECK (status IN ('planned', 'active', 'on_hold', 'completed', 'cancelled')),
    CONSTRAINT projects_dates_check CHECK (end_date IS NULL OR end_date >= start_date)
);
CREATE INDEX projects_org_customer_idx ON projects (organization_id, customer_id);
CREATE TRIGGER projects_set_updated_at BEFORE UPDATE ON projects FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- A project's currency is fixed: budgets and expenses reference it through a composite key.
CREATE FUNCTION projects_currency_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.currency IS DISTINCT FROM OLD.currency THEN
        RAISE EXCEPTION 'project currency cannot be changed' USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER projects_currency_guard BEFORE UPDATE ON projects FOR EACH ROW EXECUTE FUNCTION projects_currency_immutable();

CREATE TABLE expense_categories (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id),
    name            text NOT NULL,
    is_archived     boolean NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT expense_categories_org_id_key UNIQUE (organization_id, id),
    CONSTRAINT expense_categories_name_length CHECK (length(btrim(name)) BETWEEN 1 AND 100)
);
CREATE UNIQUE INDEX expense_categories_org_name_key ON expense_categories (organization_id, lower(name));
CREATE TRIGGER expense_categories_set_updated_at BEFORE UPDATE ON expense_categories FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- One budget line per project and category; a NULL category is the general line that covers
-- expenses whose category has no line of its own.
CREATE TABLE budgets (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id),
    project_id      uuid NOT NULL,
    category_id     uuid,
    currency        char(3) NOT NULL,
    amount          numeric(19, 4) NOT NULL,
    notes           text,
    created_by      uuid REFERENCES users (id),
    version         integer NOT NULL DEFAULT 1,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT budgets_project_fkey FOREIGN KEY (organization_id, project_id, currency) REFERENCES projects (organization_id, id, currency),
    CONSTRAINT budgets_category_fkey FOREIGN KEY (organization_id, category_id) REFERENCES expense_categories (organization_id, id),
    CONSTRAINT budgets_project_category_key UNIQUE NULLS NOT DISTINCT (organization_id, project_id, category_id),
    CONSTRAINT budgets_amount_positive CHECK (amount > 0)
);
CREATE TRIGGER budgets_set_updated_at BEFORE UPDATE ON budgets FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE invoice_number_sequences (
    organization_id uuid PRIMARY KEY REFERENCES organizations (id),
    prefix          text NOT NULL DEFAULT 'INV-',
    next_value      bigint NOT NULL DEFAULT 1,
    CONSTRAINT invoice_number_sequences_next_positive CHECK (next_value > 0)
);

CREATE TABLE invoices (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id),
    customer_id     uuid NOT NULL,
    project_id      uuid,
    number          text NOT NULL,
    status          text NOT NULL DEFAULT 'draft',
    currency        char(3) NOT NULL REFERENCES currencies (code),
    issue_date      date NOT NULL,
    due_date        date NOT NULL,
    discount_type   text NOT NULL DEFAULT 'none',
    discount_value  numeric(19, 4) NOT NULL DEFAULT 0,
    tax_rate        numeric(7, 4) NOT NULL DEFAULT 0,
    subtotal        numeric(19, 4) NOT NULL DEFAULT 0,
    discount_total  numeric(19, 4) NOT NULL DEFAULT 0,
    tax_total       numeric(19, 4) NOT NULL DEFAULT 0,
    total           numeric(19, 4) NOT NULL DEFAULT 0,
    amount_paid     numeric(19, 4) NOT NULL DEFAULT 0,
    notes           text,
    sent_at         timestamptz,
    voided_at       timestamptz,
    void_reason     text,
    source          text NOT NULL DEFAULT 'internal',
    external_id     text,
    created_by      uuid REFERENCES users (id),
    version         integer NOT NULL DEFAULT 1,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT invoices_org_id_key UNIQUE (organization_id, id),
    CONSTRAINT invoices_number_key UNIQUE (organization_id, number),
    CONSTRAINT invoices_external_key UNIQUE (organization_id, source, external_id),
    CONSTRAINT invoices_customer_fkey FOREIGN KEY (organization_id, customer_id) REFERENCES customers (organization_id, id),
    CONSTRAINT invoices_project_fkey FOREIGN KEY (organization_id, project_id, currency) REFERENCES projects (organization_id, id, currency),
    CONSTRAINT invoices_status_check CHECK (status IN ('draft', 'sent', 'partially_paid', 'paid', 'void')),
    CONSTRAINT invoices_source_check CHECK (source IN ('internal', 'quickbooks')),
    CONSTRAINT invoices_dates_check CHECK (due_date >= issue_date),
    CONSTRAINT invoices_discount_type_check CHECK (discount_type IN ('none', 'percent', 'fixed')),
    CONSTRAINT invoices_discount_value_check CHECK (discount_value >= 0 AND (discount_type <> 'percent' OR discount_value <= 100)
        AND (discount_type <> 'none' OR discount_value = 0)),
    CONSTRAINT invoices_tax_rate_check CHECK (tax_rate >= 0 AND tax_rate <= 100),
    CONSTRAINT invoices_amounts_non_negative CHECK (subtotal >= 0 AND discount_total >= 0 AND tax_total >= 0 AND total >= 0),
    CONSTRAINT invoices_discount_within_subtotal CHECK (discount_total <= subtotal),
    CONSTRAINT invoices_total_formula CHECK (total = subtotal - discount_total + tax_total),
    CONSTRAINT invoices_amount_paid_range CHECK (amount_paid >= 0 AND amount_paid <= total),
    -- Status must agree with what has been paid.
    CONSTRAINT invoices_status_matches_payments CHECK (
        (status IN ('draft', 'sent', 'void') AND amount_paid = 0)
        OR (status = 'partially_paid' AND amount_paid > 0 AND amount_paid < total)
        OR (status = 'paid' AND amount_paid = total AND total > 0)
    ),
    CONSTRAINT invoices_void_reason_required CHECK (status <> 'void' OR (voided_at IS NOT NULL AND void_reason IS NOT NULL)),
    CONSTRAINT invoices_number_length CHECK (length(number) BETWEEN 1 AND 40)
);
CREATE INDEX invoices_org_status_due_idx ON invoices (organization_id, status, due_date);
CREATE INDEX invoices_org_customer_idx ON invoices (organization_id, customer_id);
CREATE INDEX invoices_org_project_idx ON invoices (organization_id, project_id) WHERE project_id IS NOT NULL;
CREATE INDEX invoices_org_issue_date_idx ON invoices (organization_id, issue_date);
CREATE TRIGGER invoices_set_updated_at BEFORE UPDATE ON invoices FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Once money has been applied (or the invoice voided) its commercial terms are frozen.
CREATE FUNCTION invoices_guard_history() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status = 'void' THEN
        RAISE EXCEPTION 'void invoice % cannot be modified', OLD.number USING ERRCODE = 'check_violation';
    END IF;
    IF (OLD.amount_paid > 0 OR OLD.status IN ('partially_paid', 'paid')) AND (
        NEW.customer_id IS DISTINCT FROM OLD.customer_id OR NEW.currency IS DISTINCT FROM OLD.currency
        OR NEW.number IS DISTINCT FROM OLD.number OR NEW.issue_date IS DISTINCT FROM OLD.issue_date
        OR NEW.subtotal IS DISTINCT FROM OLD.subtotal OR NEW.discount_total IS DISTINCT FROM OLD.discount_total
        OR NEW.tax_total IS DISTINCT FROM OLD.tax_total OR NEW.total IS DISTINCT FROM OLD.total
        OR NEW.discount_type IS DISTINCT FROM OLD.discount_type OR NEW.discount_value IS DISTINCT FROM OLD.discount_value
        OR NEW.tax_rate IS DISTINCT FROM OLD.tax_rate
    ) THEN
        RAISE EXCEPTION 'invoice % has payments; its amounts and terms cannot change', OLD.number USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER invoices_history_guard BEFORE UPDATE ON invoices FOR EACH ROW EXECUTE FUNCTION invoices_guard_history();

CREATE FUNCTION invoices_reject_delete() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'invoices cannot be deleted; void them instead' USING ERRCODE = 'check_violation';
END
$$;
CREATE TRIGGER invoices_no_delete BEFORE DELETE ON invoices FOR EACH ROW EXECUTE FUNCTION invoices_reject_delete();

CREATE TABLE invoice_items (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id),
    invoice_id      uuid NOT NULL,
    position        integer NOT NULL,
    description     text NOT NULL,
    quantity        numeric(12, 4) NOT NULL,
    unit_price      numeric(19, 4) NOT NULL,
    line_total      numeric(19, 4) NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT invoice_items_invoice_fkey FOREIGN KEY (organization_id, invoice_id) REFERENCES invoices (organization_id, id),
    CONSTRAINT invoice_items_position_key UNIQUE (invoice_id, position),
    CONSTRAINT invoice_items_description_length CHECK (length(btrim(description)) BETWEEN 1 AND 500),
    CONSTRAINT invoice_items_quantity_positive CHECK (quantity > 0),
    CONSTRAINT invoice_items_unit_price_non_negative CHECK (unit_price >= 0),
    CONSTRAINT invoice_items_line_total_non_negative CHECK (line_total >= 0)
);

CREATE FUNCTION invoice_items_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    inv record;
BEGIN
    SELECT status, amount_paid, number INTO inv FROM invoices WHERE id = COALESCE(NEW.invoice_id, OLD.invoice_id);
    IF inv.status NOT IN ('draft', 'sent') OR inv.amount_paid > 0 THEN
        RAISE EXCEPTION 'line items of invoice % are locked', inv.number USING ERRCODE = 'check_violation';
    END IF;
    RETURN COALESCE(NEW, OLD);
END
$$;
CREATE TRIGGER invoice_items_lock BEFORE INSERT OR UPDATE OR DELETE ON invoice_items FOR EACH ROW EXECUTE FUNCTION invoice_items_guard();

-- Payments are never edited or deleted. A mistaken payment is reversed, which keeps both events.
CREATE TABLE payments (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     uuid NOT NULL REFERENCES organizations (id),
    invoice_id          uuid NOT NULL,
    amount              numeric(19, 4) NOT NULL,
    currency            char(3) NOT NULL REFERENCES currencies (code),
    paid_on             date NOT NULL,
    method              text NOT NULL,
    reference           text,
    status              text NOT NULL DEFAULT 'completed',
    idempotency_key     text NOT NULL,
    request_fingerprint bytea NOT NULL,
    recorded_by         uuid REFERENCES users (id),
    reversed_at         timestamptz,
    reversed_by         uuid REFERENCES users (id),
    reversal_reason     text,
    source              text NOT NULL DEFAULT 'internal',
    external_id         text,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT payments_org_id_key UNIQUE (organization_id, id),
    CONSTRAINT payments_idempotency_key UNIQUE (organization_id, idempotency_key),
    CONSTRAINT payments_external_key UNIQUE (organization_id, source, external_id),
    CONSTRAINT payments_invoice_fkey FOREIGN KEY (organization_id, invoice_id) REFERENCES invoices (organization_id, id),
    CONSTRAINT payments_amount_positive CHECK (amount > 0),
    CONSTRAINT payments_method_check CHECK (method IN ('bank_transfer', 'card', 'cash', 'check', 'other')),
    CONSTRAINT payments_status_check CHECK (status IN ('completed', 'reversed')),
    CONSTRAINT payments_source_check CHECK (source IN ('internal', 'quickbooks')),
    CONSTRAINT payments_reversal_fields CHECK (
        (status = 'completed' AND reversed_at IS NULL AND reversal_reason IS NULL)
        OR (status = 'reversed' AND reversed_at IS NOT NULL AND reversal_reason IS NOT NULL)
    ),
    CONSTRAINT payments_idempotency_key_length CHECK (length(idempotency_key) BETWEEN 8 AND 100)
);
CREATE INDEX payments_org_invoice_idx ON payments (organization_id, invoice_id);
CREATE INDEX payments_org_paid_on_idx ON payments (organization_id, paid_on);

CREATE FUNCTION payments_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'payments cannot be deleted; reverse them instead' USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.amount IS DISTINCT FROM OLD.amount OR NEW.currency IS DISTINCT FROM OLD.currency
        OR NEW.invoice_id IS DISTINCT FROM OLD.invoice_id OR NEW.paid_on IS DISTINCT FROM OLD.paid_on
        OR NEW.organization_id IS DISTINCT FROM OLD.organization_id OR NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key
        OR NEW.method IS DISTINCT FROM OLD.method OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'recorded payments are immutable' USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.status = 'reversed' THEN
        RAISE EXCEPTION 'a reversed payment cannot change again' USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER payments_immutable BEFORE UPDATE OR DELETE ON payments FOR EACH ROW EXECUTE FUNCTION payments_guard();

CREATE TABLE expenses (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id),
    category_id     uuid NOT NULL,
    project_id      uuid,
    vendor_name     text NOT NULL,
    expense_date    date NOT NULL,
    amount          numeric(19, 4) NOT NULL,
    currency        char(3) NOT NULL REFERENCES currencies (code),
    description     text,
    approval_status text NOT NULL DEFAULT 'pending',
    submitted_by    uuid REFERENCES users (id),
    reviewed_by     uuid REFERENCES users (id),
    reviewed_at     timestamptz,
    review_note     text,
    version         integer NOT NULL DEFAULT 1,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT expenses_org_id_key UNIQUE (organization_id, id),
    CONSTRAINT expenses_category_fkey FOREIGN KEY (organization_id, category_id) REFERENCES expense_categories (organization_id, id),
    CONSTRAINT expenses_project_fkey FOREIGN KEY (organization_id, project_id, currency) REFERENCES projects (organization_id, id, currency),
    CONSTRAINT expenses_amount_positive CHECK (amount > 0),
    CONSTRAINT expenses_vendor_length CHECK (length(btrim(vendor_name)) BETWEEN 1 AND 200),
    CONSTRAINT expenses_status_check CHECK (approval_status IN ('pending', 'approved', 'rejected')),
    CONSTRAINT expenses_review_fields CHECK (
        (approval_status = 'pending' AND reviewed_at IS NULL AND reviewed_by IS NULL)
        OR (approval_status <> 'pending' AND reviewed_at IS NOT NULL AND reviewed_by IS NOT NULL)
    )
);
CREATE INDEX expenses_org_date_idx ON expenses (organization_id, expense_date);
CREATE INDEX expenses_org_project_idx ON expenses (organization_id, project_id) WHERE project_id IS NOT NULL;
CREATE INDEX expenses_org_category_idx ON expenses (organization_id, category_id);
CREATE INDEX expenses_org_status_idx ON expenses (organization_id, approval_status);
CREATE TRIGGER expenses_set_updated_at BEFORE UPDATE ON expenses FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE FUNCTION expenses_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF OLD.approval_status = 'approved' THEN
            RAISE EXCEPTION 'approved expenses cannot be deleted' USING ERRCODE = 'check_violation';
        END IF;
        RETURN OLD;
    END IF;
    IF OLD.approval_status <> 'pending' AND NEW.approval_status IS DISTINCT FROM OLD.approval_status THEN
        RAISE EXCEPTION 'a reviewed expense cannot change status' USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.approval_status = 'approved' AND (
        NEW.amount IS DISTINCT FROM OLD.amount OR NEW.currency IS DISTINCT FROM OLD.currency
        OR NEW.expense_date IS DISTINCT FROM OLD.expense_date OR NEW.category_id IS DISTINCT FROM OLD.category_id
        OR NEW.project_id IS DISTINCT FROM OLD.project_id
    ) THEN
        RAISE EXCEPTION 'approved expenses are locked' USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER expenses_lock BEFORE UPDATE OR DELETE ON expenses FOR EACH ROW EXECUTE FUNCTION expenses_guard();

CREATE TABLE accounts (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id),
    code            text NOT NULL,
    name            text NOT NULL,
    type            text NOT NULL,
    is_system       boolean NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT accounts_org_id_key UNIQUE (organization_id, id),
    CONSTRAINT accounts_code_key UNIQUE (organization_id, code),
    CONSTRAINT accounts_type_check CHECK (type IN ('asset', 'liability', 'equity', 'revenue', 'expense'))
);

-- Cash ledger: one immutable row per cash movement, keyed by its source event.
CREATE TABLE transactions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id),
    account_id      uuid NOT NULL,
    occurred_on     date NOT NULL,
    direction       text NOT NULL,
    amount          numeric(19, 4) NOT NULL,
    currency        char(3) NOT NULL REFERENCES currencies (code),
    description     text NOT NULL,
    source_type     text NOT NULL,
    source_id       uuid NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT transactions_account_fkey FOREIGN KEY (organization_id, account_id) REFERENCES accounts (organization_id, id),
    CONSTRAINT transactions_source_key UNIQUE (organization_id, source_type, source_id),
    CONSTRAINT transactions_direction_check CHECK (direction IN ('in', 'out')),
    CONSTRAINT transactions_amount_positive CHECK (amount > 0),
    CONSTRAINT transactions_source_type_check CHECK (source_type IN ('payment', 'payment_reversal', 'expense'))
);
CREATE INDEX transactions_org_date_idx ON transactions (organization_id, occurred_on DESC, created_at DESC);

CREATE FUNCTION transactions_reject_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'ledger transactions are immutable' USING ERRCODE = 'check_violation';
END
$$;
CREATE TRIGGER transactions_immutable BEFORE UPDATE OR DELETE ON transactions FOR EACH ROW EXECUTE FUNCTION transactions_reject_change();

-- Row-level security: identical tenant policy on every table.
DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['customers', 'projects', 'expense_categories', 'budgets', 'invoice_number_sequences',
        'invoices', 'invoice_items', 'payments', 'expenses', 'accounts', 'transactions']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('CREATE POLICY %I ON %I USING (organization_id = app_current_org()) WITH CHECK (organization_id = app_current_org())',
            t || '_tenant', t);
    END LOOP;
END
$$;

REVOKE DELETE ON invoices, payments, transactions, accounts FROM finops_app;
REVOKE UPDATE ON transactions FROM finops_app;
