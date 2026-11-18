CREATE INDEX CONCURRENTLY IF NOT EXISTS expenses_org_vendor_idx ON expenses (organization_id, vendor_id);
