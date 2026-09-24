-- +goose Up
-- +goose StatementBegin
SET lock_timeout = '5s';
SET statement_timeout = '30s';
SET ROLE wde_owner;
GRANT CREATE ON SCHEMA wde TO wde_quota_executor, wde_audit_executor;

SET ROLE wde_audit_executor;
CREATE FUNCTION wde.append_audit_event_v4_stage(
    p_id uuid, p_workspace_id uuid, p_actor_type text, p_actor_id text,
    p_action text, p_resource_type text, p_resource_id text,
    p_request_id text, p_outcome text, p_reason text DEFAULT NULL
)
RETURNS void
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog
AS $function$
BEGIN
    IF p_id IS NULL OR p_actor_type IS NULL OR p_actor_id IS NULL OR p_action IS NULL
       OR p_resource_type IS NULL OR p_resource_id IS NULL OR p_request_id IS NULL OR p_outcome IS NULL
       OR p_actor_type NOT IN ('api_key', 'admin_cli', 'system')
       OR p_action NOT IN ('credential.bootstrap', 'credential.revoke', 'endpoint.create', 'delivery.replay')
       OR p_resource_type NOT IN ('workspace', 'api_key', 'endpoint', 'delivery')
       OR p_outcome NOT IN ('accepted', 'revoked')
       OR char_length(p_actor_id) NOT BETWEEN 1 AND 128
       OR char_length(p_resource_id) NOT BETWEEN 1 AND 128
       OR char_length(p_request_id) NOT BETWEEN 1 AND 128
       OR (p_reason IS NOT NULL AND (char_length(p_reason) NOT BETWEEN 1 AND 500
           OR p_reason ~ '[[:cntrl:]]')) THEN
        RAISE EXCEPTION 'invalid audit event';
    END IF;
    INSERT INTO wde.audit_events
        (id, workspace_id, actor_type, actor_id, action, resource_type,
         resource_id, request_id, outcome, reason)
    VALUES
        (p_id, p_workspace_id, p_actor_type, p_actor_id, p_action, p_resource_type,
         p_resource_id, p_request_id, p_outcome, p_reason);
END
$function$;
ALTER FUNCTION wde.append_audit_event_v4_stage(uuid, uuid, text, text, text, text, text, text, text, text)
    OWNER TO wde_audit_executor;
REVOKE ALL ON FUNCTION wde.append_audit_event_v4_stage(uuid, uuid, text, text, text, text, text, text, text, text)
    FROM PUBLIC, wde_api, wde_admin, wde_worker;

SET ROLE wde_quota_executor;
CREATE FUNCTION wde.consume_quota_v4_stage(
    p_dimension_hash bytea, p_dimension_type text, p_operation text,
    p_workspace_id uuid, p_api_key_id uuid, p_resource_id uuid,
    p_limit integer, p_window_seconds integer
)
RETURNS TABLE (allowed boolean, retry_after_seconds integer)
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog
AS $function$
DECLARE
    bucket_start timestamptz;
    bucket_expiry timestamptz;
    updated_count integer;
BEGIN
    IF p_dimension_hash IS NULL OR p_dimension_type IS NULL OR p_operation IS NULL
       OR p_limit IS NULL OR p_window_seconds IS NULL OR octet_length(p_dimension_hash) <> 32
       OR p_dimension_type NOT IN ('global', 'workspace', 'api_key', 'delivery')
       OR p_operation NOT IN ('ingest', 'endpoint_write', 'query', 'replay')
       OR p_limit NOT BETWEEN 1 AND 1000000
       OR p_window_seconds NOT BETWEEN 1 AND 3600
       OR (p_dimension_type = 'global' AND (p_workspace_id IS NOT NULL OR p_api_key_id IS NOT NULL OR p_resource_id IS NOT NULL))
       OR (p_dimension_type = 'workspace' AND (p_workspace_id IS NULL OR p_api_key_id IS NOT NULL OR p_resource_id IS NOT NULL))
       OR (p_dimension_type = 'api_key' AND (p_workspace_id IS NULL OR p_api_key_id IS NULL OR p_resource_id IS NOT NULL))
       OR (p_dimension_type = 'delivery' AND (p_workspace_id IS NULL OR p_api_key_id IS NOT NULL OR p_resource_id IS NULL)) THEN
        RAISE EXCEPTION 'invalid quota request';
    END IF;

    bucket_start := to_timestamp(
        floor(extract(epoch FROM transaction_timestamp()) / p_window_seconds) * p_window_seconds
    );
    bucket_expiry := bucket_start + make_interval(secs => p_window_seconds);

    INSERT INTO wde.rate_limit_buckets
        (dimension_hash, dimension_type, operation, window_started_at,
         workspace_id, api_key_id, resource_id, count, expires_at)
    VALUES
        (p_dimension_hash, p_dimension_type, p_operation, bucket_start,
         p_workspace_id, p_api_key_id, p_resource_id, 1, bucket_expiry)
    ON CONFLICT (dimension_hash, operation, window_started_at)
    DO UPDATE SET count = wde.rate_limit_buckets.count + 1
      WHERE wde.rate_limit_buckets.count < p_limit
        AND wde.rate_limit_buckets.dimension_type = p_dimension_type
        AND wde.rate_limit_buckets.workspace_id IS NOT DISTINCT FROM p_workspace_id
        AND wde.rate_limit_buckets.api_key_id IS NOT DISTINCT FROM p_api_key_id
        AND wde.rate_limit_buckets.resource_id IS NOT DISTINCT FROM p_resource_id
        AND wde.rate_limit_buckets.expires_at = bucket_expiry
    RETURNING count INTO updated_count;

    DELETE FROM wde.rate_limit_buckets
     WHERE ctid IN (
        SELECT ctid FROM wde.rate_limit_buckets
         WHERE expires_at <= transaction_timestamp()
         ORDER BY expires_at LIMIT 100
     );

    IF updated_count IS NULL THEN
        RETURN QUERY SELECT false,
            GREATEST(1, CEIL(extract(epoch FROM (bucket_expiry - transaction_timestamp())))::integer);
        RETURN;
    END IF;
    RETURN QUERY SELECT true, 0;
END
$function$;
ALTER FUNCTION wde.consume_quota_v4_stage(bytea, text, text, uuid, uuid, uuid, integer, integer)
    OWNER TO wde_quota_executor;
REVOKE ALL ON FUNCTION wde.consume_quota_v4_stage(bytea, text, text, uuid, uuid, uuid, integer, integer)
    FROM PUBLIC, wde_api, wde_admin, wde_worker;

SET ROLE wde_owner;
REVOKE CREATE ON SCHEMA wde FROM wde_quota_executor, wde_audit_executor;
RESET ROLE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET lock_timeout = '5s';
SET statement_timeout = '30s';
SET ROLE wde_owner;
GRANT CREATE ON SCHEMA wde TO wde_quota_executor, wde_audit_executor;
SET ROLE wde_quota_executor;
DROP FUNCTION wde.consume_quota_v4_stage(bytea, text, text, uuid, uuid, uuid, integer, integer);
SET ROLE wde_audit_executor;
DROP FUNCTION wde.append_audit_event_v4_stage(uuid, uuid, text, text, text, text, text, text, text, text);
SET ROLE wde_owner;
REVOKE CREATE ON SCHEMA wde FROM wde_quota_executor, wde_audit_executor;
RESET ROLE;
-- +goose StatementEnd
