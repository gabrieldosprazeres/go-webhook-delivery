-- +goose Up
-- +goose StatementBegin
SET lock_timeout='5s';
SET statement_timeout='30s';
SET ROLE wde_owner;
GRANT CREATE ON SCHEMA wde TO wde_rotation_executor;
GRANT EXECUTE ON FUNCTION wde.append_audit_event(uuid,uuid,text,text,text,text,text,text,text,text)
    TO wde_rotation_executor;

SET ROLE wde_rotation_executor;
CREATE FUNCTION wde.rotate_endpoint_secret_v5_stage(
    p_command_id uuid, p_audit_id uuid, p_workspace_id uuid, p_endpoint_id uuid,
    p_secret_version_id uuid, p_key_id text, p_format smallint, p_ciphertext bytea,
    p_nonce bytea, p_kek smallint, p_actor_id text, p_request_id text,
    p_idempotency_hash bytea, p_fingerprint bytea, p_overlap_seconds integer
)
RETURNS TABLE(secret_version_id uuid,key_id text,cipher_format_version smallint,
    secret_ciphertext bytea,secret_nonce bytea,kek_version smallint,duplicate boolean)
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog
AS $function$
DECLARE prior record; active_secret record; expired_secret record;
BEGIN
    IF p_command_id IS NULL OR p_audit_id IS NULL OR p_workspace_id IS NULL OR p_endpoint_id IS NULL
       OR p_secret_version_id IS NULL OR p_key_id IS NULL OR char_length(p_key_id) NOT BETWEEN 8 AND 64
       OR p_format IS NULL OR p_ciphertext IS NULL OR p_nonce IS NULL OR p_kek IS NULL
       OR p_format NOT IN(1,2) OR octet_length(p_ciphertext)<16 OR octet_length(p_nonce)<>12 OR p_kek<=0
       OR p_actor_id IS NULL OR p_request_id IS NULL OR p_idempotency_hash IS NULL OR p_fingerprint IS NULL
       OR char_length(p_actor_id) NOT BETWEEN 1 AND 128 OR char_length(p_request_id) NOT BETWEEN 1 AND 128
       OR octet_length(p_idempotency_hash)<>32 OR octet_length(p_fingerprint)<>32
       OR p_overlap_seconds IS NULL OR p_overlap_seconds NOT BETWEEN 3600 AND 604800 THEN
        RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='rotation_invalid';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended(p_workspace_id::text||':'||p_endpoint_id::text||':'||encode(p_idempotency_hash,'hex'),0));
    SELECT c.secret_version_id,c.key_id,c.fingerprint,s.state,s.cipher_format_version,
        s.secret_ciphertext,s.secret_nonce,s.kek_version
      INTO prior FROM wde.secret_rotation_commands c
      JOIN wde.endpoint_secret_versions s ON s.workspace_id=c.workspace_id AND s.id=c.secret_version_id
     WHERE c.workspace_id=p_workspace_id AND c.endpoint_id=p_endpoint_id
       AND c.idempotency_key_hash=p_idempotency_hash FOR UPDATE OF s;
    IF FOUND THEN
        IF prior.fingerprint<>p_fingerprint THEN
            RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='rotation_conflict';
        END IF;
        IF prior.state='purged' THEN
            RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='rotation_result_expired';
        END IF;
        RETURN QUERY SELECT prior.secret_version_id,prior.key_id,prior.cipher_format_version,
            prior.secret_ciphertext,prior.secret_nonce,prior.kek_version,true;
        RETURN;
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended(p_workspace_id::text||':'||p_endpoint_id::text,1));
    PERFORM 1 FROM wde.endpoints e WHERE e.workspace_id=p_workspace_id AND e.id=p_endpoint_id
        AND e.status='active';
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='rotation_not_found'; END IF;
    IF EXISTS(SELECT 1 FROM wde.endpoint_secret_versions s WHERE s.workspace_id=p_workspace_id
        AND s.endpoint_id=p_endpoint_id AND s.state='retiring' AND s.retire_at>transaction_timestamp()) THEN
        RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='rotation_in_progress';
    END IF;
    SELECT id INTO expired_secret FROM wde.endpoint_secret_versions
     WHERE workspace_id=p_workspace_id AND endpoint_id=p_endpoint_id AND state='retiring'
     FOR UPDATE;
    IF FOUND THEN
        UPDATE wde.endpoint_secret_versions SET state='purged',cipher_format_version=NULL,
            secret_ciphertext=NULL,secret_nonce=NULL,kek_version=NULL,purged_at=transaction_timestamp()
         WHERE workspace_id=p_workspace_id AND id=expired_secret.id;
        PERFORM wde.append_audit_event(gen_random_uuid(),p_workspace_id,'system','retention',
            'secret.purge','secret',expired_secret.id::text,p_request_id,'completed',NULL);
    END IF;
    SELECT id INTO active_secret FROM wde.endpoint_secret_versions s
     WHERE s.workspace_id=p_workspace_id AND s.endpoint_id=p_endpoint_id AND s.state='active' FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='rotation_no_active_secret'; END IF;
    UPDATE wde.endpoint_secret_versions SET state='retiring',retire_at=transaction_timestamp()+make_interval(secs=>p_overlap_seconds)
     WHERE workspace_id=p_workspace_id AND id=active_secret.id;
    INSERT INTO wde.endpoint_secret_versions(id,workspace_id,endpoint_id,key_id,state,cipher_format_version,
        secret_ciphertext,secret_nonce,kek_version)
    VALUES(p_secret_version_id,p_workspace_id,p_endpoint_id,p_key_id,'active',p_format,p_ciphertext,p_nonce,p_kek);
    INSERT INTO wde.secret_rotation_commands(id,workspace_id,endpoint_id,secret_version_id,actor_id,
        idempotency_key_hash,fingerprint,key_id,overlap_seconds)
    VALUES(p_command_id,p_workspace_id,p_endpoint_id,p_secret_version_id,p_actor_id,
        p_idempotency_hash,p_fingerprint,p_key_id,p_overlap_seconds);
    PERFORM wde.append_audit_event(p_audit_id,p_workspace_id,'api_key',p_actor_id,
        'endpoint.secret_rotate','secret',p_secret_version_id::text,p_request_id,'accepted',NULL);
    RETURN QUERY SELECT p_secret_version_id,p_key_id,p_format,p_ciphertext,p_nonce,p_kek,false;
