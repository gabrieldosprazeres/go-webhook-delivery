-- +goose Up
-- +goose StatementBegin
SET lock_timeout='5s';
SET statement_timeout='30s';
SET ROLE wde_owner;

GRANT USAGE ON SCHEMA wde TO wde_console,wde_console_session_executor;
GRANT EXECUTE ON FUNCTION wde.schema_version(),wde.runtime_ready(),wde.current_workspace_id(),
    wde.authenticate_key(text) TO wde_console;
GRANT EXECUTE ON FUNCTION wde.append_audit_event(uuid,uuid,text,text,text,text,text,text,text,text),
    wde.request_replay(uuid,uuid,uuid,uuid,text,bytea,bytea,smallint,text,text),
    wde.consume_quota(bytea,text,text,uuid,uuid,uuid,integer,integer) TO wde_console;

ALTER TABLE wde.audit_events DROP CONSTRAINT audit_events_actor_type_check;
ALTER TABLE wde.audit_events ADD CONSTRAINT audit_events_actor_type_check
    CHECK(actor_type IN('api_key','admin_cli','system','console_session'));
ALTER TABLE wde.audit_events DROP CONSTRAINT audit_events_action_check;
ALTER TABLE wde.audit_events ADD CONSTRAINT audit_events_action_check CHECK(action IN(
    'credential.bootstrap','credential.revoke','endpoint.create','endpoint.secret_rotate','delivery.replay',
    'payload.purge','secret.purge','workspace.purge','restore.quarantine','restore.reconcile',
    'console.session.created','console.session.logout','console.session.revoked'));
ALTER TABLE wde.audit_events DROP CONSTRAINT audit_events_resource_type_check;
ALTER TABLE wde.audit_events ADD CONSTRAINT audit_events_resource_type_check CHECK(resource_type IN(
    'workspace','api_key','endpoint','delivery','event','secret','console_session'));

