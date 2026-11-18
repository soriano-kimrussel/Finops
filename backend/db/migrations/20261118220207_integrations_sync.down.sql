DROP FUNCTION IF EXISTS find_quickbooks_connection(text);
DROP FUNCTION IF EXISTS claim_sync_job(text, integer);
DROP TABLE IF EXISTS external_records;
DROP TABLE IF EXISTS sync_job_attempts;
DROP TABLE IF EXISTS sync_jobs;
DROP TABLE IF EXISTS oauth_states;
DROP TABLE IF EXISTS sync_connections;
