CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email         citext NOT NULL,
    name          text NOT NULL,
    password_hash text NOT NULL,
    is_active     boolean NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_email_key UNIQUE (email),
    CONSTRAINT users_email_format CHECK (length(email) <= 254 AND email ~ '^[^@\s]+@[^@\s]+\.[^@\s]+$'),
    CONSTRAINT users_name_length CHECK (length(btrim(name)) BETWEEN 1 AND 200)
);
CREATE TRIGGER users_set_updated_at BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Only a SHA-256 hash of the session token is stored; the raw token lives in the cookie.
CREATE TABLE sessions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash   bytea NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    ip           text,
    user_agent   text,
    CONSTRAINT sessions_token_hash_key UNIQUE (token_hash),
    CONSTRAINT sessions_expiry_after_creation CHECK (expires_at > created_at)
);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

CREATE TABLE organizations (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name          text NOT NULL,
    base_currency char(3) NOT NULL REFERENCES currencies (code),
    timezone      text NOT NULL DEFAULT 'UTC',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT organizations_name_length CHECK (length(btrim(name)) BETWEEN 1 AND 200),
    CONSTRAINT organizations_timezone_length CHECK (length(timezone) BETWEEN 1 AND 64)
);
CREATE TRIGGER organizations_set_updated_at BEFORE UPDATE ON organizations FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE memberships (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    user_id         uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role            text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT memberships_org_user_key UNIQUE (organization_id, user_id),
    CONSTRAINT memberships_role_check CHECK (role IN ('owner', 'admin', 'accountant', 'project_manager', 'viewer'))
);
CREATE INDEX memberships_user_id_idx ON memberships (user_id);
CREATE TRIGGER memberships_set_updated_at BEFORE UPDATE ON memberships FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE audit_logs (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id),
    actor_user_id   uuid REFERENCES users (id),
    actor_type      text NOT NULL,
    action          text NOT NULL,
    entity_type     text NOT NULL,
    entity_id       uuid,
    changes         jsonb NOT NULL DEFAULT '{}'::jsonb,
    request_id      text,
    ip              text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT audit_logs_actor_type_check CHECK (actor_type IN ('user', 'system')),
    CONSTRAINT audit_logs_actor_present CHECK (actor_type = 'system' OR actor_user_id IS NOT NULL),
    CONSTRAINT audit_logs_action_format CHECK (action ~ '^[a-z_]+\.[a-z_]+$')
);
CREATE INDEX audit_logs_org_created_idx ON audit_logs (organization_id, created_at DESC, id DESC);
CREATE INDEX audit_logs_entity_idx ON audit_logs (organization_id, entity_type, entity_id);

-- Audit history is append-only for everyone, including the schema owner.
CREATE FUNCTION audit_logs_reject_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_logs is append-only' USING ERRCODE = 'insufficient_privilege';
END
$$;
CREATE TRIGGER audit_logs_no_update_delete BEFORE UPDATE OR DELETE ON audit_logs
    FOR EACH ROW EXECUTE FUNCTION audit_logs_reject_change();
CREATE TRIGGER audit_logs_no_truncate BEFORE TRUNCATE ON audit_logs
    FOR EACH STATEMENT EXECUTE FUNCTION audit_logs_reject_change();
REVOKE UPDATE, DELETE, TRUNCATE ON audit_logs FROM finops_app;

-- Row-level security. users and sessions are global identity tables and are not tenant-scoped.
ALTER TABLE organizations ENABLE ROW LEVEL SECURITY;
ALTER TABLE organizations FORCE ROW LEVEL SECURITY;
CREATE POLICY organizations_read ON organizations FOR SELECT
    USING (id = app_current_org()
        OR EXISTS (SELECT 1 FROM memberships m WHERE m.organization_id = organizations.id AND m.user_id = app_current_user()));
CREATE POLICY organizations_insert ON organizations FOR INSERT WITH CHECK (id = app_current_org());
CREATE POLICY organizations_update ON organizations FOR UPDATE
    USING (id = app_current_org()) WITH CHECK (id = app_current_org());

ALTER TABLE memberships ENABLE ROW LEVEL SECURITY;
ALTER TABLE memberships FORCE ROW LEVEL SECURITY;
CREATE POLICY memberships_tenant ON memberships
    USING (organization_id = app_current_org()) WITH CHECK (organization_id = app_current_org());
CREATE POLICY memberships_own_read ON memberships FOR SELECT USING (user_id = app_current_user());

ALTER TABLE audit_logs ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_logs FORCE ROW LEVEL SECURITY;
CREATE POLICY audit_logs_read ON audit_logs FOR SELECT USING (organization_id = app_current_org());
CREATE POLICY audit_logs_insert ON audit_logs FOR INSERT WITH CHECK (organization_id = app_current_org());
