-- +goose Up
-- +goose StatementBegin
SET lock_timeout = '5s';
SET statement_timeout = '30s';
SET ROLE wde_owner;
GRANT CREATE ON SCHEMA wde TO wde_replay_executor;

SET ROLE wde_replay_executor;
CREATE FUNCTION wde.request_replay_v4_stage(
    p_command_id uuid, p_audit_id uuid, p_workspace_id uuid, p_delivery_id uuid,
    p_actor_id text, p_idempotency_key_hash bytea, p_fingerprint bytea,
    p_fingerprint_version smallint, p_reason text, p_request_id text
)
RETURNS TABLE (command_id uuid, new_run_number integer, duplicate boolean)
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog
AS $function$
DECLARE
    prior record;
    target record;
    next_run integer;
BEGIN
    IF p_command_id IS NULL OR p_audit_id IS NULL OR p_workspace_id IS NULL OR p_delivery_id IS NULL
       OR char_length(p_actor_id) NOT BETWEEN 1 AND 128
       OR octet_length(p_idempotency_key_hash) <> 32 OR octet_length(p_fingerprint) <> 32
       OR p_fingerprint_version <> 1 OR char_length(p_reason) NOT BETWEEN 1 AND 500
       OR p_reason ~ '[[:cntrl:]]' OR char_length(p_request_id) NOT BETWEEN 1 AND 128 THEN
        RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'replay_invalid';
    END IF;

    PERFORM pg_advisory_xact_lock(
        hashtextextended(p_workspace_id::text || ':' || encode(p_idempotency_key_hash, 'hex'), 0)
    );

    SELECT c.id, c.delivery_id, c.fingerprint, c.fingerprint_version, c.new_run_number
      INTO prior
     FROM wde.replay_commands c
     WHERE c.workspace_id = p_workspace_id
       AND c.idempotency_key_hash = p_idempotency_key_hash;
    IF FOUND THEN
        IF prior.delivery_id <> p_delivery_id OR prior.fingerprint_version <> p_fingerprint_version
           OR prior.fingerprint <> p_fingerprint THEN
            RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'replay_conflict';
        END IF;
        RETURN QUERY SELECT prior.id, prior.new_run_number, true;
        RETURN;
    END IF;

    SELECT d.status, d.run_number, ev.payload_ciphertext, ev.payload_purged_at
      INTO target
      FROM wde.deliveries d
      JOIN wde.events ev ON ev.workspace_id = d.workspace_id AND ev.id = d.event_id
     WHERE d.workspace_id = p_workspace_id AND d.id = p_delivery_id
     FOR UPDATE OF d;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'replay_not_found';
    END IF;
    IF target.payload_ciphertext IS NULL OR target.payload_purged_at IS NOT NULL THEN
        RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'replay_payload_purged';
    END IF;
    IF target.status NOT IN ('failed_permanent', 'dead_letter') THEN
        RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'replay_invalid_transition';
    END IF;
    IF target.run_number = 2147483647 THEN
        RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'replay_invalid_transition';
    END IF;
    next_run := target.run_number + 1;

    INSERT INTO wde.replay_commands
        (id, workspace_id, delivery_id, actor_type, actor_id, idempotency_key_hash,
         fingerprint, fingerprint_version, new_run_number, reason, request_id, result)
    VALUES
        (p_command_id, p_workspace_id, p_delivery_id, 'api_key', p_actor_id,
         p_idempotency_key_hash, p_fingerprint, p_fingerprint_version,
         next_run, p_reason, p_request_id, 'accepted');

    UPDATE wde.deliveries d
       SET status = 'pending', run_number = next_run, attempts_in_run = 0,
           next_attempt_at = clock_timestamp(), lease_owner = NULL, lease_expires_at = NULL,
           last_error_category = NULL, succeeded_at = NULL, terminal_at = NULL,
           updated_at = clock_timestamp()
     WHERE d.workspace_id = p_workspace_id AND d.id = p_delivery_id;

    PERFORM wde.append_audit_event(
        p_audit_id, p_workspace_id, 'api_key', p_actor_id, 'delivery.replay',
        'delivery', p_delivery_id::text, p_request_id, 'accepted', p_reason
    );
    RETURN QUERY SELECT p_command_id, next_run, false;
END
$function$;
ALTER FUNCTION wde.request_replay_v4_stage(uuid, uuid, uuid, uuid, text, bytea, bytea, smallint, text, text)
    OWNER TO wde_replay_executor;
REVOKE ALL ON FUNCTION wde.request_replay_v4_stage(uuid, uuid, uuid, uuid, text, bytea, bytea, smallint, text, text)
    FROM PUBLIC, wde_api, wde_admin, wde_worker;

SET ROLE wde_owner;
REVOKE CREATE ON SCHEMA wde FROM wde_replay_executor;
RESET ROLE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET lock_timeout = '5s';
SET statement_timeout = '30s';
SET ROLE wde_owner;
GRANT CREATE ON SCHEMA wde TO wde_replay_executor;
SET ROLE wde_replay_executor;
DROP FUNCTION wde.request_replay_v4_stage(uuid, uuid, uuid, uuid, text, bytea, bytea, smallint, text, text);
SET ROLE wde_owner;
REVOKE CREATE ON SCHEMA wde FROM wde_replay_executor;
RESET ROLE;
-- +goose StatementEnd
