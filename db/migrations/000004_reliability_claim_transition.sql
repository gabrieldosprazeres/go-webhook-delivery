-- +goose Up
-- +goose StatementBegin
SET lock_timeout = '5s';
SET statement_timeout = '30s';
SET ROLE wde_owner;
GRANT CREATE ON SCHEMA wde TO wde_worker_executor;

CREATE FUNCTION wde.recover_exhausted_deliveries(p_limit integer)
RETURNS integer
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog
AS $function$
DECLARE
    exhausted record;
    recovered integer := 0;
BEGIN
    IF p_limit IS NULL OR p_limit < 1 OR p_limit > 100 THEN
        RAISE EXCEPTION 'invalid recovery limit';
    END IF;
    FOR exhausted IN
        SELECT d.id, d.workspace_id, d.fencing_token
          FROM wde.deliveries d
         WHERE d.attempts_in_run >= d.max_attempts_per_run
           AND ((d.status IN ('pending', 'retry_scheduled') AND d.next_attempt_at <= statement_timestamp())
             OR (d.status = 'processing' AND d.lease_expires_at <= statement_timestamp()))
         ORDER BY d.next_attempt_at, d.created_at, d.id
         FOR UPDATE SKIP LOCKED
         LIMIT p_limit
    LOOP
        UPDATE wde.delivery_attempts a
           SET state = 'abandoned', outcome = 'stale', finished_at = clock_timestamp(),
               error_category = 'lease_expired'
         WHERE a.workspace_id = exhausted.workspace_id AND a.delivery_id = exhausted.id
           AND a.fencing_token = exhausted.fencing_token AND a.state = 'started';
        UPDATE wde.deliveries d
           SET status = 'dead_letter', lease_owner = NULL, lease_expires_at = NULL,
               last_error_category = COALESCE(d.last_error_category, 'attempts_exhausted'),
               terminal_at = clock_timestamp(), updated_at = clock_timestamp()
         WHERE d.id = exhausted.id;
        recovered := recovered + 1;
    END LOOP;
    RETURN recovered;
END
$function$;
ALTER FUNCTION wde.recover_exhausted_deliveries(integer) OWNER TO wde_worker_executor;
REVOKE ALL ON FUNCTION wde.recover_exhausted_deliveries(integer) FROM PUBLIC, wde_worker;

CREATE FUNCTION wde.claim_one_delivery(
    p_worker_id uuid, p_attempt_id uuid, p_lease_ttl interval,
    p_workspace_limit integer, p_endpoint_limit integer,
    p_workspace_claims jsonb, p_endpoint_claims jsonb
)
RETURNS TABLE (
    workspace_id uuid, delivery_id uuid, event_id uuid, endpoint_id uuid,
    fencing_token bigint, attempt_number smallint, max_attempts smallint,
    scheme text, host_ascii text, port integer, target_cipher_format_version smallint,
    target_ciphertext bytea, target_nonce bytea, target_kek_version smallint,
    event_type text, payload_cipher_format_version smallint, payload_ciphertext bytea,
    payload_nonce bytea, payload_kek_version smallint, key_id text,
    secret_version_id uuid, secret_cipher_format_version smallint, secret_ciphertext bytea,
    secret_nonce bytea, secret_kek_version smallint
)
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog
AS $function$
DECLARE
    selected record;
    candidate record;
    active_count integer;
    next_sequence bigint;
    new_fencing_token bigint;
    new_attempt_number smallint;
    new_attempt_sequence integer;
