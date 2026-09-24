-- +goose Up
-- +goose StatementBegin
SET lock_timeout='5s';
SET statement_timeout='30s';
SET ROLE wde_owner;
GRANT CREATE ON SCHEMA wde TO wde_maintenance_executor;
SET ROLE wde_maintenance_executor;

CREATE FUNCTION wde.request_workspace_purge_v5_stage(p_workspace_id uuid,p_request_id text)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog
AS $function$
DECLARE current_status text; next_generation bigint;
BEGIN
    IF p_workspace_id IS NULL OR p_request_id IS NULL OR char_length(p_request_id) NOT BETWEEN 1 AND 128 THEN
        RAISE EXCEPTION 'invalid workspace purge request';
    END IF;
    SELECT status INTO current_status FROM wde.workspaces WHERE id=p_workspace_id FOR UPDATE;
    IF NOT FOUND THEN RETURN false; END IF;
    IF current_status='deleting' THEN RETURN false; END IF;

    -- Serialize with every endpoint rotation. A rotation that already owns a
    -- lock commits first and becomes visible to the initial purge checkpoint;
    -- a later rotation observes the disabled endpoint and fails closed.
    PERFORM pg_advisory_xact_lock(hashtextextended(p_workspace_id::text||':'||e.id::text,1))
      FROM wde.endpoints e WHERE e.workspace_id=p_workspace_id ORDER BY e.id;

    UPDATE wde.workspaces SET status='deleting',
        deletion_requested_at=COALESCE(deletion_requested_at,transaction_timestamp()),
        updated_at=transaction_timestamp() WHERE id=p_workspace_id;
    UPDATE wde.api_keys SET status='revoked',revoked_at=COALESCE(revoked_at,transaction_timestamp())
     WHERE workspace_id=p_workspace_id AND status='active';
    UPDATE wde.endpoints SET status='disabled',updated_at=transaction_timestamp()
     WHERE workspace_id=p_workspace_id AND status<>'disabled';
    UPDATE wde.delivery_attempts a SET state='abandoned',outcome='stale',error_category='workspace_deleting',
        finished_at=transaction_timestamp() FROM wde.deliveries d
     WHERE d.workspace_id=p_workspace_id AND a.workspace_id=d.workspace_id
       AND a.delivery_id=d.id AND a.state='started';
    UPDATE wde.deliveries SET status='failed_permanent',fencing_token=fencing_token+1,
        lease_owner=NULL,lease_expires_at=NULL,last_error_category='workspace_deleting',
        terminal_at=transaction_timestamp(),updated_at=transaction_timestamp()
     WHERE workspace_id=p_workspace_id AND status='processing';

    SELECT COALESCE(generation,0)+1 INTO next_generation FROM wde.workspace_tombstones
     WHERE workspace_id=p_workspace_id FOR UPDATE;
    IF NOT FOUND THEN next_generation:=1; END IF;
    INSERT INTO wde.workspace_tombstones(workspace_id,generation)
    VALUES(p_workspace_id,next_generation)
    ON CONFLICT(workspace_id) DO UPDATE SET generation=EXCLUDED.generation,
        deleted_at=transaction_timestamp(),purge_completed_at=NULL;
    INSERT INTO wde.maintenance_jobs(id,workspace_id,job_type,deduplication_key)
    VALUES(gen_random_uuid(),p_workspace_id,'purge_workspace',p_workspace_id::text)
    ON CONFLICT(job_type,deduplication_key) DO UPDATE SET status='pending',
        checkpoint='replay_commands',processed_rows=0,scheduled_at=transaction_timestamp(),
        lease_owner=NULL,lease_expires_at=NULL,updated_at=transaction_timestamp();
    INSERT INTO wde.audit_events(id,workspace_id,actor_type,actor_id,action,resource_type,
        resource_id,request_id,outcome) VALUES(gen_random_uuid(),p_workspace_id,'admin_cli','workspace-purge',
        'workspace.purge','workspace',p_workspace_id::text,p_request_id,'accepted');
    RETURN true;
END
$function$;

ALTER FUNCTION wde.request_workspace_purge_v5_stage(uuid,text) OWNER TO wde_maintenance_executor;
REVOKE ALL ON FUNCTION wde.request_workspace_purge_v5_stage(uuid,text)
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
DROP FUNCTION wde.request_workspace_purge_v5_stage(uuid,text);
SET ROLE wde_owner;
REVOKE CREATE ON SCHEMA wde FROM wde_maintenance_executor;
RESET ROLE;
-- +goose StatementEnd
