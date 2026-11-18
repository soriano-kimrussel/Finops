-- Step 4 of 4 (contract). Deploy only after every running instance reads vendors through
-- vendor_id. Dropping a column is metadata-only; the space is reclaimed by later rewrites.
ALTER TABLE expenses DROP COLUMN vendor_name;
