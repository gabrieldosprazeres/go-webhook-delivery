-- +goose Up
-- +goose StatementBegin
SET lock_timeout = '5s';
SET statement_timeout = '30s';
SET ROLE wde_owner;
GRANT USAGE ON SCHEMA wde TO wde_maintenance_executor, wde_rotation_executor;

ALTER TABLE wde.endpoint_secret_versions ALTER COLUMN cipher_format_version DROP NOT NULL;
ALTER TABLE wde.endpoint_secret_versions DROP CONSTRAINT endpoint_secret_versions_state_check;
ALTER TABLE wde.endpoint_secret_versions ADD CONSTRAINT endpoint_secret_versions_state_check
    CHECK(state IN('active','retiring','purged'));
ALTER TABLE wde.endpoint_secret_versions DROP CONSTRAINT endpoint_secret_versions_check;
ALTER TABLE wde.endpoint_secret_versions ADD CONSTRAINT endpoint_secret_envelope_check CHECK (
    (state='active' AND retire_at IS NULL AND cipher_format_version IS NOT NULL
        AND cipher_format_version IN (1,2) AND secret_ciphertext IS NOT NULL
        AND secret_nonce IS NOT NULL AND kek_version IS NOT NULL
        AND octet_length(secret_ciphertext) >= 16 AND octet_length(secret_nonce) = 12
        AND kek_version > 0 AND purged_at IS NULL)
    OR (state='retiring' AND retire_at IS NOT NULL AND retire_at>valid_from
        AND cipher_format_version IS NOT NULL AND cipher_format_version IN(1,2)
        AND secret_ciphertext IS NOT NULL AND secret_nonce IS NOT NULL AND kek_version IS NOT NULL
        AND octet_length(secret_ciphertext)>=16 AND octet_length(secret_nonce)=12
        AND kek_version>0 AND purged_at IS NULL)
    OR (state='purged' AND purged_at>=valid_from AND (retire_at IS NULL OR purged_at>=retire_at)
        AND cipher_format_version IS NULL AND secret_ciphertext IS NULL
        AND secret_nonce IS NULL AND kek_version IS NULL AND purged_at IS NOT NULL)
);
ALTER TABLE wde.events ADD CONSTRAINT event_payload_format_check
    CHECK (payload_cipher_format_version IS NULL OR payload_cipher_format_version IN (1,2));
ALTER TABLE wde.endpoints ADD CONSTRAINT endpoint_target_format_check
    CHECK (target_cipher_format_version IN (1,2));
ALTER TABLE wde.rate_limit_buckets ADD CONSTRAINT rate_limit_bucket_max_retention_check
    CHECK(expires_at<=window_started_at+interval '7 days');

ALTER TABLE wde.audit_events DROP CONSTRAINT audit_events_action_check;
ALTER TABLE wde.audit_events ADD CHECK (action IN ('credential.bootstrap','credential.revoke',
    'endpoint.create','endpoint.secret_rotate','delivery.replay','payload.purge',
    'secret.purge','workspace.purge','restore.quarantine','restore.reconcile'));
ALTER TABLE wde.audit_events DROP CONSTRAINT audit_events_resource_type_check;
ALTER TABLE wde.audit_events ADD CHECK (resource_type IN ('workspace','api_key','endpoint','delivery','event','secret'));
ALTER TABLE wde.audit_events DROP CONSTRAINT audit_events_outcome_check;
ALTER TABLE wde.audit_events ADD CHECK (outcome IN ('accepted','revoked','completed'));

CREATE TABLE wde.secret_rotation_commands (
    id uuid PRIMARY KEY, workspace_id uuid NOT NULL, endpoint_id uuid NOT NULL,
    secret_version_id uuid NOT NULL, actor_id text NOT NULL CHECK (char_length(actor_id) BETWEEN 1 AND 128),
    idempotency_key_hash bytea NOT NULL CHECK (octet_length(idempotency_key_hash)=32),
    fingerprint bytea NOT NULL CHECK (octet_length(fingerprint)=32),
    key_id text NOT NULL CHECK (char_length(key_id) BETWEEN 8 AND 64),
    overlap_seconds integer NOT NULL CHECK (overlap_seconds BETWEEN 3600 AND 604800),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(workspace_id,id), UNIQUE(workspace_id,endpoint_id,idempotency_key_hash),
    FOREIGN KEY(workspace_id,endpoint_id) REFERENCES wde.endpoints(workspace_id,id) ON DELETE RESTRICT,
    FOREIGN KEY(workspace_id,secret_version_id) REFERENCES wde.endpoint_secret_versions(workspace_id,id) ON DELETE RESTRICT
);
CREATE UNIQUE INDEX endpoint_secret_one_retiring_idx ON wde.endpoint_secret_versions(workspace_id,endpoint_id)
    WHERE state='retiring';

