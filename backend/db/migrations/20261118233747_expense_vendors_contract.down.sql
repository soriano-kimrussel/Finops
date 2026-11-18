-- Restores the column and repopulates it from vendors. Values are recoverable because every
-- expense still points at its vendor row; nothing was lost by the drop.
ALTER TABLE expenses ADD COLUMN vendor_name text;
UPDATE expenses e SET vendor_name = v.name FROM vendors v WHERE v.id = e.vendor_id;
ALTER TABLE expenses ALTER COLUMN vendor_name SET NOT NULL;
