#!/bin/sh
set -eu
umask 077

if [ "$#" -ne 1 ]; then
  echo "usage: $0 OUTPUT_ENV_FILE" >&2
  exit 64
fi

output=$1
if [ -e "$output" ]; then
  echo "refusing to overwrite existing output" >&2
  exit 65
fi
directory=$(dirname -- "$output")
if [ ! -d "$directory" ]; then
  echo "output directory does not exist" >&2
  exit 66
fi

temporary=$(mktemp "$directory/.wde-easypanel-secrets.XXXXXX")
cleanup() { rm -f -- "$temporary"; }
trap cleanup EXIT HUP INT TERM

random_base64url() {
  openssl rand "$1" | openssl base64 -A | tr '+/' '-_' | tr -d '='
}

postgres_password=$(random_base64url 36)
migrator_password=$(random_base64url 36)
api_password=$(random_base64url 36)
worker_password=$(random_base64url 36)
admin_password=$(random_base64url 36)
console_password=$(random_base64url 36)
grafana_admin_password=$(random_base64url 36)
auth_pepper=$(random_base64url 32)
idempotency_pepper=$(random_base64url 32)
fingerprint_pepper=$(random_base64url 32)
rate_limit_pepper=$(random_base64url 32)
cursor_pepper=$(random_base64url 32)
console_session_pepper=$(random_base64url 32)
console_csrf_pepper=$(random_base64url 32)
payload_key=$(random_base64url 32)
signing_key=$(random_base64url 32)

{
  printf 'WDE_POSTGRES_SUPERUSER_PASSWORD=%s\n' "$postgres_password"
  printf 'WDE_MIGRATOR_PGPASS=localhost:5432:wde:wde_migrator:%s\n' "$migrator_password"
  printf 'WDE_API_PGPASS=localhost:5432:wde:wde_api:%s\n' "$api_password"
  printf 'WDE_WORKER_PGPASS=localhost:5432:wde:wde_worker:%s\n' "$worker_password"
  printf 'WDE_ADMIN_PGPASS=localhost:5432:wde:wde_admin:%s\n' "$admin_password"
  printf 'WDE_CONSOLE_PGPASS=localhost:5432:wde:wde_console:%s\n' "$console_password"
  printf 'WDE_AUTH_PEPPER=v1:%s\n' "$auth_pepper"
  printf 'WDE_IDEMPOTENCY_PEPPER=v1:%s\n' "$idempotency_pepper"
  printf 'WDE_FINGERPRINT_PEPPER=v1:%s\n' "$fingerprint_pepper"
  printf 'WDE_RATE_LIMIT_PEPPER=v1:%s\n' "$rate_limit_pepper"
  printf 'WDE_CURSOR_PEPPER=v1:%s\n' "$cursor_pepper"
  printf 'WDE_CONSOLE_SESSION_PEPPER=v1:%s\n' "$console_session_pepper"
  printf 'WDE_CONSOLE_CSRF_PEPPER=v1:%s\n' "$console_csrf_pepper"
  printf 'WDE_PAYLOAD_KEYRING='\''{"format_version":1,"primary_key_version":1,"keys":[{"version":1,"material":"%s"}]}'\''\n' "$payload_key"
  printf 'WDE_SIGNING_KEYRING='\''{"format_version":1,"primary_key_version":1,"keys":[{"version":1,"material":"%s"}]}'\''\n' "$signing_key"
  printf 'WDE_GRAFANA_ADMIN_PASSWORD=%s\n' "$grafana_admin_password"
} > "$temporary"

chmod 0600 "$temporary"
mv -- "$temporary" "$output"
trap - EXIT HUP INT TERM
echo "arquivo de segredos criado com permissao 0600: $output" >&2