CREATE TABLE wde.maintenance_jobs (
    id uuid PRIMARY KEY, workspace_id uuid, job_type text NOT NULL CHECK (job_type IN
        ('purge_payload','purge_workspace','reencrypt_payload','reencrypt_secret','reconcile_tombstone')),
    deduplication_key text NOT NULL CHECK (char_length(deduplication_key) BETWEEN 1 AND 160),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','processing','completed','failed')),
    checkpoint text NOT NULL DEFAULT 'replay_commands' CHECK (checkpoint IN ('replay_commands',
        'rotation_commands','attempts','deliveries','events','subscriptions','runtime','secrets',
        'endpoints','scopes','keys','buckets','workspace','complete')),
    scheduled_at timestamptz NOT NULL DEFAULT clock_timestamp(), lease_owner uuid, lease_expires_at timestamptz,
    fencing_token bigint NOT NULL DEFAULT 0 CHECK (fencing_token>=0), attempts integer NOT NULL DEFAULT 0,
    processed_rows bigint NOT NULL DEFAULT 0 CHECK(processed_rows>=0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(), updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(job_type,deduplication_key), CHECK ((status='processing')=(lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL))
);
CREATE INDEX maintenance_jobs_ready_idx ON wde.maintenance_jobs(status,scheduled_at,id);
CREATE INDEX delivery_attempts_retention_idx ON wde.delivery_attempts(finished_at,id) WHERE state<>'started';
CREATE INDEX deliveries_retention_idx ON wde.deliveries(terminal_at,id) WHERE terminal_at IS NOT NULL;
CREATE INDEX events_retention_idx ON wde.events(created_at,id) WHERE payload_ciphertext IS NULL;
CREATE INDEX replay_commands_retention_idx ON wde.replay_commands(created_at,id);
CREATE INDEX endpoint_secrets_retention_idx ON wde.endpoint_secret_versions(retire_at,id) WHERE state='retiring';

CREATE TABLE wde.workspace_tombstones (
    workspace_id uuid PRIMARY KEY, generation bigint NOT NULL CHECK(generation>0),
    deleted_at timestamptz NOT NULL DEFAULT clock_timestamp(), purge_completed_at timestamptz
);
CREATE TABLE wde.restore_control (
    singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton), state text NOT NULL CHECK(state IN('ready','quarantined')),
    generation bigint NOT NULL DEFAULT 0 CHECK(generation>=0),
    audit_retention_days smallint NOT NULL DEFAULT 365 CHECK(audit_retention_days BETWEEN 365 AND 730),
    quarantined_at timestamptz, reconciled_at timestamptz
);
INSERT INTO wde.restore_control(singleton,state) VALUES(true,'ready');

ALTER TABLE wde.secret_rotation_commands ENABLE ROW LEVEL SECURITY;
ALTER TABLE wde.secret_rotation_commands FORCE ROW LEVEL SECURITY;
ALTER TABLE wde.maintenance_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE wde.maintenance_jobs FORCE ROW LEVEL SECURITY;
CREATE POLICY rotations_executor ON wde.secret_rotation_commands TO wde_rotation_executor USING(true) WITH CHECK(true);
CREATE POLICY rotation_endpoints_executor ON wde.endpoints FOR SELECT TO wde_rotation_executor USING(true);
CREATE POLICY rotation_secrets_executor ON wde.endpoint_secret_versions TO wde_rotation_executor USING(true) WITH CHECK(true);
CREATE POLICY maintenance_executor ON wde.maintenance_jobs TO wde_maintenance_executor USING(true) WITH CHECK(true);
CREATE POLICY maintenance_workspaces ON wde.workspaces TO wde_maintenance_executor USING(true) WITH CHECK(true);
CREATE POLICY maintenance_keys ON wde.api_keys TO wde_maintenance_executor USING(true) WITH CHECK(true);
CREATE POLICY maintenance_scopes ON wde.api_key_scopes TO wde_maintenance_executor USING(true) WITH CHECK(true);
CREATE POLICY maintenance_endpoints ON wde.endpoints TO wde_maintenance_executor USING(true) WITH CHECK(true);
CREATE POLICY maintenance_subscriptions ON wde.endpoint_subscriptions TO wde_maintenance_executor USING(true) WITH CHECK(true);
CREATE POLICY maintenance_runtime ON wde.endpoint_runtime TO wde_maintenance_executor USING(true) WITH CHECK(true);
CREATE POLICY maintenance_secrets ON wde.endpoint_secret_versions TO wde_maintenance_executor USING(true) WITH CHECK(true);
CREATE POLICY maintenance_events ON wde.events TO wde_maintenance_executor USING(true) WITH CHECK(true);
CREATE POLICY maintenance_deliveries ON wde.deliveries TO wde_maintenance_executor USING(true) WITH CHECK(true);
CREATE POLICY maintenance_attempts ON wde.delivery_attempts TO wde_maintenance_executor USING(true) WITH CHECK(true);
CREATE POLICY maintenance_replays ON wde.replay_commands TO wde_maintenance_executor USING(true) WITH CHECK(true);
CREATE POLICY maintenance_rotations ON wde.secret_rotation_commands TO wde_maintenance_executor USING(true) WITH CHECK(true);
CREATE POLICY maintenance_buckets ON wde.rate_limit_buckets TO wde_maintenance_executor USING(true) WITH CHECK(true);
CREATE POLICY maintenance_audit ON wde.audit_events TO wde_maintenance_executor USING(true) WITH CHECK(true);
GRANT SELECT,INSERT ON wde.secret_rotation_commands TO wde_rotation_executor;
GRANT SELECT ON wde.endpoints TO wde_rotation_executor;
GRANT SELECT,INSERT,UPDATE ON wde.endpoint_secret_versions TO wde_rotation_executor;
GRANT SELECT,INSERT,UPDATE,DELETE ON wde.maintenance_jobs TO wde_maintenance_executor;
GRANT SELECT,UPDATE,DELETE ON wde.workspaces,wde.api_keys,wde.api_key_scopes,wde.endpoints,
    wde.endpoint_subscriptions,wde.endpoint_runtime,wde.endpoint_secret_versions,wde.events,
    wde.deliveries,wde.delivery_attempts,wde.replay_commands,wde.rate_limit_buckets TO wde_maintenance_executor;
