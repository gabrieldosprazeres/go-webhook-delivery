-- +goose Up
-- +goose StatementBegin
SET lock_timeout = '5s';
SET statement_timeout = '30s';
SET ROLE wde_owner;
GRANT CREATE ON SCHEMA wde TO wde_worker_executor;

CREATE FUNCTION wde.claim_deliveries_v3_stage(
    p_worker_id uuid, p_attempt_ids uuid[], p_lease_ttl interval,
    p_limit integer, p_workspace_limit integer, p_endpoint_limit integer
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
    candidate record;
    claimed_count integer := 0;
    consecutive_misses integer := 0;
    workspace_claims jsonb := '{}'::jsonb;
    endpoint_claims jsonb := '{}'::jsonb;
BEGIN
    IF p_worker_id IS NULL OR p_worker_id = '00000000-0000-0000-0000-000000000000'::uuid THEN
        RAISE EXCEPTION 'invalid worker id';
    END IF;
    IF p_lease_ttl IS NULL OR p_lease_ttl < interval '20 seconds' OR p_lease_ttl > interval '2 minutes' THEN
        RAISE EXCEPTION 'invalid lease ttl';
    END IF;
    IF p_limit IS NULL OR p_limit < 1 OR p_limit > 100 OR p_attempt_ids IS NULL
       OR array_ndims(p_attempt_ids) <> 1 OR array_lower(p_attempt_ids, 1) <> 1
       OR array_upper(p_attempt_ids, 1) <> p_limit OR cardinality(p_attempt_ids) <> p_limit THEN
        RAISE EXCEPTION 'invalid claim limit';
    END IF;
    IF p_workspace_limit IS NULL OR p_endpoint_limit IS NULL
       OR p_workspace_limit < 1 OR p_workspace_limit > p_limit
       OR p_endpoint_limit < 1 OR p_endpoint_limit > p_limit THEN
        RAISE EXCEPTION 'invalid fairness limit';
    END IF;
    IF EXISTS (
        SELECT 1 FROM unnest(p_attempt_ids) AS ids(attempt_id)
         GROUP BY attempt_id HAVING attempt_id IS NULL
            OR attempt_id = '00000000-0000-0000-0000-000000000000'::uuid OR count(*) > 1
    ) THEN
        RAISE EXCEPTION 'invalid attempt ids';
    END IF;

    PERFORM wde.recover_exhausted_deliveries(p_limit);
    WHILE claimed_count < p_limit LOOP
        SELECT * INTO candidate FROM wde.claim_one_delivery(
            p_worker_id, p_attempt_ids[claimed_count + 1], p_lease_ttl,
            p_workspace_limit, p_endpoint_limit, workspace_claims, endpoint_claims
        );
        IF NOT FOUND THEN
            consecutive_misses := consecutive_misses + 1;
            IF consecutive_misses >= 3 THEN EXIT; END IF;
            CONTINUE;
        END IF;

        consecutive_misses := 0;
        claimed_count := claimed_count + 1;
        workspace_claims := jsonb_set(workspace_claims, ARRAY[candidate.workspace_id::text],
            to_jsonb(COALESCE((workspace_claims ->> candidate.workspace_id::text)::integer, 0) + 1));
        endpoint_claims := jsonb_set(endpoint_claims,
            ARRAY[candidate.workspace_id::text || ':' || candidate.endpoint_id::text],
            to_jsonb(COALESCE((endpoint_claims ->>
                (candidate.workspace_id::text || ':' || candidate.endpoint_id::text))::integer, 0) + 1));

        RETURN QUERY SELECT candidate.workspace_id, candidate.delivery_id, candidate.event_id,
            candidate.endpoint_id, candidate.fencing_token, candidate.attempt_number, candidate.max_attempts,
            candidate.scheme, candidate.host_ascii, candidate.port, candidate.target_cipher_format_version,
            candidate.target_ciphertext, candidate.target_nonce, candidate.target_kek_version,
            candidate.event_type, candidate.payload_cipher_format_version, candidate.payload_ciphertext,
            candidate.payload_nonce, candidate.payload_kek_version, candidate.key_id, candidate.secret_version_id,
            candidate.secret_cipher_format_version, candidate.secret_ciphertext, candidate.secret_nonce,
            candidate.secret_kek_version;
    END LOOP;
END
$function$;
ALTER FUNCTION wde.claim_deliveries_v3_stage(uuid, uuid[], interval, integer, integer, integer)
    OWNER TO wde_worker_executor;
REVOKE ALL ON FUNCTION wde.claim_deliveries_v3_stage(uuid, uuid[], interval, integer, integer, integer)
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
DROP FUNCTION wde.claim_deliveries_v3_stage(uuid, uuid[], interval, integer, integer, integer);
SET ROLE wde_owner;
REVOKE CREATE ON SCHEMA wde FROM wde_worker_executor;
RESET ROLE;
-- +goose StatementEnd
