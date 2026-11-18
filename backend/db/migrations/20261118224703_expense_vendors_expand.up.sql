-- Step 1 of 4 (expand). Vendors become a table instead of free text on each expense.
-- Every statement here is metadata-only or touches an empty table, so it holds its
-- ACCESS EXCLUSIVE lock on expenses for milliseconds regardless of table size.

CREATE TABLE vendors (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id),
    name            text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT vendors_org_id_key UNIQUE (organization_id, id),
    CONSTRAINT vendors_name_length CHECK (length(btrim(name)) BETWEEN 1 AND 200)
);
CREATE UNIQUE INDEX vendors_org_name_key ON vendors (organization_id, lower(name));
CREATE TRIGGER vendors_set_updated_at BEFORE UPDATE ON vendors FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE vendors ENABLE ROW LEVEL SECURITY;
ALTER TABLE vendors FORCE ROW LEVEL SECURITY;
CREATE POLICY vendors_tenant ON vendors
    USING (organization_id = app_current_org()) WITH CHECK (organization_id = app_current_org());
REVOKE DELETE ON vendors FROM finops_app;

-- Nullable with no default: no table rewrite. Old code keeps writing vendor_name and ignores it.
ALTER TABLE expenses ADD COLUMN vendor_id uuid;

-- NOT VALID skips the scan of existing rows; new and updated rows are checked immediately.
ALTER TABLE expenses ADD CONSTRAINT expenses_vendor_fkey
    FOREIGN KEY (organization_id, vendor_id) REFERENCES vendors (organization_id, id) NOT VALID;