GRANT SELECT,UPDATE,DELETE ON wde.secret_rotation_commands TO wde_maintenance_executor;
GRANT SELECT,INSERT,UPDATE ON wde.workspace_tombstones,wde.restore_control TO wde_maintenance_executor;
GRANT SELECT,INSERT,UPDATE,DELETE ON wde.audit_events TO wde_maintenance_executor;
RESET ROLE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET ROLE wde_owner;
DROP POLICY rotation_secrets_executor ON wde.endpoint_secret_versions;
DROP POLICY rotation_endpoints_executor ON wde.endpoints;
DROP POLICY maintenance_workspaces ON wde.workspaces;
DROP POLICY maintenance_keys ON wde.api_keys;
DROP POLICY maintenance_scopes ON wde.api_key_scopes;
DROP POLICY maintenance_endpoints ON wde.endpoints;
DROP POLICY maintenance_subscriptions ON wde.endpoint_subscriptions;
DROP POLICY maintenance_runtime ON wde.endpoint_runtime;
DROP POLICY maintenance_secrets ON wde.endpoint_secret_versions;
DROP POLICY maintenance_events ON wde.events;
DROP POLICY maintenance_deliveries ON wde.deliveries;
DROP POLICY maintenance_attempts ON wde.delivery_attempts;
DROP POLICY maintenance_replays ON wde.replay_commands;
DROP POLICY maintenance_buckets ON wde.rate_limit_buckets;
DROP POLICY maintenance_audit ON wde.audit_events;
DROP TABLE wde.restore_control;
DROP TABLE wde.workspace_tombstones;
DROP TABLE wde.maintenance_jobs;
DROP INDEX wde.endpoint_secret_one_retiring_idx;
DROP TABLE wde.secret_rotation_commands;
DROP INDEX wde.endpoint_secrets_retention_idx;
DROP INDEX wde.replay_commands_retention_idx;
DROP INDEX wde.events_retention_idx;
DROP INDEX wde.deliveries_retention_idx;
DROP INDEX wde.delivery_attempts_retention_idx;
ALTER TABLE wde.audit_events DROP CONSTRAINT audit_events_outcome_check;
ALTER TABLE wde.audit_events ADD CHECK (outcome IN ('accepted','revoked'));
ALTER TABLE wde.audit_events DROP CONSTRAINT audit_events_resource_type_check;
ALTER TABLE wde.audit_events ADD CHECK (resource_type IN ('workspace','api_key','endpoint','delivery'));
ALTER TABLE wde.audit_events DROP CONSTRAINT audit_events_action_check;
ALTER TABLE wde.audit_events ADD CHECK (action IN ('credential.bootstrap','credential.revoke','endpoint.create','delivery.replay'));
ALTER TABLE wde.events DROP CONSTRAINT event_payload_format_check;
ALTER TABLE wde.endpoints DROP CONSTRAINT endpoint_target_format_check;
ALTER TABLE wde.rate_limit_buckets DROP CONSTRAINT rate_limit_bucket_max_retention_check;
ALTER TABLE wde.endpoint_secret_versions DROP CONSTRAINT endpoint_secret_envelope_check;
ALTER TABLE wde.endpoint_secret_versions ADD CHECK ((state <> 'purged' AND secret_ciphertext IS NOT NULL
    AND octet_length(secret_ciphertext)>=16 AND octet_length(secret_nonce)=12 AND kek_version>0 AND purged_at IS NULL)
    OR (state='purged' AND secret_ciphertext IS NULL AND secret_nonce IS NULL AND kek_version IS NULL AND purged_at IS NOT NULL));
ALTER TABLE wde.endpoint_secret_versions ALTER COLUMN cipher_format_version SET NOT NULL;
ALTER TABLE wde.endpoint_secret_versions DROP CONSTRAINT endpoint_secret_versions_state_check;
ALTER TABLE wde.endpoint_secret_versions ADD CONSTRAINT endpoint_secret_versions_state_check
    CHECK(state IN('active','retiring','retired','purged'));
REVOKE USAGE ON SCHEMA wde FROM wde_maintenance_executor, wde_rotation_executor;
RESET ROLE;
-- +goose StatementEnd
