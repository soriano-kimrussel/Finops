CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- The runtime role (finops_app) never owns objects. It gets DML on whatever the owner creates.
GRANT USAGE ON SCHEMA public TO finops_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO finops_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO finops_app;

-- Tenant context is set per transaction with set_config(..., true) and read by RLS policies.
-- An unset or empty value yields NULL, which matches no rows.
CREATE FUNCTION app_current_org() RETURNS uuid
LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT nullif(current_setting('app.org_id', true), '')::uuid
$$;

CREATE FUNCTION app_current_user() RETURNS uuid
LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT nullif(current_setting('app.user_id', true), '')::uuid
$$;

CREATE FUNCTION set_updated_at() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END
$$;

CREATE TABLE currencies (
    code        char(3) PRIMARY KEY CHECK (code ~ '^[A-Z]{3}$'),
    name        text NOT NULL,
    minor_units smallint NOT NULL CHECK (minor_units BETWEEN 0 AND 4)
);

INSERT INTO currencies (code, name, minor_units) VALUES
    ('USD', 'US Dollar', 2),
    ('EUR', 'Euro', 2),
    ('GBP', 'Pound Sterling', 2),
    ('CAD', 'Canadian Dollar', 2),
    ('AUD', 'Australian Dollar', 2),
    ('NZD', 'New Zealand Dollar', 2),
    ('CHF', 'Swiss Franc', 2),
    ('SGD', 'Singapore Dollar', 2),
    ('INR', 'Indian Rupee', 2),
    ('JPY', 'Yen', 0),
    ('KWD', 'Kuwaiti Dinar', 3);

REVOKE INSERT, UPDATE, DELETE ON currencies FROM finops_app;
