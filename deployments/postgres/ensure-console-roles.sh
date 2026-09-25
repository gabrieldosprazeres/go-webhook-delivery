#!/usr/bin/env bash
set -euo pipefail

read_pgpass() {
  local path=$1 expected_user=$2 output_name=$3
  local line host port database username password extra
  IFS=: read -r host port database username password extra < "${path}"
  if [[ "${host}" != "localhost" || "${port}" != "5432" || "${database}" != "wde" ||
        "${username}" != "${expected_user}" || -z "${password}" || -n "${extra:-}" ||
        "${password}" == *[!A-Za-z0-9_-]* ]]; then
    echo "invalid pgpass for ${expected_user}" >&2
    exit 1
  fi
  printf -v "${output_name}" '%s' "${password}"
  export "${output_name}"
}

read_pgpass /run/secrets/console_pgpass wde_console WDE_CONSOLE_DATABASE_PASSWORD
export PGPASSWORD
PGPASSWORD="$(tr -d '\r\n' < /run/secrets/postgres_password)"

psql --set ON_ERROR_STOP=1 --host /var/run/postgresql --username postgres --dbname wde <<'SQL'
\getenv console_password WDE_CONSOLE_DATABASE_PASSWORD
DO $block$
BEGIN
  IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='wde_console') THEN
    CREATE ROLE wde_console LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
  END IF;
  IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='wde_console_session_executor') THEN
    CREATE ROLE wde_console_session_executor NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
  END IF;
END
$block$;
ALTER ROLE wde_console PASSWORD :'console_password';
GRANT wde_console_session_executor TO wde_owner;
GRANT CONNECT ON DATABASE wde TO wde_console;
SQL

unset WDE_CONSOLE_DATABASE_PASSWORD PGPASSWORD