CREATE TABLE wde.console_sessions(
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    api_key_id uuid NOT NULL,
    token_prefix text NOT NULL UNIQUE CHECK(char_length(token_prefix)=16 AND token_prefix~'^[0-9a-f]+$'),
    token_verifier bytea NOT NULL CHECK(octet_length(token_verifier)=32),
    status text NOT NULL DEFAULT 'active' CHECK(status IN('active','revoked')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    last_seen_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    idle_expires_at timestamptz NOT NULL,
    absolute_expires_at timestamptz NOT NULL,
    restore_generation bigint NOT NULL CHECK(restore_generation>=0),
    revoked_at timestamptz,
    revoke_reason text CHECK(revoke_reason IS NULL OR revoke_reason IN(
        'user_logout','admin_revoke','api_key_revoked','expired','restore_quarantine','workspace_deleting')),
    UNIQUE(workspace_id,id),
    FOREIGN KEY(workspace_id,api_key_id) REFERENCES wde.api_keys(workspace_id,id) ON DELETE CASCADE,
    CHECK(created_at<=last_seen_at AND last_seen_at<idle_expires_at AND idle_expires_at<=absolute_expires_at),
    CHECK((status='active' AND revoked_at IS NULL AND revoke_reason IS NULL) OR
          (status='revoked' AND revoked_at IS NOT NULL AND revoke_reason IS NOT NULL))
);

CREATE INDEX console_sessions_key_active_idx ON wde.console_sessions(workspace_id,api_key_id,created_at,id)
    WHERE status='active';
CREATE INDEX console_sessions_expiry_idx ON wde.console_sessions(idle_expires_at,id) WHERE status='active';
CREATE INDEX endpoints_workspace_console_idx ON wde.endpoints(workspace_id,created_at DESC,id DESC);
CREATE INDEX events_workspace_metrics_idx ON wde.events(workspace_id,created_at);
CREATE INDEX delivery_attempts_workspace_metrics_idx ON wde.delivery_attempts(workspace_id,finished_at)
    INCLUDE(outcome,duration_ms) WHERE finished_at IS NOT NULL;

ALTER TABLE wde.console_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE wde.console_sessions FORCE ROW LEVEL SECURITY;
CREATE POLICY console_sessions_executor ON wde.console_sessions TO wde_console_session_executor
    USING(true) WITH CHECK(true);
CREATE POLICY console_workspaces ON wde.workspaces FOR SELECT TO wde_console
    USING(id=wde.current_workspace_id() AND status='active');
CREATE POLICY console_endpoints ON wde.endpoints TO wde_console
    USING(workspace_id=wde.current_workspace_id()) WITH CHECK(workspace_id=wde.current_workspace_id());
CREATE POLICY console_subscriptions ON wde.endpoint_subscriptions TO wde_console
    USING(workspace_id=wde.current_workspace_id()) WITH CHECK(workspace_id=wde.current_workspace_id());
CREATE POLICY console_runtime ON wde.endpoint_runtime TO wde_console
    USING(workspace_id=wde.current_workspace_id()) WITH CHECK(workspace_id=wde.current_workspace_id());
CREATE POLICY console_secrets ON wde.endpoint_secret_versions TO wde_console
    USING(workspace_id=wde.current_workspace_id()) WITH CHECK(workspace_id=wde.current_workspace_id());
CREATE POLICY console_events ON wde.events TO wde_console
    USING(workspace_id=wde.current_workspace_id()) WITH CHECK(workspace_id=wde.current_workspace_id());
CREATE POLICY console_deliveries ON wde.deliveries TO wde_console
    USING(workspace_id=wde.current_workspace_id()) WITH CHECK(workspace_id=wde.current_workspace_id());
CREATE POLICY console_attempts ON wde.delivery_attempts FOR SELECT TO wde_console
    USING(workspace_id=wde.current_workspace_id());
CREATE POLICY console_replays ON wde.replay_commands FOR SELECT TO wde_console
    USING(workspace_id=wde.current_workspace_id());
CREATE POLICY console_session_keys ON wde.api_keys FOR SELECT TO wde_console_session_executor USING(true);
CREATE POLICY console_session_scopes ON wde.api_key_scopes FOR SELECT TO wde_console_session_executor USING(true);
CREATE POLICY console_session_workspaces ON wde.workspaces FOR SELECT TO wde_console_session_executor USING(true);
CREATE POLICY console_session_audit ON wde.audit_events FOR INSERT TO wde_console_session_executor WITH CHECK(true);

GRANT SELECT,INSERT,UPDATE ON wde.console_sessions TO wde_console_session_executor;
GRANT SELECT ON wde.workspaces,wde.api_keys,wde.api_key_scopes,wde.restore_control TO wde_console_session_executor;
GRANT INSERT ON wde.audit_events TO wde_console_session_executor;
GRANT SELECT ON wde.workspaces,wde.delivery_attempts,wde.replay_commands TO wde_console;
GRANT SELECT,INSERT ON wde.endpoints,wde.endpoint_subscriptions,wde.endpoint_runtime,
    wde.endpoint_secret_versions,wde.events,wde.deliveries TO wde_console;

GRANT CREATE ON SCHEMA wde TO wde_console_session_executor;
SET ROLE wde_console_session_executor;

CREATE FUNCTION wde.create_console_session(
    p_id uuid,p_workspace_id uuid,p_api_key_id uuid,p_prefix text,p_verifier bytea,p_audit_id uuid
) RETURNS TABLE(session_id uuid)
LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path=pg_catalog SET statement_timeout='2s'
AS $function$
DECLARE v_generation bigint; v_active integer;
BEGIN
    IF p_id IS NULL OR p_workspace_id IS NULL OR p_api_key_id IS NULL OR p_audit_id IS NULL
       OR char_length(p_prefix)<>16 OR p_prefix!~'^[0-9a-f]+$' OR octet_length(p_verifier)<>32 THEN
        RAISE EXCEPTION 'console_session_invalid';
    END IF;
    SELECT r.generation INTO v_generation FROM wde.restore_control r
      WHERE r.singleton AND r.state='ready';
    IF NOT FOUND THEN RAISE EXCEPTION 'console_session_unauthorized'; END IF;
    PERFORM pg_catalog.pg_advisory_xact_lock(pg_catalog.hashtextextended(p_api_key_id::text,731));
    IF NOT EXISTS(
        SELECT 1 FROM wde.api_keys k JOIN wde.workspaces w ON w.id=k.workspace_id
         WHERE k.workspace_id=p_workspace_id AND k.id=p_api_key_id AND k.status='active'
           AND (k.expires_at IS NULL OR k.expires_at>statement_timestamp()) AND w.status='active') THEN
        RAISE EXCEPTION 'console_session_unauthorized';
    END IF;
    UPDATE wde.console_sessions SET status='revoked',revoked_at=clock_timestamp(),
        revoke_reason='expired',updated_at=clock_timestamp()
      WHERE workspace_id=p_workspace_id AND api_key_id=p_api_key_id AND status='active'
        AND (idle_expires_at<=statement_timestamp() OR absolute_expires_at<=statement_timestamp()
             OR restore_generation<>v_generation);
    SELECT count(*) INTO v_active FROM wde.console_sessions
      WHERE workspace_id=p_workspace_id AND api_key_id=p_api_key_id AND status='active';
    IF v_active>=5 THEN RAISE EXCEPTION 'console_session_limit'; END IF;
    INSERT INTO wde.console_sessions(id,workspace_id,api_key_id,token_prefix,token_verifier,
        idle_expires_at,absolute_expires_at,restore_generation)
      VALUES(p_id,p_workspace_id,p_api_key_id,p_prefix,p_verifier,
        clock_timestamp()+interval '15 minutes',clock_timestamp()+interval '60 minutes',v_generation);
    INSERT INTO wde.audit_events(id,workspace_id,actor_type,actor_id,action,resource_type,
        resource_id,request_id,outcome)
      VALUES(p_audit_id,p_workspace_id,'console_session',p_id::text,'console.session.created',
        'console_session',p_id::text,p_audit_id::text,'accepted');
    RETURN QUERY SELECT p_id;
END
$function$;

CREATE FUNCTION wde.lookup_console_session(p_prefix text)
RETURNS TABLE(session_id uuid,token_verifier bytea)
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path=pg_catalog SET statement_timeout='1s'
AS $function$
    SELECT s.id,s.token_verifier FROM wde.console_sessions s
     WHERE char_length(p_prefix)=16 AND s.token_prefix=p_prefix
$function$;

CREATE FUNCTION wde.resume_console_session(p_id uuid)
RETURNS TABLE(api_key_id uuid,workspace_id uuid,scopes text[])
LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path=pg_catalog SET statement_timeout='2s'
AS $function$
DECLARE v_generation bigint;
BEGIN
    SELECT generation INTO v_generation FROM wde.restore_control WHERE singleton AND state='ready';
    IF NOT FOUND THEN RETURN; END IF;
    UPDATE wde.console_sessions s SET
        last_seen_at=CASE WHEN s.last_seen_at<=statement_timestamp()-interval '1 minute'
            THEN clock_timestamp() ELSE s.last_seen_at END,
        idle_expires_at=CASE WHEN s.last_seen_at<=statement_timestamp()-interval '1 minute'
            THEN LEAST(clock_timestamp()+interval '15 minutes',s.absolute_expires_at) ELSE s.idle_expires_at END,
        updated_at=clock_timestamp()
      FROM wde.api_keys k,wde.workspaces w
     WHERE s.id=p_id AND s.status='active' AND s.api_key_id=k.id AND s.workspace_id=k.workspace_id
       AND w.id=s.workspace_id AND k.status='active' AND w.status='active'
       AND (k.expires_at IS NULL OR k.expires_at>statement_timestamp())
       AND s.idle_expires_at>statement_timestamp() AND s.absolute_expires_at>statement_timestamp()
       AND s.restore_generation=v_generation;
    IF NOT FOUND THEN RETURN; END IF;
    RETURN QUERY
      SELECT s.api_key_id,s.workspace_id,
        COALESCE(array_agg(ks.scope ORDER BY ks.scope) FILTER(WHERE ks.scope IS NOT NULL),ARRAY[]::text[])
        FROM wde.console_sessions s
        LEFT JOIN wde.api_key_scopes ks ON ks.workspace_id=s.workspace_id AND ks.api_key_id=s.api_key_id
       WHERE s.id=p_id GROUP BY s.api_key_id,s.workspace_id;
END
$function$;

CREATE FUNCTION wde.revoke_console_session(p_id uuid,p_audit_id uuid,p_reason text)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path=pg_catalog SET statement_timeout='2s'
AS $function$
DECLARE v_workspace uuid;
BEGIN
    IF p_reason NOT IN('user_logout','admin_revoke','api_key_revoked','restore_quarantine','workspace_deleting') THEN
        RAISE EXCEPTION 'console_session_invalid';
    END IF;
    UPDATE wde.console_sessions SET status='revoked',revoked_at=clock_timestamp(),
        revoke_reason=p_reason,updated_at=clock_timestamp()
      WHERE id=p_id AND status='active' RETURNING workspace_id INTO v_workspace;
    IF NOT FOUND THEN RETURN false; END IF;
    INSERT INTO wde.audit_events(id,workspace_id,actor_type,actor_id,action,resource_type,
        resource_id,request_id,outcome)
      VALUES(p_audit_id,v_workspace,'console_session',p_id::text,
        CASE WHEN p_reason='user_logout' THEN 'console.session.logout' ELSE 'console.session.revoked' END,
        'console_session',p_id::text,p_audit_id::text,'revoked');
    RETURN true;
END
$function$;

CREATE FUNCTION wde.purge_console_sessions(p_batch integer)
RETURNS integer LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path=pg_catalog SET statement_timeout='5s'
AS $function$
DECLARE v_count integer;
BEGIN
    IF p_batch<1 OR p_batch>1000 THEN RAISE EXCEPTION 'console_session_invalid'; END IF;
    UPDATE wde.console_sessions SET status='revoked',revoked_at=clock_timestamp(),
        revoke_reason='expired',updated_at=clock_timestamp()
      WHERE id IN(
        SELECT id FROM wde.console_sessions
         WHERE status='active' AND (idle_expires_at<=statement_timestamp()
            OR absolute_expires_at<=statement_timestamp())
         ORDER BY LEAST(idle_expires_at,absolute_expires_at),id
         LIMIT p_batch FOR UPDATE SKIP LOCKED
      );
    WITH candidates AS(
      SELECT id FROM wde.console_sessions
       WHERE status='revoked' AND revoked_at<statement_timestamp()-interval '30 days'
       ORDER BY revoked_at,id LIMIT p_batch FOR UPDATE SKIP LOCKED
    ) DELETE FROM wde.console_sessions s USING candidates c WHERE s.id=c.id;
    GET DIAGNOSTICS v_count=ROW_COUNT;
    RETURN v_count;
END
$function$;

ALTER FUNCTION wde.create_console_session(uuid,uuid,uuid,text,bytea,uuid) OWNER TO wde_console_session_executor;
ALTER FUNCTION wde.lookup_console_session(text) OWNER TO wde_console_session_executor;
ALTER FUNCTION wde.resume_console_session(uuid) OWNER TO wde_console_session_executor;
ALTER FUNCTION wde.revoke_console_session(uuid,uuid,text) OWNER TO wde_console_session_executor;
ALTER FUNCTION wde.purge_console_sessions(integer) OWNER TO wde_console_session_executor;
REVOKE ALL ON FUNCTION wde.create_console_session(uuid,uuid,uuid,text,bytea,uuid),
    wde.lookup_console_session(text),wde.resume_console_session(uuid),
    wde.revoke_console_session(uuid,uuid,text),wde.purge_console_sessions(integer) FROM PUBLIC;

SET ROLE wde_owner;
GRANT EXECUTE ON FUNCTION wde.create_console_session(uuid,uuid,uuid,text,bytea,uuid),
    wde.lookup_console_session(text),wde.resume_console_session(uuid),
    wde.revoke_console_session(uuid,uuid,text) TO wde_console;
GRANT EXECUTE ON FUNCTION wde.purge_console_sessions(integer) TO wde_worker;
REVOKE CREATE ON SCHEMA wde FROM wde_console_session_executor;
GRANT EXECUTE ON FUNCTION wde.runtime_ready() TO wde_console;
UPDATE wde.schema_metadata SET schema_version=6 WHERE singleton;
RESET ROLE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET lock_timeout='5s';
SET statement_timeout='30s';
SET ROLE wde_owner;
DO $guard$
BEGIN
    IF EXISTS(SELECT 1 FROM wde.console_sessions) OR EXISTS(
        SELECT 1 FROM wde.audit_events WHERE action LIKE 'console.session.%') THEN
        RAISE EXCEPTION 'unsafe downgrade: console session or audit state exists';
    END IF;
END
$guard$;
UPDATE wde.schema_metadata SET schema_version=5 WHERE singleton;
REVOKE EXECUTE ON FUNCTION wde.purge_console_sessions(integer) FROM wde_worker;
REVOKE EXECUTE ON FUNCTION wde.create_console_session(uuid,uuid,uuid,text,bytea,uuid),
    wde.lookup_console_session(text),wde.resume_console_session(uuid),
    wde.revoke_console_session(uuid,uuid,text) FROM wde_console;
GRANT CREATE ON SCHEMA wde TO wde_console_session_executor;
SET ROLE wde_console_session_executor;
DROP FUNCTION wde.purge_console_sessions(integer);
DROP FUNCTION wde.revoke_console_session(uuid,uuid,text);
DROP FUNCTION wde.resume_console_session(uuid);
DROP FUNCTION wde.lookup_console_session(text);
DROP FUNCTION wde.create_console_session(uuid,uuid,uuid,text,bytea,uuid);
SET ROLE wde_owner;
REVOKE CREATE ON SCHEMA wde FROM wde_console_session_executor;
DROP POLICY console_session_audit ON wde.audit_events;
DROP POLICY console_session_workspaces ON wde.workspaces;
DROP POLICY console_session_scopes ON wde.api_key_scopes;
DROP POLICY console_session_keys ON wde.api_keys;
DROP POLICY console_replays ON wde.replay_commands;
DROP POLICY console_attempts ON wde.delivery_attempts;
DROP POLICY console_deliveries ON wde.deliveries;
DROP POLICY console_events ON wde.events;
DROP POLICY console_secrets ON wde.endpoint_secret_versions;
DROP POLICY console_runtime ON wde.endpoint_runtime;
DROP POLICY console_subscriptions ON wde.endpoint_subscriptions;
DROP POLICY console_endpoints ON wde.endpoints;
DROP POLICY console_workspaces ON wde.workspaces;
DROP TABLE wde.console_sessions;
DROP INDEX wde.delivery_attempts_workspace_metrics_idx;
DROP INDEX wde.events_workspace_metrics_idx;
DROP INDEX wde.endpoints_workspace_console_idx;
ALTER TABLE wde.audit_events DROP CONSTRAINT audit_events_actor_type_check;
ALTER TABLE wde.audit_events ADD CONSTRAINT audit_events_actor_type_check
    CHECK(actor_type IN('api_key','admin_cli','system'));
ALTER TABLE wde.audit_events DROP CONSTRAINT audit_events_action_check;
ALTER TABLE wde.audit_events ADD CONSTRAINT audit_events_action_check CHECK(action IN(
    'credential.bootstrap','credential.revoke','endpoint.create','endpoint.secret_rotate','delivery.replay',
    'payload.purge','secret.purge','workspace.purge','restore.quarantine','restore.reconcile'));
ALTER TABLE wde.audit_events DROP CONSTRAINT audit_events_resource_type_check;
ALTER TABLE wde.audit_events ADD CONSTRAINT audit_events_resource_type_check
    CHECK(resource_type IN('workspace','api_key','endpoint','delivery','event','secret'));
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA wde FROM wde_console;
REVOKE ALL ON ALL TABLES IN SCHEMA wde FROM wde_console,wde_console_session_executor;
REVOKE USAGE ON SCHEMA wde FROM wde_console,wde_console_session_executor;
RESET ROLE;
-- +goose StatementEnd
