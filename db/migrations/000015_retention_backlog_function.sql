-- +goose Up
-- +goose StatementBegin
SET lock_timeout='5s';
SET statement_timeout='30s';
SET ROLE wde_owner;
GRANT CREATE ON SCHEMA wde TO wde_maintenance_executor;
SET ROLE wde_maintenance_executor;

CREATE FUNCTION wde.retention_backlog_v5_stage()
RETURNS TABLE(total bigint,oldest_expired_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog SET statement_timeout='5s'
AS $function$
WITH expired(expired_at) AS (
    SELECT payload_expires_at FROM wde.events
     WHERE payload_ciphertext IS NOT NULL AND payload_expires_at<=transaction_timestamp()
    UNION ALL SELECT retire_at FROM wde.endpoint_secret_versions
     WHERE state='retiring' AND retire_at<=transaction_timestamp()
    UNION ALL SELECT a.finished_at+make_interval(days=>w.metadata_retention_days)
      FROM wde.delivery_attempts a JOIN wde.workspaces w ON w.id=a.workspace_id
     WHERE a.state<>'started' AND a.finished_at+make_interval(days=>w.metadata_retention_days)
        <=transaction_timestamp()
    UNION ALL SELECT r.created_at+make_interval(days=>w.metadata_retention_days)
      FROM wde.replay_commands r JOIN wde.workspaces w ON w.id=r.workspace_id
     WHERE r.created_at+make_interval(days=>w.metadata_retention_days)<=transaction_timestamp()
    UNION ALL SELECT c.created_at+make_interval(days=>w.metadata_retention_days)
      FROM wde.secret_rotation_commands c JOIN wde.workspaces w ON w.id=c.workspace_id
     WHERE c.created_at+make_interval(days=>w.metadata_retention_days)<=transaction_timestamp()
    UNION ALL SELECT d.terminal_at+make_interval(days=>w.metadata_retention_days)
      FROM wde.deliveries d JOIN wde.workspaces w ON w.id=d.workspace_id
     WHERE d.terminal_at IS NOT NULL
       AND d.terminal_at+make_interval(days=>w.metadata_retention_days)<=transaction_timestamp()
       AND NOT EXISTS(SELECT 1 FROM wde.delivery_attempts a
           WHERE a.workspace_id=d.workspace_id AND a.delivery_id=d.id)
       AND NOT EXISTS(SELECT 1 FROM wde.replay_commands r
           WHERE r.workspace_id=d.workspace_id AND r.delivery_id=d.id)
    UNION ALL SELECT e.created_at+make_interval(days=>w.metadata_retention_days)
      FROM wde.events e JOIN wde.workspaces w ON w.id=e.workspace_id
     WHERE e.payload_ciphertext IS NULL
       AND e.created_at+make_interval(days=>w.metadata_retention_days)<=transaction_timestamp()
       AND NOT EXISTS(SELECT 1 FROM wde.deliveries d
           WHERE d.workspace_id=e.workspace_id AND d.event_id=e.id)
    UNION ALL SELECT a.created_at+make_interval(days=>r.audit_retention_days)
      FROM wde.audit_events a CROSS JOIN wde.restore_control r
     WHERE r.singleton AND a.created_at+make_interval(days=>r.audit_retention_days)<=transaction_timestamp()
    UNION ALL SELECT expires_at FROM wde.rate_limit_buckets
     WHERE expires_at<=transaction_timestamp()
    UNION ALL SELECT scheduled_at FROM wde.maintenance_jobs
     WHERE job_type='purge_workspace' AND (status='pending'
        OR (status='processing' AND lease_expires_at<=transaction_timestamp()))
)
SELECT count(*),min(expired_at) FROM expired
$function$;

ALTER FUNCTION wde.retention_backlog_v5_stage() OWNER TO wde_maintenance_executor;
REVOKE ALL ON FUNCTION wde.retention_backlog_v5_stage() FROM PUBLIC,wde_api,wde_worker,wde_admin;
SET ROLE wde_owner;
REVOKE CREATE ON SCHEMA wde FROM wde_maintenance_executor;
RESET ROLE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET ROLE wde_owner;
GRANT CREATE ON SCHEMA wde TO wde_maintenance_executor;
SET ROLE wde_maintenance_executor;
DROP FUNCTION wde.retention_backlog_v5_stage();
SET ROLE wde_owner;
REVOKE CREATE ON SCHEMA wde FROM wde_maintenance_executor;
RESET ROLE;
-- +goose StatementEnd
