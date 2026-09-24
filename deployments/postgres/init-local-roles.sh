#!/usr/bin/env bash
set -euo pipefail

required_variables=(
  WDE_MIGRATOR_PASSWORD
  WDE_API_DATABASE_PASSWORD
  WDE_WORKER_DATABASE_PASSWORD
  WDE_ADMIN_DATABASE_PASSWORD
)

for variable_name in "${required_variables[@]}"; do
  if [[ -z "${!variable_name:-}" ]]; then
    echo "missing required local database variable: ${variable_name}" >&2
    exit 1
  fi
done

psql --set ON_ERROR_STOP=1 \
  --username "${POSTGRES_USER}" \
  --dbname "${POSTGRES_DB}" \
  --set migrator_password="${WDE_MIGRATOR_PASSWORD}" \
  --set api_password="${WDE_API_DATABASE_PASSWORD}" \
  --set worker_password="${WDE_WORKER_DATABASE_PASSWORD}" \
  --set admin_password="${WDE_ADMIN_DATABASE_PASSWORD}" <<'SQL'
CREATE ROLE wde_owner NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
CREATE ROLE wde_migrator LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD :'migrator_password';
CREATE ROLE wde_api LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD :'api_password';
CREATE ROLE wde_worker LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD :'worker_password';
CREATE ROLE wde_admin LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD :'admin_password';
CREATE ROLE wde_auth_executor NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
CREATE ROLE wde_worker_executor NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
CREATE ROLE wde_audit_executor NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
CREATE ROLE wde_quota_executor NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
CREATE ROLE wde_replay_executor NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;

GRANT wde_owner TO wde_migrator;
GRANT wde_auth_executor, wde_worker_executor, wde_audit_executor,
  wde_quota_executor, wde_replay_executor TO wde_owner;
ALTER DATABASE wde OWNER TO wde_owner;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
REVOKE ALL ON DATABASE wde FROM PUBLIC;
GRANT CONNECT ON DATABASE wde TO wde_migrator, wde_api, wde_worker, wde_admin;
SQL
