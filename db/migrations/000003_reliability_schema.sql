-- +goose Up
-- +goose StatementBegin
SET lock_timeout = '5s';
SET statement_timeout = '30s';
SET ROLE wde_owner;

ALTER TABLE wde.workspaces
    ADD COLUMN last_delivery_claim_sequence bigint NOT NULL DEFAULT 0 CHECK (last_delivery_claim_sequence >= 0);
ALTER TABLE wde.endpoint_runtime
    ADD COLUMN last_delivery_claim_sequence bigint NOT NULL DEFAULT 0 CHECK (last_delivery_claim_sequence >= 0);

GRANT CREATE ON SCHEMA wde TO wde_worker_executor;
CREATE SEQUENCE wde.delivery_claim_sequence AS bigint MINVALUE 1 NO CYCLE;
ALTER SEQUENCE wde.delivery_claim_sequence OWNER TO wde_worker_executor;
REVOKE ALL ON SEQUENCE wde.delivery_claim_sequence FROM PUBLIC, wde_api, wde_worker, wde_admin;

GRANT SELECT ON wde.workspaces TO wde_worker_executor;
GRANT UPDATE(last_delivery_claim_sequence) ON wde.workspaces TO wde_worker_executor;
GRANT SELECT ON wde.endpoint_runtime TO wde_worker_executor;
GRANT UPDATE(lock_version, last_delivery_claim_sequence, updated_at) ON wde.endpoint_runtime TO wde_worker_executor;
CREATE POLICY workspaces_worker ON wde.workspaces FOR SELECT TO wde_worker_executor USING (status = 'active');
CREATE POLICY workspaces_worker_fairness ON wde.workspaces FOR UPDATE TO wde_worker_executor
    USING (status = 'active') WITH CHECK (status = 'active');
CREATE POLICY endpoint_runtime_worker ON wde.endpoint_runtime TO wde_worker_executor USING (true) WITH CHECK (true);

CREATE FUNCTION wde.select_delivery_candidate(
    p_workspace_limit integer, p_endpoint_limit integer,
    p_workspace_claims jsonb, p_endpoint_claims jsonb
)
RETURNS TABLE (delivery_id uuid, workspace_id uuid, endpoint_id uuid)
LANGUAGE sql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog
AS $function$
    WITH eligible AS MATERIALIZED (
        SELECT d.id FROM wde.deliveries d
         WHERE d.status IN ('pending', 'retry_scheduled') AND d.next_attempt_at <= statement_timestamp()
           AND d.attempts_in_run < d.max_attempts_per_run
        UNION ALL
        SELECT d.id FROM wde.deliveries d
         WHERE d.status = 'processing' AND d.lease_expires_at <= statement_timestamp()
           AND d.attempts_in_run < d.max_attempts_per_run
    )
    SELECT d.id, d.workspace_id, d.endpoint_id
      FROM eligible ready
      JOIN wde.deliveries d ON d.id = ready.id
      JOIN wde.workspaces w ON w.id = d.workspace_id AND w.status = 'active'
      JOIN wde.endpoints ep ON ep.workspace_id = d.workspace_id AND ep.id = d.endpoint_id AND ep.status = 'active'
      JOIN wde.endpoint_runtime rt ON rt.workspace_id = d.workspace_id AND rt.endpoint_id = d.endpoint_id
      JOIN wde.events ev ON ev.workspace_id = d.workspace_id AND ev.id = d.event_id AND ev.payload_ciphertext IS NOT NULL
      JOIN wde.endpoint_secret_versions sv ON sv.workspace_id = d.workspace_id AND sv.endpoint_id = d.endpoint_id
       AND sv.state = 'active' AND sv.secret_ciphertext IS NOT NULL
     WHERE d.attempts_in_run < d.max_attempts_per_run
       AND ((d.status IN ('pending', 'retry_scheduled') AND d.next_attempt_at <= statement_timestamp())
         OR (d.status = 'processing' AND d.lease_expires_at <= statement_timestamp()))
       AND COALESCE((p_workspace_claims ->> d.workspace_id::text)::integer, 0) < p_workspace_limit
       AND COALESCE((p_endpoint_claims ->>
           (d.workspace_id::text || ':' || d.endpoint_id::text))::integer, 0) < p_endpoint_limit
       AND (SELECT count(*) FROM wde.deliveries active
             WHERE active.workspace_id = d.workspace_id AND active.endpoint_id = d.endpoint_id
               AND active.status = 'processing'
               AND active.lease_expires_at > clock_timestamp()) < ep.max_concurrency
     ORDER BY w.last_delivery_claim_sequence, rt.last_delivery_claim_sequence,
              d.next_attempt_at, d.created_at, d.workspace_id, d.endpoint_id, d.id
     FOR UPDATE OF w, d, rt SKIP LOCKED
     LIMIT 1
$function$;
ALTER FUNCTION wde.select_delivery_candidate(integer, integer, jsonb, jsonb) OWNER TO wde_worker_executor;
REVOKE ALL ON FUNCTION wde.select_delivery_candidate(integer, integer, jsonb, jsonb) FROM PUBLIC, wde_worker;
COMMENT ON FUNCTION wde.select_delivery_candidate(integer, integer, jsonb, jsonb)
    IS 'Internal indexed nonblocking selection; locks only workspace, endpoint runtime and delivery';

