-- +goose Up
-- +goose StatementBegin
SET lock_timeout='5s';
SET statement_timeout='30s';
SET ROLE wde_owner;
GRANT CREATE ON SCHEMA wde TO wde_maintenance_executor;
SET ROLE wde_maintenance_executor;

CREATE FUNCTION wde.purge_expired_metadata_v5_stage(p_batch integer)
RETURNS TABLE(attempts integer,replays integer,rotations integer,deliveries integer,
    events integer,audits integer,buckets integer)
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog SET statement_timeout='5s'
AS $function$
BEGIN
    IF p_batch IS NULL OR p_batch NOT BETWEEN 1 AND 1000 THEN RAISE EXCEPTION 'invalid metadata purge batch'; END IF;
    WITH candidates AS MATERIALIZED (
        SELECT a.id FROM wde.delivery_attempts a
        JOIN wde.deliveries d ON d.workspace_id=a.workspace_id AND d.id=a.delivery_id
        JOIN wde.workspaces w ON w.id=a.workspace_id
        WHERE a.state<>'started' AND a.finished_at+make_interval(days=>w.metadata_retention_days)
            <=transaction_timestamp()
        ORDER BY a.finished_at,a.id FOR UPDATE OF a SKIP LOCKED LIMIT p_batch
    ) DELETE FROM wde.delivery_attempts a USING candidates c WHERE a.id=c.id;
    GET DIAGNOSTICS attempts=ROW_COUNT;

    WITH candidates AS MATERIALIZED (
        SELECT r.id FROM wde.replay_commands r JOIN wde.workspaces w ON w.id=r.workspace_id
        WHERE r.created_at+make_interval(days=>w.metadata_retention_days)<=transaction_timestamp()
        ORDER BY r.created_at,r.id FOR UPDATE OF r SKIP LOCKED LIMIT p_batch
    ) DELETE FROM wde.replay_commands r USING candidates c WHERE r.id=c.id;
    GET DIAGNOSTICS replays=ROW_COUNT;

    WITH candidates AS MATERIALIZED (
        SELECT c.id FROM wde.secret_rotation_commands c JOIN wde.workspaces w ON w.id=c.workspace_id
        WHERE c.created_at+make_interval(days=>w.metadata_retention_days)<=transaction_timestamp()
        ORDER BY c.created_at,c.id FOR UPDATE OF c SKIP LOCKED LIMIT p_batch
    ) DELETE FROM wde.secret_rotation_commands c USING candidates x WHERE c.id=x.id;
    GET DIAGNOSTICS rotations=ROW_COUNT;

    WITH candidates AS MATERIALIZED (
        SELECT d.id FROM wde.deliveries d JOIN wde.workspaces w ON w.id=d.workspace_id
        WHERE d.terminal_at IS NOT NULL
          AND d.terminal_at+make_interval(days=>w.metadata_retention_days)<=transaction_timestamp()
          AND NOT EXISTS(SELECT 1 FROM wde.delivery_attempts a
              WHERE a.workspace_id=d.workspace_id AND a.delivery_id=d.id)
          AND NOT EXISTS(SELECT 1 FROM wde.replay_commands r
              WHERE r.workspace_id=d.workspace_id AND r.delivery_id=d.id)
        ORDER BY d.terminal_at,d.id FOR UPDATE OF d SKIP LOCKED LIMIT p_batch
    ) DELETE FROM wde.deliveries d USING candidates c WHERE d.id=c.id;
    GET DIAGNOSTICS deliveries=ROW_COUNT;

    WITH candidates AS MATERIALIZED (
        SELECT e.id FROM wde.events e JOIN wde.workspaces w ON w.id=e.workspace_id
        WHERE e.payload_ciphertext IS NULL
          AND e.created_at+make_interval(days=>w.metadata_retention_days)<=transaction_timestamp()
          AND NOT EXISTS(SELECT 1 FROM wde.deliveries d
              WHERE d.workspace_id=e.workspace_id AND d.event_id=e.id)
        ORDER BY e.created_at,e.id FOR UPDATE OF e SKIP LOCKED LIMIT p_batch
    ) DELETE FROM wde.events e USING candidates c WHERE e.id=c.id;
    GET DIAGNOSTICS events=ROW_COUNT;

    WITH candidates AS MATERIALIZED (
        SELECT a.id FROM wde.audit_events a CROSS JOIN wde.restore_control r
         WHERE r.singleton AND a.created_at+make_interval(days=>r.audit_retention_days)<=transaction_timestamp()
        ORDER BY a.created_at,a.id FOR UPDATE OF a SKIP LOCKED LIMIT p_batch
    ) DELETE FROM wde.audit_events a USING candidates c WHERE a.id=c.id;
    GET DIAGNOSTICS audits=ROW_COUNT;

    WITH candidates AS MATERIALIZED (
        SELECT ctid FROM wde.rate_limit_buckets WHERE expires_at<=transaction_timestamp()
        ORDER BY expires_at FOR UPDATE SKIP LOCKED LIMIT p_batch
    ) DELETE FROM wde.rate_limit_buckets b USING candidates c WHERE b.ctid=c.ctid;
    GET DIAGNOSTICS buckets=ROW_COUNT;
    RETURN NEXT;
END
$function$;

ALTER FUNCTION wde.purge_expired_metadata_v5_stage(integer) OWNER TO wde_maintenance_executor;
REVOKE ALL ON FUNCTION wde.purge_expired_metadata_v5_stage(integer)
    FROM PUBLIC,wde_api,wde_worker,wde_admin;
SET ROLE wde_owner;
REVOKE CREATE ON SCHEMA wde FROM wde_maintenance_executor;
RESET ROLE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET ROLE wde_owner;
GRANT CREATE ON SCHEMA wde TO wde_maintenance_executor;
SET ROLE wde_maintenance_executor;
DROP FUNCTION wde.purge_expired_metadata_v5_stage(integer);
SET ROLE wde_owner;
REVOKE CREATE ON SCHEMA wde FROM wde_maintenance_executor;
RESET ROLE;
-- +goose StatementEnd
