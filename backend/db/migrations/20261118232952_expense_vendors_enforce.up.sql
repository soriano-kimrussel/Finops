-- Step 3 of 4 (enforce). Run only after `finops backfill vendors` reports zero remaining rows.
-- Refuse to continue otherwise, rather than failing halfway through a long validation.
DO $$
DECLARE
    remaining bigint;
BEGIN
    SELECT count(*) INTO remaining FROM expenses WHERE vendor_id IS NULL;
    IF remaining > 0 THEN
        RAISE EXCEPTION '% expenses have no vendor_id; run "finops backfill vendors" before this migration', remaining;
    END IF;
END
$$;

-- VALIDATE scans the table under SHARE UPDATE EXCLUSIVE: reads and writes continue meanwhile.
ALTER TABLE expenses VALIDATE CONSTRAINT expenses_vendor_fkey;

-- SET NOT NULL normally scans the whole table under ACCESS EXCLUSIVE. A validated CHECK
-- (vendor_id IS NOT NULL) lets PostgreSQL 12+ skip that scan, so the exclusive lock is brief.
ALTER TABLE expenses ADD CONSTRAINT expenses_vendor_id_present CHECK (vendor_id IS NOT NULL) NOT VALID;
ALTER TABLE expenses VALIDATE CONSTRAINT expenses_vendor_id_present;
ALTER TABLE expenses ALTER COLUMN vendor_id SET NOT NULL;
ALTER TABLE expenses DROP CONSTRAINT expenses_vendor_id_present;
