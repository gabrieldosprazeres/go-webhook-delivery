-- +goose Up
-- +goose StatementBegin
SET lock_timeout='5s';
SET statement_timeout='30s';
SET ROLE wde_owner;
GRANT CREATE ON SCHEMA wde TO wde_worker_executor;
SET LOCAL check_function_bodies=false;
SET ROLE wde_worker_executor;

CREATE FUNCTION wde.claim_deliveries_v5_stage(
    p_worker_id uuid,p_attempt_ids uuid[],p_lease_ttl interval,
    p_limit integer,p_workspace_limit integer,p_endpoint_limit integer
)
RETURNS TABLE(
    workspace_id uuid,delivery_id uuid,event_id uuid,endpoint_id uuid,
    fencing_token bigint,attempt_number smallint,max_attempts smallint,
    scheme text,host_ascii text,port integer,target_cipher_format_version smallint,
    target_ciphertext bytea,target_nonce bytea,target_kek_version smallint,
    event_type text,payload_cipher_format_version smallint,payload_ciphertext bytea,
    payload_nonce bytea,payload_kek_version smallint,key_id text,
    secret_version_id uuid,secret_cipher_format_version smallint,secret_ciphertext bytea,
    secret_nonce bytea,secret_kek_version smallint,retiring_key_id text,
    retiring_secret_version_id uuid,retiring_cipher_format_version smallint,
    retiring_secret_ciphertext bytea,retiring_secret_nonce bytea,retiring_kek_version smallint
)
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog
AS $function$
    SELECT c.workspace_id,c.delivery_id,c.event_id,c.endpoint_id,c.fencing_token,
        c.attempt_number,c.max_attempts,c.scheme,c.host_ascii,c.port,
        c.target_cipher_format_version,c.target_ciphertext,c.target_nonce,c.target_kek_version,
        c.event_type,c.payload_cipher_format_version,c.payload_ciphertext,c.payload_nonce,
        c.payload_kek_version,c.key_id,c.secret_version_id,c.secret_cipher_format_version,
        c.secret_ciphertext,c.secret_nonce,c.secret_kek_version,r.key_id,r.id,
        r.cipher_format_version,r.secret_ciphertext,r.secret_nonce,r.kek_version
      FROM wde.claim_deliveries_v4(p_worker_id,p_attempt_ids,p_lease_ttl,p_limit,p_workspace_limit,p_endpoint_limit) c
      LEFT JOIN LATERAL(
          SELECT s.id,s.key_id,s.cipher_format_version,s.secret_ciphertext,s.secret_nonce,s.kek_version
            FROM wde.endpoint_secret_versions s
           WHERE s.workspace_id=c.workspace_id AND s.endpoint_id=c.endpoint_id
             AND s.state='retiring' AND s.retire_at>transaction_timestamp()
           ORDER BY s.retire_at DESC,s.id DESC LIMIT 1
      ) r ON true
$function$;
ALTER FUNCTION wde.claim_deliveries_v5_stage(uuid,uuid[],interval,integer,integer,integer)
    OWNER TO wde_worker_executor;
REVOKE ALL ON FUNCTION wde.claim_deliveries_v5_stage(uuid,uuid[],interval,integer,integer,integer)
    FROM PUBLIC,wde_worker;
SET ROLE wde_owner;
REVOKE CREATE ON SCHEMA wde FROM wde_worker_executor;
RESET ROLE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET ROLE wde_owner;
GRANT CREATE ON SCHEMA wde TO wde_worker_executor;
SET ROLE wde_worker_executor;
DROP FUNCTION wde.claim_deliveries_v5_stage(uuid,uuid[],interval,integer,integer,integer);
SET ROLE wde_owner;
REVOKE CREATE ON SCHEMA wde FROM wde_worker_executor;
RESET ROLE;
-- +goose StatementEnd
