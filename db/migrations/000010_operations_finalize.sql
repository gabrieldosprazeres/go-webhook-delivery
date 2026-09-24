-- +goose Up
-- +goose StatementBegin
SET lock_timeout = '5s';
SET statement_timeout = '30s';
SET ROLE wde_owner;

ALTER FUNCTION wde.append_audit_event_v4_stage(uuid, uuid, text, text, text, text, text, text, text, text)
    RENAME TO append_audit_event;
GRANT EXECUTE ON FUNCTION wde.append_audit_event(uuid, uuid, text, text, text, text, text, text, text, text)
    TO wde_api, wde_admin, wde_replay_executor;

ALTER FUNCTION wde.consume_quota_v4_stage(bytea, text, text, uuid, uuid, uuid, integer, integer)
    RENAME TO consume_quota;
GRANT EXECUTE ON FUNCTION wde.consume_quota(bytea, text, text, uuid, uuid, uuid, integer, integer)
    TO wde_api;

ALTER FUNCTION wde.request_replay_v4_stage(uuid, uuid, uuid, uuid, text, bytea, bytea, smallint, text, text)
    RENAME TO request_replay;
GRANT EXECUTE ON FUNCTION wde.request_replay(uuid, uuid, uuid, uuid, text, bytea, bytea, smallint, text, text)
    TO wde_api;

REVOKE CREATE ON SCHEMA wde FROM wde_auth_executor, wde_audit_executor, wde_worker_executor,
    wde_quota_executor, wde_replay_executor;
UPDATE wde.schema_metadata SET schema_version = 4 WHERE singleton;
RESET ROLE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET lock_timeout = '5s';
SET statement_timeout = '30s';
SET ROLE wde_owner;

REVOKE EXECUTE ON FUNCTION wde.request_replay(uuid, uuid, uuid, uuid, text, bytea, bytea, smallint, text, text)
    FROM wde_api;
ALTER FUNCTION wde.request_replay(uuid, uuid, uuid, uuid, text, bytea, bytea, smallint, text, text)
    RENAME TO request_replay_v4_stage;

REVOKE EXECUTE ON FUNCTION wde.consume_quota(bytea, text, text, uuid, uuid, uuid, integer, integer)
    FROM wde_api;
ALTER FUNCTION wde.consume_quota(bytea, text, text, uuid, uuid, uuid, integer, integer)
    RENAME TO consume_quota_v4_stage;

REVOKE EXECUTE ON FUNCTION wde.append_audit_event(uuid, uuid, text, text, text, text, text, text, text, text)
    FROM wde_api, wde_admin, wde_replay_executor;
ALTER FUNCTION wde.append_audit_event(uuid, uuid, text, text, text, text, text, text, text, text)
    RENAME TO append_audit_event_v4_stage;

UPDATE wde.schema_metadata SET schema_version = 3 WHERE singleton;
RESET ROLE;
-- +goose StatementEnd
