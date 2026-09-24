-- +goose Up
-- +goose StatementBegin
SET lock_timeout = '5s';
SET statement_timeout = '30s';
SET ROLE wde_owner;
GRANT CREATE ON SCHEMA wde TO wde_worker_executor;
SET ROLE wde_worker_executor;

CREATE FUNCTION wde.delivery_queue_metrics()
RETURNS TABLE(ready_count bigint,oldest_seconds double precision)
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path=pg_catalog SET statement_timeout='2s'
AS $function$
    WITH eligible AS MATERIALIZED (
        SELECT CASE WHEN d.status='processing' THEN d.lease_expires_at ELSE d.next_attempt_at END ready_at
          FROM wde.deliveries d
          JOIN wde.workspaces w ON w.id=d.workspace_id AND w.status='active'
          JOIN wde.endpoints ep ON ep.workspace_id=d.workspace_id AND ep.id=d.endpoint_id AND ep.status='active'
          JOIN wde.events ev ON ev.workspace_id=d.workspace_id AND ev.id=d.event_id
            AND ev.payload_ciphertext IS NOT NULL
         WHERE d.attempts_in_run<d.max_attempts_per_run
           AND EXISTS(SELECT 1 FROM wde.endpoint_secret_versions sv
             WHERE sv.workspace_id=d.workspace_id AND sv.endpoint_id=d.endpoint_id
               AND sv.state='active' AND sv.secret_ciphertext IS NOT NULL)
           AND ((d.status IN('pending','retry_scheduled') AND d.next_attempt_at<=statement_timestamp())
             OR (d.status='processing' AND d.lease_expires_at<=statement_timestamp()))
    )
    SELECT count(*)::bigint,
        COALESCE(GREATEST(EXTRACT(EPOCH FROM statement_timestamp()-min(ready_at)),0),0)::double precision
      FROM eligible
$function$;

ALTER FUNCTION wde.delivery_queue_metrics() OWNER TO wde_worker_executor;
REVOKE ALL ON FUNCTION wde.delivery_queue_metrics() FROM PUBLIC,wde_api,wde_admin;
GRANT EXECUTE ON FUNCTION wde.delivery_queue_metrics() TO wde_worker;
SET ROLE wde_owner;
REVOKE CREATE ON SCHEMA wde FROM wde_worker_executor;
RESET ROLE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET lock_timeout = '5s';
SET statement_timeout = '30s';
SET ROLE wde_owner;
REVOKE EXECUTE ON FUNCTION wde.delivery_queue_metrics() FROM wde_worker;
GRANT CREATE ON SCHEMA wde TO wde_worker_executor;
SET ROLE wde_worker_executor;
DROP FUNCTION wde.delivery_queue_metrics();
SET ROLE wde_owner;
REVOKE CREATE ON SCHEMA wde FROM wde_worker_executor;
RESET ROLE;
-- +goose StatementEnd
