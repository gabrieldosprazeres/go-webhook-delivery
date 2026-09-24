-- +goose Up
-- +goose StatementBegin
SET lock_timeout = '5s';
SET statement_timeout = '30s';
SET ROLE wde_owner;
GRANT CREATE ON SCHEMA wde TO wde_worker_executor;
SET ROLE wde_worker_executor;

ALTER FUNCTION wde.claim_delivery(uuid, uuid, interval) RENAME TO claim_delivery_v2;
REVOKE EXECUTE ON FUNCTION wde.claim_delivery_v2(uuid, uuid, interval) FROM wde_worker;
COMMENT ON FUNCTION wde.claim_delivery_v2(uuid, uuid, interval)
    IS 'Inactive v2 implementation retained only for migration downgrade';
ALTER FUNCTION wde.claim_deliveries_v3_stage(uuid, uuid[], interval, integer, integer, integer)
    RENAME TO claim_deliveries;
GRANT EXECUTE ON FUNCTION wde.claim_deliveries(uuid, uuid[], interval, integer, integer, integer)
    TO wde_worker;

ALTER FUNCTION wde.finalize_delivery(uuid, uuid, uuid, bigint, boolean, smallint, integer, text)
    RENAME TO finalize_delivery_v2;
REVOKE EXECUTE ON FUNCTION wde.finalize_delivery_v2(uuid, uuid, uuid, bigint, boolean, smallint, integer, text)
    FROM wde_worker;
COMMENT ON FUNCTION wde.finalize_delivery_v2(uuid, uuid, uuid, bigint, boolean, smallint, integer, text)
    IS 'Inactive v2 implementation retained only for migration downgrade';
ALTER FUNCTION wde.finalize_delivery_v3_stage(uuid, uuid, uuid, bigint, text, smallint, integer, text, interval)
    RENAME TO finalize_delivery;
GRANT EXECUTE ON FUNCTION wde.finalize_delivery(uuid, uuid, uuid, bigint, text, smallint, integer, text, interval)
    TO wde_worker;

SET ROLE wde_owner;
REVOKE CREATE ON SCHEMA wde FROM wde_worker_executor;
UPDATE wde.schema_metadata SET schema_version = 3 WHERE singleton;
RESET ROLE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET lock_timeout = '5s';
SET statement_timeout = '30s';
SET ROLE wde_owner;
GRANT CREATE ON SCHEMA wde TO wde_worker_executor;
SET ROLE wde_worker_executor;

REVOKE EXECUTE ON FUNCTION wde.claim_deliveries(uuid, uuid[], interval, integer, integer, integer)
    FROM wde_worker;
ALTER FUNCTION wde.claim_deliveries(uuid, uuid[], interval, integer, integer, integer)
    RENAME TO claim_deliveries_v3_stage;
ALTER FUNCTION wde.claim_delivery_v2(uuid, uuid, interval) RENAME TO claim_delivery;
GRANT EXECUTE ON FUNCTION wde.claim_delivery(uuid, uuid, interval) TO wde_worker;

REVOKE EXECUTE ON FUNCTION wde.finalize_delivery(uuid, uuid, uuid, bigint, text, smallint, integer, text, interval)
    FROM wde_worker;
ALTER FUNCTION wde.finalize_delivery(uuid, uuid, uuid, bigint, text, smallint, integer, text, interval)
    RENAME TO finalize_delivery_v3_stage;
ALTER FUNCTION wde.finalize_delivery_v2(uuid, uuid, uuid, bigint, boolean, smallint, integer, text)
    RENAME TO finalize_delivery;
GRANT EXECUTE ON FUNCTION wde.finalize_delivery(uuid, uuid, uuid, bigint, boolean, smallint, integer, text)
    TO wde_worker;

SET ROLE wde_owner;
REVOKE CREATE ON SCHEMA wde FROM wde_worker_executor;
UPDATE wde.schema_metadata SET schema_version = 2 WHERE singleton;
RESET ROLE;
-- +goose StatementEnd