BEGIN
    SELECT * INTO selected FROM wde.select_delivery_candidate(
        p_workspace_limit, p_endpoint_limit, p_workspace_claims, p_endpoint_claims
    );
    IF NOT FOUND THEN RETURN; END IF;

    SELECT d.id AS delivery_id, d.workspace_id, d.event_id, d.endpoint_id,
           d.status AS delivery_status, d.fencing_token, d.run_number,
           d.max_attempts_per_run, ep.max_concurrency, ep.scheme, ep.host_ascii, ep.port,
           ep.target_cipher_format_version, ep.target_ciphertext, ep.target_nonce, ep.target_kek_version,
           ev.event_type, ev.payload_cipher_format_version, ev.payload_ciphertext,
           ev.payload_nonce, ev.payload_kek_version, sv.key_id, sv.id AS secret_version_id,
           sv.cipher_format_version AS secret_cipher_format_version,
           sv.secret_ciphertext, sv.secret_nonce, sv.kek_version AS secret_kek_version
      INTO candidate
      FROM wde.deliveries d
      JOIN wde.endpoints ep ON ep.workspace_id = d.workspace_id AND ep.id = d.endpoint_id AND ep.status = 'active'
      JOIN wde.events ev ON ev.workspace_id = d.workspace_id AND ev.id = d.event_id AND ev.payload_ciphertext IS NOT NULL
      JOIN wde.endpoint_secret_versions sv ON sv.workspace_id = d.workspace_id AND sv.endpoint_id = d.endpoint_id
       AND sv.state = 'active' AND sv.secret_ciphertext IS NOT NULL
     WHERE d.workspace_id = selected.workspace_id AND d.id = selected.delivery_id
       AND d.endpoint_id = selected.endpoint_id;
    IF NOT FOUND THEN RETURN; END IF;

    SELECT count(*)::integer INTO active_count FROM wde.deliveries d
     WHERE d.workspace_id = candidate.workspace_id AND d.endpoint_id = candidate.endpoint_id
       AND d.status = 'processing' AND d.lease_expires_at > clock_timestamp();
    IF active_count >= candidate.max_concurrency THEN RETURN; END IF;
    UPDATE wde.deliveries d
       SET status = 'processing', lease_owner = p_worker_id,
           lease_expires_at = clock_timestamp() + p_lease_ttl,
           fencing_token = d.fencing_token + 1, attempts_in_run = d.attempts_in_run + 1,
           attempt_sequence = d.attempt_sequence + 1, updated_at = clock_timestamp()
     WHERE d.workspace_id = candidate.workspace_id AND d.id = candidate.delivery_id
       AND d.attempts_in_run < d.max_attempts_per_run
       AND ((d.status IN ('pending', 'retry_scheduled') AND d.next_attempt_at <= clock_timestamp())
         OR (d.status = 'processing' AND d.lease_expires_at <= clock_timestamp()))
    RETURNING d.fencing_token, d.attempts_in_run, d.attempt_sequence
         INTO new_fencing_token, new_attempt_number, new_attempt_sequence;
    IF NOT FOUND THEN RETURN; END IF;

    IF candidate.delivery_status = 'processing' THEN
        UPDATE wde.delivery_attempts a
           SET state = 'abandoned', outcome = 'stale', finished_at = clock_timestamp(),
               error_category = 'lease_expired'
         WHERE a.workspace_id = candidate.workspace_id AND a.delivery_id = candidate.delivery_id
           AND a.fencing_token = candidate.fencing_token AND a.state = 'started';
    END IF;

    INSERT INTO wde.delivery_attempts
        (id, workspace_id, delivery_id, run_number, attempt_number_in_run, attempt_sequence, fencing_token)
    VALUES (p_attempt_id, candidate.workspace_id, candidate.delivery_id, candidate.run_number,
            new_attempt_number, new_attempt_sequence, new_fencing_token);
    next_sequence := nextval('wde.delivery_claim_sequence'::regclass);
    UPDATE wde.workspaces w SET last_delivery_claim_sequence = next_sequence
     WHERE w.id = candidate.workspace_id;
    UPDATE wde.endpoint_runtime rt
       SET last_delivery_claim_sequence = next_sequence, lock_version = rt.lock_version + 1,
           updated_at = clock_timestamp()
     WHERE rt.workspace_id = candidate.workspace_id AND rt.endpoint_id = candidate.endpoint_id;

    RETURN QUERY SELECT candidate.workspace_id, candidate.delivery_id, candidate.event_id,
        candidate.endpoint_id, new_fencing_token, new_attempt_number, candidate.max_attempts_per_run,
        candidate.scheme, candidate.host_ascii, candidate.port, candidate.target_cipher_format_version,
        candidate.target_ciphertext, candidate.target_nonce, candidate.target_kek_version,
        candidate.event_type, candidate.payload_cipher_format_version, candidate.payload_ciphertext,
        candidate.payload_nonce, candidate.payload_kek_version, candidate.key_id, candidate.secret_version_id,
        candidate.secret_cipher_format_version, candidate.secret_ciphertext, candidate.secret_nonce,
        candidate.secret_kek_version;
END
$function$;
ALTER FUNCTION wde.claim_one_delivery(uuid, uuid, interval, integer, integer, jsonb, jsonb)
    OWNER TO wde_worker_executor;
REVOKE ALL ON FUNCTION wde.claim_one_delivery(uuid, uuid, interval, integer, integer, jsonb, jsonb)
    FROM PUBLIC, wde_worker;
COMMENT ON FUNCTION wde.claim_one_delivery(uuid, uuid, interval, integer, integer, jsonb, jsonb)
    IS 'Internal atomic transition after nonblocking candidate selection';

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
DROP FUNCTION wde.claim_one_delivery(uuid, uuid, interval, integer, integer, jsonb, jsonb);
DROP FUNCTION wde.recover_exhausted_deliveries(integer);
SET ROLE wde_owner;
REVOKE CREATE ON SCHEMA wde FROM wde_worker_executor;
RESET ROLE;
-- +goose StatementEnd