END
$function$;
ALTER FUNCTION wde.rotate_endpoint_secret_v5_stage(uuid,uuid,uuid,uuid,uuid,text,smallint,bytea,bytea,smallint,text,text,bytea,bytea,integer)
    OWNER TO wde_rotation_executor;
REVOKE ALL ON FUNCTION wde.rotate_endpoint_secret_v5_stage(uuid,uuid,uuid,uuid,uuid,text,smallint,bytea,bytea,smallint,text,text,bytea,bytea,integer)
    FROM PUBLIC,wde_api,wde_admin,wde_worker;
SET ROLE wde_owner;
REVOKE CREATE ON SCHEMA wde FROM wde_rotation_executor;
RESET ROLE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET ROLE wde_owner;
GRANT CREATE ON SCHEMA wde TO wde_rotation_executor;
SET ROLE wde_rotation_executor;
DROP FUNCTION wde.rotate_endpoint_secret_v5_stage(uuid,uuid,uuid,uuid,uuid,text,smallint,bytea,bytea,smallint,text,text,bytea,bytea,integer);
SET ROLE wde_owner;
REVOKE EXECUTE ON FUNCTION wde.append_audit_event(uuid,uuid,text,text,text,text,text,text,text,text) FROM wde_rotation_executor;
REVOKE CREATE ON SCHEMA wde FROM wde_rotation_executor;
RESET ROLE;
-- +goose StatementEnd
