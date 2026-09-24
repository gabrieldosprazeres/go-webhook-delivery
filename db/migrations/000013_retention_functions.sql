-- +goose Up
-- +goose StatementBegin
SET lock_timeout='5s';
SET statement_timeout='30s';
SET ROLE wde_owner;
GRANT CREATE ON SCHEMA wde TO wde_maintenance_executor;
GRANT EXECUTE ON FUNCTION wde.append_audit_event(uuid,uuid,text,text,text,text,text,text,text,text)
    TO wde_maintenance_executor;

SET ROLE wde_maintenance_executor;
CREATE FUNCTION wde.purge_expired_payloads_v5_stage(p_batch integer)
RETURNS integer LANGUAGE plpgsql VOLATILE SECURITY DEFINER
    SET search_path=pg_catalog SET statement_timeout='5s'
AS $function$
DECLARE purged integer;
BEGIN
    IF p_batch IS NULL OR p_batch NOT BETWEEN 1 AND 1000 THEN RAISE EXCEPTION 'invalid purge batch'; END IF;
    WITH candidates AS MATERIALIZED (
        SELECT e.workspace_id,e.id FROM wde.events e
         WHERE e.payload_ciphertext IS NOT NULL AND e.payload_expires_at<=transaction_timestamp()
         ORDER BY e.payload_expires_at,e.id FOR UPDATE SKIP LOCKED LIMIT p_batch
    ), abandoned AS (
        UPDATE wde.delivery_attempts a SET state='abandoned',outcome='stale',
            error_category='payload_expired',finished_at=transaction_timestamp()
          FROM wde.deliveries d,candidates c
         WHERE d.workspace_id=c.workspace_id AND d.event_id=c.id
           AND a.workspace_id=d.workspace_id AND a.delivery_id=d.id AND a.state='started'
    ), terminated AS (
        UPDATE wde.deliveries d SET status='failed_permanent',fencing_token=d.fencing_token+1,
            lease_owner=NULL,lease_expires_at=NULL,last_error_category='payload_expired',
            terminal_at=transaction_timestamp(),updated_at=transaction_timestamp()
          FROM candidates c WHERE d.workspace_id=c.workspace_id AND d.event_id=c.id
            AND d.status IN('pending','processing','retry_scheduled')
    ), cleared AS (
        UPDATE wde.events e SET payload_cipher_format_version=NULL,payload_ciphertext=NULL,
            payload_nonce=NULL,payload_kek_version=NULL,payload_purged_at=transaction_timestamp()
          FROM candidates c WHERE e.workspace_id=c.workspace_id AND e.id=c.id RETURNING e.workspace_id,e.id
    ), audited AS (
        INSERT INTO wde.audit_events(id,workspace_id,actor_type,actor_id,action,resource_type,
            resource_id,request_id,outcome)
        SELECT gen_random_uuid(),workspace_id,'system','retention','payload.purge','event',
            id::text,'maintenance','completed' FROM cleared
    ) SELECT count(*) INTO purged FROM cleared;
    RETURN purged;
END
$function$;

CREATE FUNCTION wde.purge_retired_secrets_v5_stage(p_batch integer)
RETURNS integer LANGUAGE plpgsql VOLATILE SECURITY DEFINER
    SET search_path=pg_catalog SET statement_timeout='5s'
AS $function$
DECLARE purged integer;
BEGIN
    IF p_batch IS NULL OR p_batch NOT BETWEEN 1 AND 1000 THEN RAISE EXCEPTION 'invalid purge batch'; END IF;
    WITH candidates AS MATERIALIZED (
        SELECT workspace_id,id FROM wde.endpoint_secret_versions
         WHERE state='retiring' AND retire_at<=transaction_timestamp()
         ORDER BY retire_at,id FOR UPDATE SKIP LOCKED LIMIT p_batch
    ), cleared AS (
        UPDATE wde.endpoint_secret_versions s SET state='purged',cipher_format_version=NULL,
            secret_ciphertext=NULL,secret_nonce=NULL,kek_version=NULL,purged_at=transaction_timestamp()
          FROM candidates c WHERE s.workspace_id=c.workspace_id AND s.id=c.id RETURNING s.workspace_id,s.id
    ), audited AS (
        INSERT INTO wde.audit_events(id,workspace_id,actor_type,actor_id,action,resource_type,
            resource_id,request_id,outcome)
        SELECT gen_random_uuid(),workspace_id,'system','retention','secret.purge','secret',
            id::text,'maintenance','completed' FROM cleared
    ) SELECT count(*) INTO purged FROM cleared;
    RETURN purged;
END
$function$;

