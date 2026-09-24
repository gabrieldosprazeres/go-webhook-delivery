-- +goose Up
-- +goose StatementBegin
SET lock_timeout='5s';
SET statement_timeout='30s';
SET ROLE wde_owner;
GRANT CREATE ON SCHEMA wde TO wde_maintenance_executor;
SET ROLE wde_maintenance_executor;

CREATE FUNCTION wde.purge_workspace_v5_stage(p_batch integer)
RETURNS TABLE(workspace_id uuid,deleted integer,completed boolean,checkpoint text)
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog SET statement_timeout='5s'
AS $function$
DECLARE job record; owner_id uuid; token bigint; affected integer:=0; next_step text; more boolean;
BEGIN
    IF p_batch IS NULL OR p_batch NOT BETWEEN 1 AND 1000 THEN RAISE EXCEPTION 'invalid workspace purge batch'; END IF;
    SELECT j.id,j.workspace_id,j.checkpoint INTO job FROM wde.maintenance_jobs j
     WHERE j.job_type='purge_workspace' AND j.scheduled_at<=transaction_timestamp()
       AND (j.status='pending' OR (j.status='processing' AND j.lease_expires_at<=transaction_timestamp()))
     ORDER BY j.scheduled_at,j.id FOR UPDATE OF j SKIP LOCKED LIMIT 1;
    IF NOT FOUND THEN RETURN; END IF;
    owner_id:=gen_random_uuid();
    UPDATE wde.maintenance_jobs SET status='processing',lease_owner=owner_id,
        lease_expires_at=transaction_timestamp()+interval '30 seconds',fencing_token=fencing_token+1,
        attempts=attempts+1,updated_at=transaction_timestamp() WHERE id=job.id RETURNING fencing_token INTO token;
    -- Recompute the earliest dependency on every lease. This preserves the
    -- durable checkpoint while also recovering if an older transaction makes
    -- a child visible after a previous stage was observed empty.
    next_step:=CASE
        WHEN EXISTS(SELECT 1 FROM wde.replay_commands x WHERE x.workspace_id=job.workspace_id) THEN 'replay_commands'
        WHEN EXISTS(SELECT 1 FROM wde.secret_rotation_commands x WHERE x.workspace_id=job.workspace_id) THEN 'rotation_commands'
        WHEN EXISTS(SELECT 1 FROM wde.delivery_attempts x WHERE x.workspace_id=job.workspace_id) THEN 'attempts'
        WHEN EXISTS(SELECT 1 FROM wde.deliveries x WHERE x.workspace_id=job.workspace_id) THEN 'deliveries'
        WHEN EXISTS(SELECT 1 FROM wde.events x WHERE x.workspace_id=job.workspace_id) THEN 'events'
        WHEN EXISTS(SELECT 1 FROM wde.endpoint_subscriptions x WHERE x.workspace_id=job.workspace_id) THEN 'subscriptions'
        WHEN EXISTS(SELECT 1 FROM wde.endpoint_runtime x WHERE x.workspace_id=job.workspace_id) THEN 'runtime'
        WHEN EXISTS(SELECT 1 FROM wde.endpoint_secret_versions x WHERE x.workspace_id=job.workspace_id) THEN 'secrets'
        WHEN EXISTS(SELECT 1 FROM wde.endpoints x WHERE x.workspace_id=job.workspace_id) THEN 'endpoints'
        WHEN EXISTS(SELECT 1 FROM wde.api_key_scopes x WHERE x.workspace_id=job.workspace_id) THEN 'scopes'
        WHEN EXISTS(SELECT 1 FROM wde.api_keys x WHERE x.workspace_id=job.workspace_id) THEN 'keys'
        WHEN EXISTS(SELECT 1 FROM wde.rate_limit_buckets x WHERE x.workspace_id=job.workspace_id) THEN 'buckets'
        ELSE 'workspace'
    END;

    IF next_step='replay_commands' THEN
        DELETE FROM wde.replay_commands r WHERE r.id IN (SELECT x.id FROM wde.replay_commands x
            WHERE x.workspace_id=job.workspace_id ORDER BY x.id FOR UPDATE OF x SKIP LOCKED LIMIT p_batch);
        GET DIAGNOSTICS affected=ROW_COUNT;
        SELECT EXISTS(SELECT 1 FROM wde.replay_commands x WHERE x.workspace_id=job.workspace_id) INTO more;
        IF NOT more THEN next_step:='rotation_commands'; END IF;
    ELSIF next_step='rotation_commands' THEN
        DELETE FROM wde.secret_rotation_commands r WHERE r.id IN (SELECT x.id FROM wde.secret_rotation_commands x
            WHERE x.workspace_id=job.workspace_id ORDER BY x.id FOR UPDATE OF x SKIP LOCKED LIMIT p_batch);
        GET DIAGNOSTICS affected=ROW_COUNT;
        SELECT EXISTS(SELECT 1 FROM wde.secret_rotation_commands x WHERE x.workspace_id=job.workspace_id) INTO more;
        IF NOT more THEN next_step:='attempts'; END IF;
    ELSIF next_step='attempts' THEN
        DELETE FROM wde.delivery_attempts a WHERE a.id IN (SELECT x.id FROM wde.delivery_attempts x
            WHERE x.workspace_id=job.workspace_id ORDER BY x.id FOR UPDATE OF x SKIP LOCKED LIMIT p_batch);
        GET DIAGNOSTICS affected=ROW_COUNT;
        SELECT EXISTS(SELECT 1 FROM wde.delivery_attempts x WHERE x.workspace_id=job.workspace_id) INTO more;
        IF NOT more THEN next_step:='deliveries'; END IF;
    ELSIF next_step='deliveries' THEN
        DELETE FROM wde.deliveries d WHERE d.id IN (SELECT x.id FROM wde.deliveries x
            WHERE x.workspace_id=job.workspace_id ORDER BY x.id FOR UPDATE OF x SKIP LOCKED LIMIT p_batch);
        GET DIAGNOSTICS affected=ROW_COUNT;
        SELECT EXISTS(SELECT 1 FROM wde.deliveries x WHERE x.workspace_id=job.workspace_id) INTO more;
        IF NOT more THEN next_step:='events'; END IF;
    ELSIF next_step='events' THEN
        DELETE FROM wde.events e WHERE e.id IN (SELECT x.id FROM wde.events x
            WHERE x.workspace_id=job.workspace_id ORDER BY x.id FOR UPDATE OF x SKIP LOCKED LIMIT p_batch);
        GET DIAGNOSTICS affected=ROW_COUNT;
        SELECT EXISTS(SELECT 1 FROM wde.events x WHERE x.workspace_id=job.workspace_id) INTO more;
        IF NOT more THEN next_step:='subscriptions'; END IF;
    ELSIF next_step='subscriptions' THEN
        DELETE FROM wde.endpoint_subscriptions s WHERE s.ctid IN (SELECT x.ctid FROM wde.endpoint_subscriptions x
            WHERE x.workspace_id=job.workspace_id FOR UPDATE OF x SKIP LOCKED LIMIT p_batch);
        GET DIAGNOSTICS affected=ROW_COUNT;
        SELECT EXISTS(SELECT 1 FROM wde.endpoint_subscriptions x WHERE x.workspace_id=job.workspace_id) INTO more;
        IF NOT more THEN next_step:='runtime'; END IF;
    ELSIF next_step='runtime' THEN
        DELETE FROM wde.endpoint_runtime r WHERE r.ctid IN (SELECT x.ctid FROM wde.endpoint_runtime x
            WHERE x.workspace_id=job.workspace_id FOR UPDATE OF x SKIP LOCKED LIMIT p_batch);
        GET DIAGNOSTICS affected=ROW_COUNT;
        SELECT EXISTS(SELECT 1 FROM wde.endpoint_runtime x WHERE x.workspace_id=job.workspace_id) INTO more;
        IF NOT more THEN next_step:='secrets'; END IF;
    ELSIF next_step='secrets' THEN
        DELETE FROM wde.endpoint_secret_versions s WHERE s.id IN (SELECT x.id FROM wde.endpoint_secret_versions x
            WHERE x.workspace_id=job.workspace_id ORDER BY x.id FOR UPDATE OF x SKIP LOCKED LIMIT p_batch);
        GET DIAGNOSTICS affected=ROW_COUNT;
        SELECT EXISTS(SELECT 1 FROM wde.endpoint_secret_versions x WHERE x.workspace_id=job.workspace_id) INTO more;
        IF NOT more THEN next_step:='endpoints'; END IF;
    ELSIF next_step='endpoints' THEN
        DELETE FROM wde.endpoints e WHERE e.id IN (SELECT x.id FROM wde.endpoints x
            WHERE x.workspace_id=job.workspace_id ORDER BY x.id FOR UPDATE OF x SKIP LOCKED LIMIT p_batch);
        GET DIAGNOSTICS affected=ROW_COUNT;
        SELECT EXISTS(SELECT 1 FROM wde.endpoints x WHERE x.workspace_id=job.workspace_id) INTO more;
        IF NOT more THEN next_step:='scopes'; END IF;
    ELSIF next_step='scopes' THEN
        DELETE FROM wde.api_key_scopes s WHERE s.ctid IN (SELECT x.ctid FROM wde.api_key_scopes x
            WHERE x.workspace_id=job.workspace_id FOR UPDATE OF x SKIP LOCKED LIMIT p_batch);
        GET DIAGNOSTICS affected=ROW_COUNT;
        SELECT EXISTS(SELECT 1 FROM wde.api_key_scopes x WHERE x.workspace_id=job.workspace_id) INTO more;
        IF NOT more THEN next_step:='keys'; END IF;
    ELSIF next_step='keys' THEN
        DELETE FROM wde.api_keys k WHERE k.id IN (SELECT x.id FROM wde.api_keys x
            WHERE x.workspace_id=job.workspace_id ORDER BY x.id FOR UPDATE OF x SKIP LOCKED LIMIT p_batch);
        GET DIAGNOSTICS affected=ROW_COUNT;
        SELECT EXISTS(SELECT 1 FROM wde.api_keys x WHERE x.workspace_id=job.workspace_id) INTO more;
        IF NOT more THEN next_step:='buckets'; END IF;
    ELSIF next_step='buckets' THEN
        DELETE FROM wde.rate_limit_buckets b WHERE b.ctid IN (SELECT x.ctid FROM wde.rate_limit_buckets x
            WHERE x.workspace_id=job.workspace_id FOR UPDATE OF x SKIP LOCKED LIMIT p_batch);
        GET DIAGNOSTICS affected=ROW_COUNT;
        SELECT EXISTS(SELECT 1 FROM wde.rate_limit_buckets x WHERE x.workspace_id=job.workspace_id) INTO more;
        IF NOT more THEN next_step:='workspace'; END IF;
    ELSIF next_step='workspace' THEN
        DELETE FROM wde.workspaces WHERE id=job.workspace_id;
        GET DIAGNOSTICS affected=ROW_COUNT;
        next_step:='complete';
    ELSE
        RAISE EXCEPTION 'invalid workspace purge checkpoint';
    END IF;

    IF next_step='complete' THEN
        UPDATE wde.workspace_tombstones t SET purge_completed_at=transaction_timestamp()
         WHERE t.workspace_id=job.workspace_id;
        INSERT INTO wde.audit_events(id,workspace_id,actor_type,actor_id,action,resource_type,
            resource_id,request_id,outcome) VALUES(gen_random_uuid(),job.workspace_id,'system','retention',
            'workspace.purge','workspace',job.workspace_id::text,'maintenance','completed');
        UPDATE wde.maintenance_jobs SET status='completed',checkpoint='complete',processed_rows=processed_rows+affected,
            lease_owner=NULL,lease_expires_at=NULL,updated_at=transaction_timestamp()
         WHERE id=job.id AND lease_owner=owner_id AND fencing_token=token AND lease_expires_at>transaction_timestamp();
    ELSE
        UPDATE wde.maintenance_jobs SET status='pending',checkpoint=next_step,processed_rows=processed_rows+affected,
            scheduled_at=transaction_timestamp(),lease_owner=NULL,lease_expires_at=NULL,updated_at=transaction_timestamp()
         WHERE id=job.id AND lease_owner=owner_id AND fencing_token=token AND lease_expires_at>transaction_timestamp();
    END IF;
    IF NOT FOUND THEN RAISE EXCEPTION 'workspace purge lease lost'; END IF;
    RETURN QUERY SELECT job.workspace_id,affected,next_step='complete',next_step;
END
$function$;

ALTER FUNCTION wde.purge_workspace_v5_stage(integer) OWNER TO wde_maintenance_executor;
REVOKE ALL ON FUNCTION wde.purge_workspace_v5_stage(integer) FROM PUBLIC,wde_api,wde_worker,wde_admin;
SET ROLE wde_owner;
REVOKE CREATE ON SCHEMA wde FROM wde_maintenance_executor;
RESET ROLE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET ROLE wde_owner;
GRANT CREATE ON SCHEMA wde TO wde_maintenance_executor;
SET ROLE wde_maintenance_executor;
DROP FUNCTION wde.purge_workspace_v5_stage(integer);
SET ROLE wde_owner;
REVOKE CREATE ON SCHEMA wde FROM wde_maintenance_executor;
RESET ROLE;
-- +goose StatementEnd
