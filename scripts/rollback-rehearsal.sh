#!/bin/sh
set -eu
umask 077

for command in docker go; do
	command -v "$command" >/dev/null 2>&1 || { echo "rollback: missing $command" >&2; exit 1; }
done
work=$(mktemp -d "${TMPDIR:-/tmp}/wde-rollback.XXXXXX")
project="wde-rollback-$$"
port=${WDE_ROLLBACK_POSTGRES_PORT:-49432}
compose() {
	COMPOSE_PROJECT_NAME="$project" WDE_POSTGRES_PORT="$port" docker compose \
		-f compose.yaml -f deployments/docker/compose.benchmark.yaml --profile test "$@"
}
cleanup() {
	compose down --volumes --remove-orphans >/dev/null 2>&1 || true
	find "$work" -depth -delete
}
trap cleanup EXIT HUP INT TERM

migrator="postgres://wde_migrator:migrator-local-only@127.0.0.1:$port/wde?sslmode=disable"
api="postgres://wde_api:api-local-only@127.0.0.1:$port/wde?sslmode=disable"
admin="postgres://wde_admin:admin-local-only@127.0.0.1:$port/wde?sslmode=disable"
goose() { GOOSE_DRIVER=postgres GOOSE_DBSTRING="$migrator" go tool goose -dir db/migrations "$@"; }

echo "[1/7] fresh PG17 tmpfs, migration status and up"
compose up --detach --wait postgres >/dev/null
goose status >/dev/null
goose up >/dev/null

echo "[2/7] empty console boundary supports reviewed down/up"
goose down-to 20 >/dev/null
[ "$(compose exec -T postgres psql -U postgres -d wde -Atc \
	'SELECT max(version_id) FROM goose_db_version WHERE is_applied')" = 20 ]
goose up >/dev/null

echo "[3/7] create representative v6 data and browser session"
WDE_PROFILE=local WDE_ADMIN_DATABASE_URL="$admin" WDE_DATABASE_URL="$api" \
	go run ./cmd/api credentials bootstrap --name "Rollback rehearsal" \
	--output-file="$work/credential.json"
workspace_id=$(sed -n 's/.*"workspace_id": "\([^"]*\)".*/\1/p' "$work/credential.json")
api_key_id=$(sed -n 's/.*"api_key_id": "\([^"]*\)".*/\1/p' "$work/credential.json")
compose exec -T -e PGPASSWORD=console-local-only postgres psql -h /var/run/postgresql \
	-U wde_console -d wde -v ON_ERROR_STOP=1 -v workspace_id="$workspace_id" -v api_key_id="$api_key_id" \
	>/dev/null <<'SQL'
SELECT wde.create_console_session(
  gen_random_uuid(),
  :'workspace_id',
  :'api_key_id',
  '0123456789abcdef',
  decode(repeat('01', 32), 'hex'),
  gen_random_uuid()
);
SQL
before=$(compose exec -T postgres psql -U postgres -d wde -Atc \
	"SELECT wde.schema_version() || '|' || count(*) FROM wde.workspaces GROUP BY wde.schema_version()")
[ "$before" = "6|1" ] || { echo "rollback: fixture was not created" >&2; exit 1; }

echo "[4/7] migration guard must fail atomically on live v6 session data"
if goose down-to 20 >"$work/down.log" 2>&1; then
	echo "rollback: unsafe downgrade unexpectedly succeeded" >&2
	exit 1
fi
physical=$(compose exec -T postgres psql -U postgres -d wde -Atc \
	"SELECT max(version_id) FROM goose_db_version WHERE is_applied")
after=$(compose exec -T postgres psql -U postgres -d wde -Atc \
	"SELECT wde.schema_version() || '|' || count(*) FROM wde.workspaces GROUP BY wde.schema_version()")
[ "$physical" = 21 ] && [ "$after" = "$before" ] || {
	echo "rollback: failed downgrade did not preserve the guarded boundary" >&2
	exit 1
}

echo "[5/7] external target-aware preflight remains useful but non-authoritative"
if WDE_MIGRATOR_DATABASE_URL="$migrator" WDE_ROLLBACK_TARGET=0 \
	./scripts/guarded-migrate-down.sh >"$work/preflight.log" 2>&1; then
	echo "rollback: unsafe wrapper downgrade unexpectedly succeeded" >&2
	exit 1
fi

echo "[6/7] queue-metrics guard still preserves its independent boundary 20"
WDE_TEST_SUPERUSER_DATABASE_URL="postgres://postgres:postgres-local-only@127.0.0.1:$port/wde?sslmode=disable" \
	go test ./test/integration -run '^TestQueueMetricsDowngradeGuardIsAtomicAndSerialized$' -count=1

echo "[7/7] status and no-op forward recovery remain healthy"
goose status >/dev/null
goose up >/dev/null
[ "$(compose exec -T postgres psql -U postgres -d wde -Atc 'SELECT wde.schema_version()')" = 6 ]
[ "$(compose exec -T postgres psql -U postgres -d wde -Atc \
	'SELECT count(*) FROM wde.workspaces')" = 1 ]
echo "Rollback rehearsal complete: empty down/up passed; live console state stayed at 21 and queue guards stayed intact."
