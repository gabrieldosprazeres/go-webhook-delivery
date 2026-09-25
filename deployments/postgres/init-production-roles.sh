#!/usr/bin/env bash
set -euo pipefail

read_pgpass() {
  local path=$1 expected_user=$2 output_name=$3
  local line host port database username password extra
  IFS= read -r line < "${path}"
  IFS=: read -r host port database username password extra <<< "${line}"
  if [[ "${host}" != "localhost" || "${port}" != "5432" || "${database}" != "wde" ||
        "${username}" != "${expected_user}" || -z "${password}" || -n "${extra:-}" ||
        "${password}" == *[!A-Za-z0-9_-]* ]]; then
    echo "invalid production pgpass for ${expected_user}" >&2
    exit 1
  fi
  printf -v "${output_name}" '%s' "${password}"
  export "${output_name}"
}

read_pgpass /run/secrets/migrator_pgpass wde_migrator WDE_MIGRATOR_PASSWORD
read_pgpass /run/secrets/api_pgpass wde_api WDE_API_DATABASE_PASSWORD
read_pgpass /run/secrets/worker_pgpass wde_worker WDE_WORKER_DATABASE_PASSWORD
read_pgpass /run/secrets/admin_pgpass wde_admin WDE_ADMIN_DATABASE_PASSWORD

PGPASSWORD="${POSTGRES_PASSWORD}" psql --set ON_ERROR_STOP=1 --username "${POSTGRES_USER}" --dbname "${POSTGRES_DB}" <<'SQL'
\getenv migrator_password WDE_MIGRATOR_PASSWORD
\getenv api_password WDE_API_DATABASE_PASSWORD
\getenv worker_password WDE_WORKER_DATABASE_PASSWORD
\getenv admin_password WDE_ADMIN_DATABASE_PASSWORD

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
CREATE ROLE wde_maintenance_executor NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
CREATE ROLE wde_rotation_executor NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;

GRANT wde_owner TO wde_migrator;
GRANT wde_auth_executor, wde_worker_executor, wde_audit_executor,
  wde_quota_executor, wde_replay_executor, wde_maintenance_executor, wde_rotation_executor TO wde_owner;
ALTER DATABASE wde OWNER TO wde_owner;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
REVOKE ALL ON DATABASE wde FROM PUBLIC;
GRANT CONNECT ON DATABASE wde TO wde_migrator, wde_api, wde_worker, wde_admin;
SQL

unset WDE_MIGRATOR_PASSWORD WDE_API_DATABASE_PASSWORD WDE_WORKER_DATABASE_PASSWORD WDE_ADMIN_DATABASE_PASSWORD
