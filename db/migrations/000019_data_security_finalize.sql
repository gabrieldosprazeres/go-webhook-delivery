-- +goose Up
-- +goose StatementBegin
SET lock_timeout='5s';
SET statement_timeout='30s';
SET ROLE wde_owner;
GRANT CREATE ON SCHEMA wde TO wde_audit_executor,wde_worker_executor;
SET ROLE wde_audit_executor;
CREATE FUNCTION wde.append_audit_event_v5_stage(
    p_id uuid,p_workspace_id uuid,p_actor_type text,p_actor_id text,p_action text,
    p_resource_type text,p_resource_id text,p_request_id text,p_outcome text,p_reason text DEFAULT NULL
)
RETURNS void LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog
AS $function$
BEGIN
    IF p_id IS NULL OR p_actor_type IS NULL OR p_actor_id IS NULL OR p_action IS NULL
       OR p_resource_type IS NULL OR p_resource_id IS NULL OR p_request_id IS NULL OR p_outcome IS NULL
       OR p_actor_type NOT IN('api_key','admin_cli','system')
       OR p_action NOT IN('credential.bootstrap','credential.revoke','endpoint.create','endpoint.secret_rotate',
          'delivery.replay','payload.purge','secret.purge','workspace.purge','restore.quarantine','restore.reconcile')
       OR p_resource_type NOT IN('workspace','api_key','endpoint','delivery','event','secret')
       OR p_outcome NOT IN('accepted','revoked','completed')
       OR char_length(p_actor_id) NOT BETWEEN 1 AND 128 OR char_length(p_resource_id) NOT BETWEEN 1 AND 128
       OR char_length(p_request_id) NOT BETWEEN 1 AND 128
       OR (p_reason IS NOT NULL AND (char_length(p_reason) NOT BETWEEN 1 AND 500 OR p_reason~'[[:cntrl:]]')) THEN
        RAISE EXCEPTION 'invalid audit event';
    END IF;
    INSERT INTO wde.audit_events(id,workspace_id,actor_type,actor_id,action,resource_type,
        resource_id,request_id,outcome,reason) VALUES(p_id,p_workspace_id,p_actor_type,p_actor_id,
        p_action,p_resource_type,p_resource_id,p_request_id,p_outcome,p_reason);
END
$function$;
ALTER FUNCTION wde.append_audit_event_v5_stage(uuid,uuid,text,text,text,text,text,text,text,text)
    OWNER TO wde_audit_executor;
REVOKE ALL ON FUNCTION wde.append_audit_event_v5_stage(uuid,uuid,text,text,text,text,text,text,text,text)
    FROM PUBLIC,wde_api,wde_admin,wde_worker,wde_rotation_executor,wde_maintenance_executor;

SET ROLE wde_owner;
ALTER FUNCTION wde.append_audit_event(uuid,uuid,text,text,text,text,text,text,text,text)
    RENAME TO append_audit_event_v4;
REVOKE EXECUTE ON FUNCTION wde.append_audit_event_v4(uuid,uuid,text,text,text,text,text,text,text,text)
    FROM wde_api,wde_admin,wde_replay_executor,wde_rotation_executor,wde_maintenance_executor;
ALTER FUNCTION wde.append_audit_event_v5_stage(uuid,uuid,text,text,text,text,text,text,text,text)
    RENAME TO append_audit_event;
GRANT EXECUTE ON FUNCTION wde.append_audit_event(uuid,uuid,text,text,text,text,text,text,text,text)
    TO wde_api,wde_admin,wde_replay_executor,wde_rotation_executor,wde_maintenance_executor;

ALTER FUNCTION wde.rotate_endpoint_secret_v5_stage(uuid,uuid,uuid,uuid,uuid,text,smallint,bytea,bytea,smallint,text,text,bytea,bytea,integer)
    RENAME TO rotate_endpoint_secret;
