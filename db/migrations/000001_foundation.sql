-- +goose Up
-- +goose StatementBegin
SET lock_timeout = '5s';
SET statement_timeout = '30s';
SET ROLE wde_owner;

CREATE SCHEMA wde AUTHORIZATION wde_owner;

REVOKE ALL ON SCHEMA public FROM PUBLIC;
REVOKE ALL ON SCHEMA wde FROM PUBLIC;
GRANT USAGE ON SCHEMA wde TO wde_api, wde_worker, wde_admin;

ALTER DEFAULT PRIVILEGES FOR ROLE wde_owner IN SCHEMA wde REVOKE ALL ON TABLES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE wde_owner IN SCHEMA wde REVOKE ALL ON SEQUENCES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE wde_owner IN SCHEMA wde REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC;

CREATE TABLE wde.schema_metadata (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    schema_version integer NOT NULL CHECK (schema_version > 0),
    installed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

REVOKE ALL ON TABLE wde.schema_metadata FROM PUBLIC, wde_api, wde_worker, wde_admin;
INSERT INTO wde.schema_metadata (schema_version) VALUES (1);

CREATE FUNCTION wde.schema_version()
RETURNS integer
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog
AS 'SELECT schema_version FROM wde.schema_metadata WHERE singleton';

REVOKE ALL ON FUNCTION wde.schema_version() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION wde.schema_version() TO wde_api, wde_worker;

RESET ROLE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET lock_timeout = '5s';
SET statement_timeout = '30s';
SET ROLE wde_owner;
DROP SCHEMA wde CASCADE;
RESET ROLE;
-- +goose StatementEnd
