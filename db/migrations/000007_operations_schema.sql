-- +goose Up
-- +goose StatementBegin
SET lock_timeout = '5s';
SET statement_timeout = '30s';
SET ROLE wde_owner;
GRANT USAGE ON SCHEMA wde TO wde_quota_executor, wde_replay_executor;

CREATE TABLE wde.audit_events (
    id uuid PRIMARY KEY,
    workspace_id uuid,
    actor_type text NOT NULL CHECK (actor_type IN ('api_key', 'admin_cli', 'system')),
    actor_id text NOT NULL CHECK (char_length(actor_id) BETWEEN 1 AND 128),
    action text NOT NULL CHECK (action IN ('credential.bootstrap', 'credential.revoke', 'endpoint.create', 'delivery.replay')),
    resource_type text NOT NULL CHECK (resource_type IN ('workspace', 'api_key', 'endpoint', 'delivery')),
    resource_id text NOT NULL CHECK (char_length(resource_id) BETWEEN 1 AND 128),
    request_id text NOT NULL CHECK (char_length(request_id) BETWEEN 1 AND 128),
    outcome text NOT NULL CHECK (outcome IN ('accepted', 'revoked')),
    reason text CHECK (reason IS NULL OR char_length(reason) BETWEEN 1 AND 500),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (workspace_id, id)
);

CREATE INDEX audit_events_workspace_timeline_idx
    ON wde.audit_events (workspace_id, created_at DESC, id DESC);
CREATE INDEX audit_events_retention_idx ON wde.audit_events (created_at, id);

CREATE TABLE wde.replay_commands (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    delivery_id uuid NOT NULL,
    actor_type text NOT NULL CHECK (actor_type = 'api_key'),
    actor_id text NOT NULL CHECK (char_length(actor_id) BETWEEN 1 AND 128),
    idempotency_key_hash bytea NOT NULL CHECK (octet_length(idempotency_key_hash) = 32),
    fingerprint bytea NOT NULL CHECK (octet_length(fingerprint) = 32),
    fingerprint_version smallint NOT NULL CHECK (fingerprint_version > 0),
    new_run_number integer NOT NULL CHECK (new_run_number >= 2),
    reason text NOT NULL CHECK (char_length(reason) BETWEEN 1 AND 500),
    request_id text NOT NULL CHECK (char_length(request_id) BETWEEN 1 AND 128),
    result text NOT NULL CHECK (result = 'accepted'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, idempotency_key_hash),
    FOREIGN KEY (workspace_id, delivery_id) REFERENCES wde.deliveries(workspace_id, id)
        ON UPDATE RESTRICT ON DELETE RESTRICT
);

CREATE INDEX replay_commands_delivery_timeline_idx
    ON wde.replay_commands (workspace_id, delivery_id, created_at DESC);

CREATE TABLE wde.rate_limit_buckets (
    dimension_hash bytea NOT NULL CHECK (octet_length(dimension_hash) = 32),
    dimension_type text NOT NULL CHECK (dimension_type IN ('global', 'workspace', 'api_key', 'delivery')),
    operation text NOT NULL CHECK (operation IN ('ingest', 'endpoint_write', 'query', 'replay')),
    window_started_at timestamptz NOT NULL,
    workspace_id uuid,
    api_key_id uuid,
    resource_id uuid,
    count integer NOT NULL CHECK (count > 0),
    expires_at timestamptz NOT NULL CHECK (expires_at > window_started_at),
    PRIMARY KEY (dimension_hash, operation, window_started_at),
    CHECK (
        (dimension_type = 'global' AND workspace_id IS NULL AND api_key_id IS NULL AND resource_id IS NULL)
        OR (dimension_type = 'workspace' AND workspace_id IS NOT NULL AND api_key_id IS NULL AND resource_id IS NULL)
        OR (dimension_type = 'api_key' AND workspace_id IS NOT NULL AND api_key_id IS NOT NULL AND resource_id IS NULL)
        OR (dimension_type = 'delivery' AND workspace_id IS NOT NULL AND api_key_id IS NULL AND resource_id IS NOT NULL)
    )
);

CREATE INDEX rate_limit_buckets_expiry_idx ON wde.rate_limit_buckets (expires_at);

ALTER TABLE wde.audit_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE wde.audit_events FORCE ROW LEVEL SECURITY;
ALTER TABLE wde.replay_commands ENABLE ROW LEVEL SECURITY;
ALTER TABLE wde.replay_commands FORCE ROW LEVEL SECURITY;
ALTER TABLE wde.rate_limit_buckets ENABLE ROW LEVEL SECURITY;
ALTER TABLE wde.rate_limit_buckets FORCE ROW LEVEL SECURITY;

CREATE POLICY audit_events_writer ON wde.audit_events
    FOR INSERT TO wde_audit_executor WITH CHECK (true);
CREATE POLICY replay_commands_executor ON wde.replay_commands
    TO wde_replay_executor USING (true) WITH CHECK (true);
CREATE POLICY replay_deliveries_executor ON wde.deliveries
    TO wde_replay_executor USING (true) WITH CHECK (true);
CREATE POLICY replay_events_executor ON wde.events
    FOR SELECT TO wde_replay_executor USING (true);
CREATE POLICY replay_commands_api ON wde.replay_commands
    FOR SELECT TO wde_api USING (workspace_id = wde.current_workspace_id());
CREATE POLICY rate_limit_executor ON wde.rate_limit_buckets
    TO wde_quota_executor USING (true) WITH CHECK (true);

GRANT INSERT ON wde.audit_events TO wde_audit_executor;
GRANT SELECT, INSERT ON wde.replay_commands TO wde_replay_executor;
GRANT SELECT, UPDATE ON wde.deliveries TO wde_replay_executor;
GRANT SELECT ON wde.events TO wde_replay_executor;
GRANT SELECT ON wde.replay_commands TO wde_api;
GRANT SELECT, INSERT, UPDATE, DELETE ON wde.rate_limit_buckets TO wde_quota_executor;

RESET ROLE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET lock_timeout = '5s';
SET statement_timeout = '30s';
SET ROLE wde_owner;
DROP POLICY rate_limit_executor ON wde.rate_limit_buckets;
DROP POLICY replay_commands_api ON wde.replay_commands;
DROP POLICY replay_events_executor ON wde.events;
DROP POLICY replay_deliveries_executor ON wde.deliveries;
DROP POLICY replay_commands_executor ON wde.replay_commands;
DROP POLICY audit_events_writer ON wde.audit_events;
DROP TABLE wde.rate_limit_buckets;
DROP TABLE wde.replay_commands;
DROP TABLE wde.audit_events;
REVOKE USAGE ON SCHEMA wde FROM wde_quota_executor, wde_replay_executor;
RESET ROLE;
-- +goose StatementEnd