GRANT EXECUTE ON FUNCTION wde.rotate_endpoint_secret(uuid,uuid,uuid,uuid,uuid,text,smallint,bytea,bytea,smallint,text,text,bytea,bytea,integer) TO wde_api;
ALTER FUNCTION wde.purge_expired_payloads_v5_stage(integer) RENAME TO purge_expired_payloads;
ALTER FUNCTION wde.purge_retired_secrets_v5_stage(integer) RENAME TO purge_retired_secrets;
ALTER FUNCTION wde.purge_expired_metadata_v5_stage(integer) RENAME TO purge_expired_metadata;
ALTER FUNCTION wde.retention_backlog_v5_stage() RENAME TO retention_backlog;
ALTER FUNCTION wde.request_workspace_purge_v5_stage(uuid,text) RENAME TO request_workspace_purge;
ALTER FUNCTION wde.purge_workspace_v5_stage(integer) RENAME TO purge_workspace;
ALTER FUNCTION wde.enter_restore_quarantine_v5_stage(bigint) RENAME TO enter_restore_quarantine;
ALTER FUNCTION wde.complete_restore_reconciliation_v5_stage(bigint) RENAME TO complete_restore_reconciliation;
GRANT EXECUTE ON FUNCTION wde.purge_expired_payloads(integer),wde.purge_retired_secrets(integer),
    wde.purge_expired_metadata(integer),wde.retention_backlog(),wde.purge_workspace(integer) TO wde_worker;
GRANT EXECUTE ON FUNCTION wde.request_workspace_purge(uuid,text),wde.enter_restore_quarantine(bigint),
    wde.complete_restore_reconciliation(bigint) TO wde_admin;

SET ROLE wde_worker_executor;
ALTER FUNCTION wde.claim_deliveries(uuid,uuid[],interval,integer,integer,integer)
    RENAME TO claim_deliveries_v4;
REVOKE EXECUTE ON FUNCTION wde.claim_deliveries_v4(uuid,uuid[],interval,integer,integer,integer) FROM wde_worker;
ALTER FUNCTION wde.claim_deliveries_v5_stage(uuid,uuid[],interval,integer,integer,integer)
    RENAME TO claim_deliveries;
GRANT EXECUTE ON FUNCTION wde.claim_deliveries(uuid,uuid[],interval,integer,integer,integer) TO wde_worker;

SET ROLE wde_owner;
CREATE FUNCTION wde.runtime_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path=pg_catalog AS 'SELECT state=''ready'' FROM wde.restore_control WHERE singleton';
REVOKE ALL ON FUNCTION wde.runtime_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION wde.runtime_ready() TO wde_api,wde_worker,wde_admin;
REVOKE CREATE ON SCHEMA wde FROM wde_auth_executor,wde_audit_executor,wde_worker_executor,
    wde_quota_executor,wde_replay_executor,wde_rotation_executor,wde_maintenance_executor;
UPDATE wde.schema_metadata SET schema_version=5 WHERE singleton;
RESET ROLE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET ROLE wde_owner;
DO $guard$
BEGIN
    IF EXISTS(SELECT 1 FROM wde.endpoints WHERE target_cipher_format_version=2)
       OR EXISTS(SELECT 1 FROM wde.events WHERE payload_cipher_format_version=2)
       OR EXISTS(SELECT 1 FROM wde.endpoint_secret_versions WHERE cipher_format_version=2 OR state='purged')
       OR EXISTS(SELECT 1 FROM wde.secret_rotation_commands)
       OR EXISTS(SELECT 1 FROM wde.workspace_tombstones)
       OR EXISTS(SELECT 1 FROM wde.audit_events WHERE action IN
          ('endpoint.secret_rotate','payload.purge','secret.purge','workspace.purge','restore.quarantine','restore.reconcile')) THEN
        RAISE EXCEPTION 'unsafe downgrade: remove or re-encrypt v5 data first';
    END IF;