CREATE FUNCTION wde.enter_restore_quarantine_v5_stage(p_generation bigint)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog
AS $function$
DECLARE control record;
BEGIN
    IF p_generation IS NULL OR p_generation<=0 THEN RAISE EXCEPTION 'invalid restore generation'; END IF;
    SELECT generation,state INTO control FROM wde.restore_control WHERE singleton FOR UPDATE;
    IF control.generation=p_generation AND control.state='quarantined' THEN RETURN false; END IF;
    IF p_generation<=control.generation THEN RAISE EXCEPTION 'stale restore generation'; END IF;
    UPDATE wde.api_keys SET status='revoked',revoked_at=COALESCE(revoked_at,transaction_timestamp()) WHERE status='active';
    UPDATE wde.endpoint_secret_versions SET state='purged',retire_at=NULL,cipher_format_version=NULL,
        secret_ciphertext=NULL,secret_nonce=NULL,kek_version=NULL,purged_at=transaction_timestamp()
     WHERE state<>'purged';
    UPDATE wde.endpoints SET status='disabled',updated_at=transaction_timestamp() WHERE status<>'disabled';
    UPDATE wde.delivery_attempts SET state='abandoned',outcome='stale',error_category='restore_quarantine',
        finished_at=transaction_timestamp() WHERE state='started';
    UPDATE wde.deliveries SET status='failed_permanent',fencing_token=fencing_token+1,
        lease_owner=NULL,lease_expires_at=NULL,last_error_category='restore_quarantine',
        terminal_at=transaction_timestamp(),updated_at=transaction_timestamp()
     WHERE status IN('pending','processing','retry_scheduled');
    UPDATE wde.workspaces SET status='suspended',updated_at=transaction_timestamp() WHERE status<>'suspended';
    UPDATE wde.restore_control SET state='quarantined',generation=p_generation,
        quarantined_at=transaction_timestamp(),reconciled_at=NULL WHERE singleton;
    INSERT INTO wde.audit_events(id,workspace_id,actor_type,actor_id,action,resource_type,
        resource_id,request_id,outcome,reason) VALUES(gen_random_uuid(),NULL,'system','restore',
        'restore.quarantine','workspace',p_generation::text,'restore','completed','all restored credentials revoked');
    RETURN true;
END
$function$;

CREATE FUNCTION wde.complete_restore_reconciliation_v5_stage(p_generation bigint)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog
AS $function$
BEGIN
    IF p_generation IS NULL OR p_generation<=0 THEN RAISE EXCEPTION 'invalid restore generation'; END IF;
    PERFORM 1 FROM wde.restore_control WHERE singleton AND state='quarantined'
        AND generation=p_generation FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION 'restore generation is not quarantined'; END IF;
    IF EXISTS(SELECT 1 FROM wde.api_keys WHERE status='active')
       OR EXISTS(SELECT 1 FROM wde.endpoints WHERE status='active')
       OR EXISTS(SELECT 1 FROM wde.workspaces WHERE status='active')
       OR EXISTS(SELECT 1 FROM wde.deliveries WHERE status='processing') THEN
        RAISE EXCEPTION 'restore reconciliation is unsafe';
    END IF;
    UPDATE wde.restore_control SET state='ready',reconciled_at=transaction_timestamp() WHERE singleton;
    INSERT INTO wde.audit_events(id,workspace_id,actor_type,actor_id,action,resource_type,
        resource_id,request_id,outcome,reason) VALUES(gen_random_uuid(),NULL,'system','restore',
        'restore.reconcile','workspace',p_generation::text,'restore','completed','quarantine reconciled');
    RETURN true;
END
$function$;

ALTER FUNCTION wde.purge_expired_payloads_v5_stage(integer) OWNER TO wde_maintenance_executor;
ALTER FUNCTION wde.purge_retired_secrets_v5_stage(integer) OWNER TO wde_maintenance_executor;
ALTER FUNCTION wde.enter_restore_quarantine_v5_stage(bigint) OWNER TO wde_maintenance_executor;
ALTER FUNCTION wde.complete_restore_reconciliation_v5_stage(bigint) OWNER TO wde_maintenance_executor;
REVOKE ALL ON FUNCTION wde.purge_expired_payloads_v5_stage(integer),
    wde.purge_retired_secrets_v5_stage(integer),wde.enter_restore_quarantine_v5_stage(bigint),
    wde.complete_restore_reconciliation_v5_stage(bigint)
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
DROP FUNCTION wde.complete_restore_reconciliation_v5_stage(bigint);
DROP FUNCTION wde.enter_restore_quarantine_v5_stage(bigint);
DROP FUNCTION wde.purge_retired_secrets_v5_stage(integer);
DROP FUNCTION wde.purge_expired_payloads_v5_stage(integer);
SET ROLE wde_owner;
REVOKE EXECUTE ON FUNCTION wde.append_audit_event(uuid,uuid,text,text,text,text,text,text,text,text) FROM wde_maintenance_executor;
REVOKE CREATE ON SCHEMA wde FROM wde_maintenance_executor;
RESET ROLE;
-- +goose StatementEnd