CREATE FUNCTION wde.finalize_delivery_v3_stage(
    p_workspace_id uuid, p_delivery_id uuid, p_worker_id uuid, p_fencing_token bigint,
    p_disposition text, p_http_status smallint, p_duration_ms integer,
    p_error_category text, p_retry_delay interval
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
    IF p_http_status IS NOT NULL AND (p_http_status < 100 OR p_http_status > 599) THEN
        p_http_status := NULL;
        p_disposition := 'permanent_failure';
        p_error_category := 'invalid_http_status';
        p_retry_delay := NULL;
    END IF;
    IF p_disposition IS NULL OR p_disposition NOT IN ('success', 'retry', 'permanent_failure') THEN
        RAISE EXCEPTION 'invalid disposition';
    END IF;
    IF p_disposition = 'retry' AND (p_retry_delay IS NULL OR p_retry_delay < interval '0 seconds'
       OR p_retry_delay > interval '15 minutes') THEN
        RAISE EXCEPTION 'invalid retry delay';
    END IF;

    UPDATE wde.deliveries d
       SET status = CASE
               WHEN p_disposition = 'success' THEN 'succeeded'
               WHEN p_disposition = 'permanent_failure' THEN 'failed_permanent'
               WHEN d.attempts_in_run >= d.max_attempts_per_run THEN 'dead_letter'
               ELSE 'retry_scheduled'
           END,
           next_attempt_at = CASE WHEN p_disposition = 'retry'
               AND d.attempts_in_run < d.max_attempts_per_run
               THEN clock_timestamp() + p_retry_delay ELSE d.next_attempt_at END,
           lease_owner = NULL, lease_expires_at = NULL,
           last_error_category = CASE WHEN p_disposition = 'success' THEN NULL ELSE left(p_error_category, 64) END,
           succeeded_at = CASE WHEN p_disposition = 'success' THEN clock_timestamp() ELSE NULL END,
           terminal_at = CASE WHEN p_disposition IN ('success', 'permanent_failure')
               OR d.attempts_in_run >= d.max_attempts_per_run THEN clock_timestamp() ELSE NULL END,
           updated_at = clock_timestamp()
     WHERE d.workspace_id = p_workspace_id AND d.id = p_delivery_id
       AND d.status = 'processing' AND d.lease_owner = p_worker_id
       AND d.fencing_token = p_fencing_token AND d.lease_expires_at > clock_timestamp();
    GET DIAGNOSTICS changed = ROW_COUNT;
    IF changed = 0 THEN RETURN false; END IF;

    UPDATE wde.delivery_attempts a
       SET state = 'completed', outcome = p_disposition,
           http_status = p_http_status, duration_ms = GREATEST(p_duration_ms, 0),
           response_bytes_read = 0,
           error_category = CASE WHEN p_disposition = 'success' THEN NULL ELSE left(p_error_category, 64) END,
           finished_at = clock_timestamp()
     WHERE a.workspace_id = p_workspace_id AND a.delivery_id = p_delivery_id
       AND a.fencing_token = p_fencing_token AND a.state = 'started';
    GET DIAGNOSTICS changed_attempt = ROW_COUNT;
    IF changed_attempt <> 1 THEN RAISE EXCEPTION 'delivery attempt invariant violated'; END IF;
    RETURN true;
END
$function$;
ALTER FUNCTION wde.finalize_delivery_v3_stage(uuid, uuid, uuid, bigint, text, smallint, integer, text, interval)
    OWNER TO wde_worker_executor;
REVOKE ALL ON FUNCTION wde.finalize_delivery_v3_stage(uuid, uuid, uuid, bigint, text, smallint, integer, text, interval)
    FROM PUBLIC, wde_worker;

REVOKE CREATE ON SCHEMA wde FROM wde_worker_executor;
RESET ROLE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET lock_timeout = '5s';
SET statement_timeout = '30s';
SET ROLE wde_owner;
GRANT CREATE ON SCHEMA wde TO wde_worker_executor;
SET ROLE wde_worker_executor;
DROP FUNCTION wde.finalize_delivery_v3_stage(uuid, uuid, uuid, bigint, text, smallint, integer, text, interval);
DROP FUNCTION wde.select_delivery_candidate(integer, integer, jsonb, jsonb);
DROP SEQUENCE wde.delivery_claim_sequence;
SET ROLE wde_owner;
REVOKE CREATE ON SCHEMA wde FROM wde_worker_executor;
DROP POLICY endpoint_runtime_worker ON wde.endpoint_runtime;
DROP POLICY workspaces_worker_fairness ON wde.workspaces;
DROP POLICY workspaces_worker ON wde.workspaces;
REVOKE SELECT ON wde.workspaces FROM wde_worker_executor;
REVOKE UPDATE(last_delivery_claim_sequence) ON wde.workspaces FROM wde_worker_executor;
REVOKE SELECT ON wde.endpoint_runtime FROM wde_worker_executor;
REVOKE UPDATE(lock_version, last_delivery_claim_sequence, updated_at) ON wde.endpoint_runtime FROM wde_worker_executor;
ALTER TABLE wde.endpoint_runtime DROP COLUMN last_delivery_claim_sequence;
ALTER TABLE wde.workspaces DROP COLUMN last_delivery_claim_sequence;
RESET ROLE;
-- +goose StatementEnd
