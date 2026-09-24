-- +goose Up
-- +goose StatementBegin
SET lock_timeout = '5s';
SET statement_timeout = '30s';
SET ROLE wde_owner;

GRANT USAGE, CREATE ON SCHEMA wde TO wde_auth_executor, wde_worker_executor, wde_audit_executor;
GRANT EXECUTE ON FUNCTION wde.schema_version() TO wde_admin;

CREATE FUNCTION wde.current_workspace_id()
RETURNS uuid
LANGUAGE sql
STABLE
SET search_path = pg_catalog
AS 'SELECT NULLIF(current_setting(''wde.workspace_id'', true), '''')::uuid';

REVOKE ALL ON FUNCTION wde.current_workspace_id() FROM PUBLIC;

CREATE TABLE wde.workspaces (
    id uuid PRIMARY KEY,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'deleting')),
    payload_retention_days smallint NOT NULL DEFAULT 30 CHECK (payload_retention_days BETWEEN 1 AND 90),
    metadata_retention_days smallint NOT NULL DEFAULT 90 CHECK (metadata_retention_days BETWEEN 30 AND 180),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    deletion_requested_at timestamptz,
    UNIQUE (id, status)
);

CREATE TABLE wde.api_keys (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    prefix text NOT NULL UNIQUE CHECK (char_length(prefix) BETWEEN 8 AND 32),
    verifier bytea NOT NULL CHECK (octet_length(verifier) = 32),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked')),
    expires_at timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    last_used_at timestamptz,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES wde.workspaces(id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    CHECK ((status = 'active' AND revoked_at IS NULL) OR (status = 'revoked' AND revoked_at IS NOT NULL))
);

CREATE TABLE wde.api_key_scopes (
    workspace_id uuid NOT NULL,
    api_key_id uuid NOT NULL,
    scope text NOT NULL CHECK (scope IN ('events:write', 'endpoints:write', 'deliveries:read', 'deliveries:retry', 'admin')),
    PRIMARY KEY (workspace_id, api_key_id, scope),
    FOREIGN KEY (workspace_id, api_key_id) REFERENCES wde.api_keys(workspace_id, id) ON UPDATE RESTRICT ON DELETE RESTRICT
);

CREATE TABLE wde.endpoints (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'disabled', 'deleting')),
    scheme text NOT NULL CHECK (scheme IN ('http', 'https')),
    host_ascii text NOT NULL CHECK (char_length(host_ascii) BETWEEN 1 AND 253),
    port integer NOT NULL CHECK (port BETWEEN 1 AND 65535),
    target_cipher_format_version smallint NOT NULL CHECK (target_cipher_format_version > 0),
    target_ciphertext bytea NOT NULL CHECK (octet_length(target_ciphertext) >= 16),
    target_nonce bytea NOT NULL CHECK (octet_length(target_nonce) = 12),
    target_kek_version smallint NOT NULL CHECK (target_kek_version > 0),
    max_concurrency smallint NOT NULL DEFAULT 4 CHECK (max_concurrency BETWEEN 1 AND 32),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES wde.workspaces(id) ON UPDATE RESTRICT ON DELETE RESTRICT
);

CREATE TABLE wde.endpoint_subscriptions (
    workspace_id uuid NOT NULL,
    endpoint_id uuid NOT NULL,
    event_type text NOT NULL CHECK (char_length(event_type) BETWEEN 1 AND 128 AND event_type ~ '^[A-Za-z0-9][A-Za-z0-9._:-]*$'),
    PRIMARY KEY (workspace_id, endpoint_id, event_type),
    FOREIGN KEY (workspace_id, endpoint_id) REFERENCES wde.endpoints(workspace_id, id) ON UPDATE RESTRICT ON DELETE RESTRICT
);

CREATE INDEX endpoint_subscriptions_event_idx
    ON wde.endpoint_subscriptions (workspace_id, event_type, endpoint_id);

CREATE TABLE wde.endpoint_runtime (
    workspace_id uuid NOT NULL,
    endpoint_id uuid NOT NULL,
    lock_version bigint NOT NULL DEFAULT 0 CHECK (lock_version >= 0),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (workspace_id, endpoint_id),
    FOREIGN KEY (workspace_id, endpoint_id) REFERENCES wde.endpoints(workspace_id, id) ON UPDATE RESTRICT ON DELETE RESTRICT
);

CREATE TABLE wde.endpoint_secret_versions (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    endpoint_id uuid NOT NULL,
    key_id text NOT NULL CHECK (char_length(key_id) BETWEEN 8 AND 64),
    state text NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'retiring', 'retired', 'purged')),
    cipher_format_version smallint NOT NULL CHECK (cipher_format_version > 0),
    secret_ciphertext bytea,
    secret_nonce bytea,
    kek_version smallint,
    valid_from timestamptz NOT NULL DEFAULT clock_timestamp(),
    retire_at timestamptz,
    purged_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, endpoint_id, key_id),
    FOREIGN KEY (workspace_id, endpoint_id) REFERENCES wde.endpoints(workspace_id, id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    CHECK ((state <> 'purged' AND secret_ciphertext IS NOT NULL AND octet_length(secret_ciphertext) >= 16 AND secret_nonce IS NOT NULL AND octet_length(secret_nonce) = 12 AND kek_version > 0 AND purged_at IS NULL)
        OR (state = 'purged' AND secret_ciphertext IS NULL AND secret_nonce IS NULL AND kek_version IS NULL AND purged_at IS NOT NULL))
);

CREATE UNIQUE INDEX endpoint_secret_one_active_idx
    ON wde.endpoint_secret_versions (workspace_id, endpoint_id) WHERE state = 'active';

CREATE TABLE wde.events (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    idempotency_key_hash bytea NOT NULL CHECK (octet_length(idempotency_key_hash) = 32),
    idempotency_fingerprint bytea NOT NULL CHECK (octet_length(idempotency_fingerprint) = 32),
    fingerprint_version smallint NOT NULL DEFAULT 1 CHECK (fingerprint_version > 0),
    event_type text NOT NULL CHECK (char_length(event_type) BETWEEN 1 AND 128 AND event_type ~ '^[A-Za-z0-9][A-Za-z0-9._:-]*$'),
    payload_cipher_format_version smallint,
    payload_ciphertext bytea,
    payload_nonce bytea,
    payload_kek_version smallint,
    payload_size integer NOT NULL CHECK (payload_size BETWEEN 0 AND 1048576),
    payload_expires_at timestamptz NOT NULL,
    payload_purged_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, idempotency_key_hash),
    FOREIGN KEY (workspace_id) REFERENCES wde.workspaces(id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    CHECK ((payload_ciphertext IS NOT NULL AND octet_length(payload_ciphertext) >= 16 AND payload_nonce IS NOT NULL AND octet_length(payload_nonce) = 12 AND payload_kek_version > 0 AND payload_cipher_format_version > 0 AND payload_purged_at IS NULL)
        OR (payload_ciphertext IS NULL AND payload_nonce IS NULL AND payload_kek_version IS NULL AND payload_cipher_format_version IS NULL AND payload_purged_at IS NOT NULL))
);

CREATE TABLE wde.deliveries (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    event_id uuid NOT NULL,
    endpoint_id uuid NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'retry_scheduled', 'succeeded', 'failed_permanent', 'dead_letter')),
    run_number integer NOT NULL DEFAULT 1 CHECK (run_number >= 1),
    attempts_in_run smallint NOT NULL DEFAULT 0 CHECK (attempts_in_run BETWEEN 0 AND 100),
    attempt_sequence integer NOT NULL DEFAULT 0 CHECK (attempt_sequence >= 0),
    max_attempts_per_run smallint NOT NULL DEFAULT 10 CHECK (max_attempts_per_run BETWEEN 1 AND 100),
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    lease_owner uuid,
    lease_expires_at timestamptz,
    fencing_token bigint NOT NULL DEFAULT 0 CHECK (fencing_token >= 0),
    last_error_category text,
    succeeded_at timestamptz,
    terminal_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id, event_id) REFERENCES wde.events(workspace_id, id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, endpoint_id) REFERENCES wde.endpoints(workspace_id, id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    CHECK (attempts_in_run <= max_attempts_per_run AND attempt_sequence >= attempts_in_run),
    CHECK ((status = 'processing' AND lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL) OR (status <> 'processing' AND lease_owner IS NULL AND lease_expires_at IS NULL)),
    CHECK ((status = 'succeeded' AND succeeded_at IS NOT NULL AND terminal_at IS NOT NULL) OR (status <> 'succeeded' AND succeeded_at IS NULL)),
    CHECK ((status IN ('failed_permanent', 'dead_letter') AND terminal_at IS NOT NULL) OR (status NOT IN ('failed_permanent', 'dead_letter', 'succeeded') AND terminal_at IS NULL) OR status = 'succeeded')
);

CREATE INDEX deliveries_ready_idx ON wde.deliveries (next_attempt_at, created_at, id)
    WHERE status IN ('pending', 'retry_scheduled');
CREATE INDEX deliveries_expired_lease_idx ON wde.deliveries (lease_expires_at, id) WHERE status = 'processing';
CREATE INDEX deliveries_workspace_list_idx ON wde.deliveries (workspace_id, created_at DESC, id DESC);
CREATE INDEX deliveries_endpoint_active_idx ON wde.deliveries (workspace_id, endpoint_id, lease_expires_at) WHERE status = 'processing';

CREATE TABLE wde.delivery_attempts (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    delivery_id uuid NOT NULL,
    run_number integer NOT NULL CHECK (run_number >= 1),
    attempt_number_in_run smallint NOT NULL CHECK (attempt_number_in_run BETWEEN 1 AND 100),
    attempt_sequence integer NOT NULL CHECK (attempt_sequence >= 1),
    fencing_token bigint NOT NULL CHECK (fencing_token >= 1),
    state text NOT NULL DEFAULT 'started' CHECK (state IN ('started', 'completed', 'abandoned')),
    outcome text CHECK (outcome IN ('success', 'retry', 'permanent_failure', 'stale')),
    http_status smallint CHECK (http_status BETWEEN 100 AND 599),
    duration_ms integer CHECK (duration_ms >= 0),
    response_bytes_read integer CHECK (response_bytes_read >= 0),
    error_category text,
    started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    finished_at timestamptz,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, delivery_id, run_number, attempt_number_in_run),
    UNIQUE (workspace_id, delivery_id, attempt_sequence),
    UNIQUE (workspace_id, delivery_id, fencing_token),
    FOREIGN KEY (workspace_id, delivery_id) REFERENCES wde.deliveries(workspace_id, id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    CHECK ((state = 'started' AND outcome IS NULL AND finished_at IS NULL) OR (state <> 'started' AND outcome IS NOT NULL AND finished_at IS NOT NULL))
);

ALTER TABLE wde.workspaces ENABLE ROW LEVEL SECURITY; ALTER TABLE wde.workspaces FORCE ROW LEVEL SECURITY;
ALTER TABLE wde.api_keys ENABLE ROW LEVEL SECURITY; ALTER TABLE wde.api_keys FORCE ROW LEVEL SECURITY;
ALTER TABLE wde.api_key_scopes ENABLE ROW LEVEL SECURITY; ALTER TABLE wde.api_key_scopes FORCE ROW LEVEL SECURITY;
ALTER TABLE wde.endpoints ENABLE ROW LEVEL SECURITY; ALTER TABLE wde.endpoints FORCE ROW LEVEL SECURITY;
ALTER TABLE wde.endpoint_subscriptions ENABLE ROW LEVEL SECURITY; ALTER TABLE wde.endpoint_subscriptions FORCE ROW LEVEL SECURITY;
ALTER TABLE wde.endpoint_runtime ENABLE ROW LEVEL SECURITY; ALTER TABLE wde.endpoint_runtime FORCE ROW LEVEL SECURITY;
ALTER TABLE wde.endpoint_secret_versions ENABLE ROW LEVEL SECURITY; ALTER TABLE wde.endpoint_secret_versions FORCE ROW LEVEL SECURITY;
ALTER TABLE wde.events ENABLE ROW LEVEL SECURITY; ALTER TABLE wde.events FORCE ROW LEVEL SECURITY;
ALTER TABLE wde.deliveries ENABLE ROW LEVEL SECURITY; ALTER TABLE wde.deliveries FORCE ROW LEVEL SECURITY;
ALTER TABLE wde.delivery_attempts ENABLE ROW LEVEL SECURITY; ALTER TABLE wde.delivery_attempts FORCE ROW LEVEL SECURITY;

CREATE POLICY workspaces_api ON wde.workspaces TO wde_api USING (id = wde.current_workspace_id() AND status = 'active');
CREATE POLICY api_keys_admin ON wde.api_keys TO wde_admin USING (true) WITH CHECK (true);
CREATE POLICY api_keys_auth ON wde.api_keys FOR SELECT TO wde_auth_executor USING (true);
CREATE POLICY workspaces_auth ON wde.workspaces FOR SELECT TO wde_auth_executor USING (true);
CREATE POLICY scopes_admin ON wde.api_key_scopes TO wde_admin USING (true) WITH CHECK (true);
CREATE POLICY scopes_auth ON wde.api_key_scopes FOR SELECT TO wde_auth_executor USING (true);
CREATE POLICY workspaces_admin ON wde.workspaces TO wde_admin USING (true) WITH CHECK (true);

CREATE POLICY endpoints_api ON wde.endpoints TO wde_api USING (workspace_id = wde.current_workspace_id() AND EXISTS (SELECT 1 FROM wde.workspaces w WHERE w.id = workspace_id AND w.status = 'active')) WITH CHECK (workspace_id = wde.current_workspace_id() AND EXISTS (SELECT 1 FROM wde.workspaces w WHERE w.id = workspace_id AND w.status = 'active'));
CREATE POLICY subscriptions_api ON wde.endpoint_subscriptions TO wde_api USING (workspace_id = wde.current_workspace_id() AND EXISTS (SELECT 1 FROM wde.workspaces w WHERE w.id = workspace_id AND w.status = 'active')) WITH CHECK (workspace_id = wde.current_workspace_id() AND EXISTS (SELECT 1 FROM wde.workspaces w WHERE w.id = workspace_id AND w.status = 'active'));
CREATE POLICY endpoint_runtime_api ON wde.endpoint_runtime TO wde_api USING (workspace_id = wde.current_workspace_id() AND EXISTS (SELECT 1 FROM wde.workspaces w WHERE w.id = workspace_id AND w.status = 'active')) WITH CHECK (workspace_id = wde.current_workspace_id() AND EXISTS (SELECT 1 FROM wde.workspaces w WHERE w.id = workspace_id AND w.status = 'active'));
CREATE POLICY endpoint_secrets_api ON wde.endpoint_secret_versions TO wde_api USING (workspace_id = wde.current_workspace_id() AND EXISTS (SELECT 1 FROM wde.workspaces w WHERE w.id = workspace_id AND w.status = 'active')) WITH CHECK (workspace_id = wde.current_workspace_id() AND EXISTS (SELECT 1 FROM wde.workspaces w WHERE w.id = workspace_id AND w.status = 'active'));
CREATE POLICY events_api ON wde.events TO wde_api USING (workspace_id = wde.current_workspace_id() AND EXISTS (SELECT 1 FROM wde.workspaces w WHERE w.id = workspace_id AND w.status = 'active')) WITH CHECK (workspace_id = wde.current_workspace_id() AND EXISTS (SELECT 1 FROM wde.workspaces w WHERE w.id = workspace_id AND w.status = 'active'));
CREATE POLICY deliveries_api ON wde.deliveries TO wde_api USING (workspace_id = wde.current_workspace_id() AND EXISTS (SELECT 1 FROM wde.workspaces w WHERE w.id = workspace_id AND w.status = 'active')) WITH CHECK (workspace_id = wde.current_workspace_id() AND EXISTS (SELECT 1 FROM wde.workspaces w WHERE w.id = workspace_id AND w.status = 'active'));
CREATE POLICY attempts_api ON wde.delivery_attempts FOR SELECT TO wde_api USING (workspace_id = wde.current_workspace_id() AND EXISTS (SELECT 1 FROM wde.workspaces w WHERE w.id = workspace_id AND w.status = 'active'));

CREATE POLICY endpoints_worker ON wde.endpoints TO wde_worker_executor USING (true) WITH CHECK (true);
CREATE POLICY endpoint_secrets_worker ON wde.endpoint_secret_versions TO wde_worker_executor USING (true) WITH CHECK (true);
CREATE POLICY events_worker ON wde.events TO wde_worker_executor USING (true) WITH CHECK (true);
CREATE POLICY deliveries_worker ON wde.deliveries TO wde_worker_executor USING (true) WITH CHECK (true);
CREATE POLICY attempts_worker ON wde.delivery_attempts TO wde_worker_executor USING (true) WITH CHECK (true);

GRANT EXECUTE ON FUNCTION wde.current_workspace_id() TO wde_api;
GRANT SELECT ON wde.workspaces TO wde_api;
GRANT SELECT, INSERT ON wde.endpoints, wde.endpoint_subscriptions, wde.endpoint_runtime, wde.endpoint_secret_versions, wde.events, wde.deliveries TO wde_api;
GRANT SELECT ON wde.delivery_attempts TO wde_api;
GRANT SELECT, INSERT, UPDATE ON wde.workspaces, wde.api_keys, wde.api_key_scopes TO wde_admin;
GRANT SELECT ON wde.workspaces, wde.api_keys, wde.api_key_scopes TO wde_auth_executor;
GRANT SELECT ON wde.endpoints, wde.endpoint_secret_versions, wde.events TO wde_worker_executor;
GRANT SELECT, UPDATE ON wde.deliveries TO wde_worker_executor;
GRANT SELECT, INSERT, UPDATE ON wde.delivery_attempts TO wde_worker_executor;

CREATE FUNCTION wde.authenticate_key(p_prefix text)
RETURNS TABLE (api_key_id uuid, workspace_id uuid, verifier bytea, status text, workspace_status text, expires_at timestamptz, scopes text[])
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog
AS $function$
    SELECT k.id, k.workspace_id, k.verifier, k.status, w.status, k.expires_at,
           COALESCE(array_agg(s.scope ORDER BY s.scope) FILTER (WHERE s.scope IS NOT NULL), ARRAY[]::text[])
      FROM wde.api_keys k
      JOIN wde.workspaces w ON w.id = k.workspace_id
      LEFT JOIN wde.api_key_scopes s ON s.workspace_id = k.workspace_id AND s.api_key_id = k.id
     WHERE k.prefix = p_prefix AND char_length(p_prefix) BETWEEN 8 AND 32
     GROUP BY k.id, k.workspace_id, k.verifier, k.status, w.status, k.expires_at
$function$;
ALTER FUNCTION wde.authenticate_key(text) OWNER TO wde_auth_executor;
REVOKE ALL ON FUNCTION wde.authenticate_key(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION wde.authenticate_key(text) TO wde_api;

CREATE FUNCTION wde.claim_delivery(p_worker_id uuid, p_attempt_id uuid, p_lease_ttl interval)
RETURNS TABLE (
    workspace_id uuid, delivery_id uuid, event_id uuid, endpoint_id uuid, fencing_token bigint,
    scheme text, host_ascii text, port integer, target_cipher_format_version smallint,
    target_ciphertext bytea, target_nonce bytea, target_kek_version smallint,
    event_type text, payload_cipher_format_version smallint, payload_ciphertext bytea,
    payload_nonce bytea, payload_kek_version smallint, key_id text,
    secret_version_id uuid, secret_cipher_format_version smallint, secret_ciphertext bytea,
    secret_nonce bytea, secret_kek_version smallint
)
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog
AS $function$
DECLARE
    candidate record;
BEGIN
    IF p_lease_ttl < interval '20 seconds' OR p_lease_ttl > interval '2 minutes' THEN
        RAISE EXCEPTION 'invalid lease ttl';
    END IF;

    SELECT d.*,
           ep.scheme AS endpoint_scheme, ep.host_ascii AS endpoint_host_ascii, ep.port AS endpoint_port,
           ep.target_cipher_format_version AS endpoint_target_cipher_format_version,
           ep.target_ciphertext AS endpoint_target_ciphertext, ep.target_nonce AS endpoint_target_nonce,
           ep.target_kek_version AS endpoint_target_kek_version,
           ev.event_type AS snapshot_event_type, ev.payload_cipher_format_version AS snapshot_payload_cipher_format_version,
           ev.payload_ciphertext AS snapshot_payload_ciphertext, ev.payload_nonce AS snapshot_payload_nonce,
           ev.payload_kek_version AS snapshot_payload_kek_version,
           sv.key_id AS snapshot_key_id, sv.id AS snapshot_secret_version_id,
           sv.cipher_format_version AS snapshot_secret_cipher_format_version,
           sv.secret_ciphertext AS snapshot_secret_ciphertext, sv.secret_nonce AS snapshot_secret_nonce,
           sv.kek_version AS snapshot_secret_kek_version
      INTO candidate
      FROM wde.deliveries d
      JOIN wde.endpoints ep ON ep.workspace_id = d.workspace_id AND ep.id = d.endpoint_id AND ep.status = 'active'
      JOIN wde.events ev ON ev.workspace_id = d.workspace_id AND ev.id = d.event_id AND ev.payload_ciphertext IS NOT NULL
      JOIN wde.endpoint_secret_versions sv ON sv.workspace_id = d.workspace_id AND sv.endpoint_id = d.endpoint_id AND sv.state = 'active'
     WHERE d.status IN ('pending', 'retry_scheduled')
       AND d.next_attempt_at <= clock_timestamp()
       AND d.attempts_in_run < d.max_attempts_per_run
     ORDER BY d.next_attempt_at, d.created_at, d.id
     FOR UPDATE OF d SKIP LOCKED
     LIMIT 1;

    IF NOT FOUND THEN RETURN; END IF;

    UPDATE wde.deliveries d
       SET status = 'processing', lease_owner = p_worker_id,
           lease_expires_at = clock_timestamp() + p_lease_ttl,
           fencing_token = d.fencing_token + 1,
           attempts_in_run = d.attempts_in_run + 1,
           attempt_sequence = d.attempt_sequence + 1,
           updated_at = clock_timestamp()
     WHERE d.id = candidate.id;

    INSERT INTO wde.delivery_attempts
        (id, workspace_id, delivery_id, run_number, attempt_number_in_run, attempt_sequence, fencing_token)
    SELECT p_attempt_id, d.workspace_id, d.id, d.run_number, d.attempts_in_run, d.attempt_sequence, d.fencing_token
      FROM wde.deliveries d WHERE d.id = candidate.id;

    RETURN QUERY SELECT candidate.workspace_id, candidate.id, candidate.event_id, candidate.endpoint_id,
           candidate.fencing_token + 1, candidate.endpoint_scheme, candidate.endpoint_host_ascii,
           candidate.endpoint_port, candidate.endpoint_target_cipher_format_version,
           candidate.endpoint_target_ciphertext, candidate.endpoint_target_nonce, candidate.endpoint_target_kek_version,
           candidate.snapshot_event_type, candidate.snapshot_payload_cipher_format_version,
           candidate.snapshot_payload_ciphertext, candidate.snapshot_payload_nonce, candidate.snapshot_payload_kek_version,
           candidate.snapshot_key_id, candidate.snapshot_secret_version_id, candidate.snapshot_secret_cipher_format_version,
           candidate.snapshot_secret_ciphertext, candidate.snapshot_secret_nonce, candidate.snapshot_secret_kek_version;
END
$function$;
ALTER FUNCTION wde.claim_delivery(uuid, uuid, interval) OWNER TO wde_worker_executor;
REVOKE ALL ON FUNCTION wde.claim_delivery(uuid, uuid, interval) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION wde.claim_delivery(uuid, uuid, interval) TO wde_worker;

CREATE FUNCTION wde.finalize_delivery(
    p_workspace_id uuid, p_delivery_id uuid, p_worker_id uuid, p_fencing_token bigint,
    p_success boolean, p_http_status smallint, p_duration_ms integer, p_error_category text
)
RETURNS boolean
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog
AS $function$
DECLARE changed integer;
DECLARE changed_attempt integer;
BEGIN
    UPDATE wde.deliveries d
       SET status = CASE WHEN p_success THEN 'succeeded' ELSE 'failed_permanent' END,
           lease_owner = NULL, lease_expires_at = NULL,
           last_error_category = CASE WHEN p_success THEN NULL ELSE left(p_error_category, 64) END,
           succeeded_at = CASE WHEN p_success THEN clock_timestamp() ELSE NULL END,
           terminal_at = clock_timestamp(), updated_at = clock_timestamp()
     WHERE d.workspace_id = p_workspace_id AND d.id = p_delivery_id
       AND d.status = 'processing' AND d.lease_owner = p_worker_id
       AND d.fencing_token = p_fencing_token AND d.lease_expires_at > clock_timestamp();
    GET DIAGNOSTICS changed = ROW_COUNT;
    IF changed = 0 THEN RETURN false; END IF;

    UPDATE wde.delivery_attempts a
       SET state = 'completed', outcome = CASE WHEN p_success THEN 'success' ELSE 'permanent_failure' END,
           http_status = p_http_status, duration_ms = GREATEST(p_duration_ms, 0),
           response_bytes_read = 0,
           error_category = CASE WHEN p_success THEN NULL ELSE left(p_error_category, 64) END,
           finished_at = clock_timestamp()
     WHERE a.workspace_id = p_workspace_id AND a.delivery_id = p_delivery_id
       AND a.fencing_token = p_fencing_token AND a.state = 'started';
    GET DIAGNOSTICS changed_attempt = ROW_COUNT;
    IF changed_attempt <> 1 THEN
        RAISE EXCEPTION 'delivery attempt invariant violated';
    END IF;
    RETURN true;
END
$function$;
ALTER FUNCTION wde.finalize_delivery(uuid, uuid, uuid, bigint, boolean, smallint, integer, text) OWNER TO wde_worker_executor;
REVOKE ALL ON FUNCTION wde.finalize_delivery(uuid, uuid, uuid, bigint, boolean, smallint, integer, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION wde.finalize_delivery(uuid, uuid, uuid, bigint, boolean, smallint, integer, text) TO wde_worker;

REVOKE ALL ON ALL TABLES IN SCHEMA wde FROM PUBLIC;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA wde FROM PUBLIC;
REVOKE CREATE ON SCHEMA wde FROM wde_auth_executor, wde_worker_executor, wde_audit_executor;
UPDATE wde.schema_metadata SET schema_version = 2 WHERE singleton;
RESET ROLE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET lock_timeout = '5s';
SET statement_timeout = '30s';
SET ROLE wde_owner;
DROP FUNCTION wde.finalize_delivery(uuid, uuid, uuid, bigint, boolean, smallint, integer, text);
DROP FUNCTION wde.claim_delivery(uuid, uuid, interval);
DROP FUNCTION wde.authenticate_key(text);
DROP TABLE wde.delivery_attempts;
DROP TABLE wde.deliveries;
DROP TABLE wde.events;
DROP TABLE wde.endpoint_secret_versions;
DROP TABLE wde.endpoint_runtime;
DROP TABLE wde.endpoint_subscriptions;
DROP TABLE wde.endpoints;
DROP TABLE wde.api_key_scopes;
DROP TABLE wde.api_keys;
DROP TABLE wde.workspaces;
DROP FUNCTION wde.current_workspace_id();
REVOKE EXECUTE ON FUNCTION wde.schema_version() FROM wde_admin;
UPDATE wde.schema_metadata SET schema_version = 1 WHERE singleton;
RESET ROLE;
-- +goose StatementEnd