END
$guard$;
UPDATE wde.schema_metadata SET schema_version=4 WHERE singleton;
DROP FUNCTION wde.runtime_ready();
GRANT CREATE ON SCHEMA wde TO wde_worker_executor;
SET ROLE wde_worker_executor;
REVOKE EXECUTE ON FUNCTION wde.claim_deliveries(uuid,uuid[],interval,integer,integer,integer) FROM wde_worker;
ALTER FUNCTION wde.claim_deliveries(uuid,uuid[],interval,integer,integer,integer) RENAME TO claim_deliveries_v5_stage;
ALTER FUNCTION wde.claim_deliveries_v4(uuid,uuid[],interval,integer,integer,integer) RENAME TO claim_deliveries;
GRANT EXECUTE ON FUNCTION wde.claim_deliveries(uuid,uuid[],interval,integer,integer,integer) TO wde_worker;
SET ROLE wde_owner;
REVOKE EXECUTE ON FUNCTION wde.enter_restore_quarantine(bigint),wde.complete_restore_reconciliation(bigint),
    wde.request_workspace_purge(uuid,text) FROM wde_admin;
REVOKE EXECUTE ON FUNCTION wde.purge_expired_payloads(integer),wde.purge_retired_secrets(integer),
    wde.purge_expired_metadata(integer),wde.retention_backlog(),wde.purge_workspace(integer) FROM wde_worker;
ALTER FUNCTION wde.enter_restore_quarantine(bigint) RENAME TO enter_restore_quarantine_v5_stage;
ALTER FUNCTION wde.complete_restore_reconciliation(bigint) RENAME TO complete_restore_reconciliation_v5_stage;
ALTER FUNCTION wde.purge_workspace(integer) RENAME TO purge_workspace_v5_stage;
ALTER FUNCTION wde.request_workspace_purge(uuid,text) RENAME TO request_workspace_purge_v5_stage;
ALTER FUNCTION wde.retention_backlog() RENAME TO retention_backlog_v5_stage;
ALTER FUNCTION wde.purge_expired_metadata(integer) RENAME TO purge_expired_metadata_v5_stage;
ALTER FUNCTION wde.purge_retired_secrets(integer) RENAME TO purge_retired_secrets_v5_stage;
ALTER FUNCTION wde.purge_expired_payloads(integer) RENAME TO purge_expired_payloads_v5_stage;
REVOKE EXECUTE ON FUNCTION wde.rotate_endpoint_secret(uuid,uuid,uuid,uuid,uuid,text,smallint,bytea,bytea,smallint,text,text,bytea,bytea,integer) FROM wde_api;
ALTER FUNCTION wde.rotate_endpoint_secret(uuid,uuid,uuid,uuid,uuid,text,smallint,bytea,bytea,smallint,text,text,bytea,bytea,integer)
    RENAME TO rotate_endpoint_secret_v5_stage;
REVOKE EXECUTE ON FUNCTION wde.append_audit_event(uuid,uuid,text,text,text,text,text,text,text,text)
    FROM wde_api,wde_admin,wde_replay_executor,wde_rotation_executor,wde_maintenance_executor;
ALTER FUNCTION wde.append_audit_event(uuid,uuid,text,text,text,text,text,text,text,text) RENAME TO append_audit_event_v5_stage;
ALTER FUNCTION wde.append_audit_event_v4(uuid,uuid,text,text,text,text,text,text,text,text) RENAME TO append_audit_event;
GRANT EXECUTE ON FUNCTION wde.append_audit_event(uuid,uuid,text,text,text,text,text,text,text,text)
    TO wde_api,wde_admin,wde_replay_executor;
GRANT CREATE ON SCHEMA wde TO wde_audit_executor;
SET ROLE wde_audit_executor;
DROP FUNCTION wde.append_audit_event_v5_stage(uuid,uuid,text,text,text,text,text,text,text,text);
SET ROLE wde_owner;
REVOKE CREATE ON SCHEMA wde FROM wde_audit_executor;
REVOKE CREATE ON SCHEMA wde FROM wde_worker_executor;
RESET ROLE;
-- +goose StatementEnd
