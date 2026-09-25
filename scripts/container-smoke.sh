#!/bin/sh
set -eu
umask 077

for command in go; do
	command -v "$command" >/dev/null 2>&1 || { echo "container-smoke: missing $command" >&2; exit 1; }
done

project="wde-s6-smoke-$$"
postgres_port=${WDE_SMOKE_POSTGRES_PORT:-35432}
api_port=${WDE_SMOKE_API_PORT:-38080}
api_ops_port=${WDE_SMOKE_API_OPS_PORT:-39090}
worker_ops_port=${WDE_SMOKE_WORKER_OPS_PORT:-39091}
console_port=${WDE_SMOKE_CONSOLE_PORT:-38082}
console_ops_port=${WDE_SMOKE_CONSOLE_OPS_PORT:-39092}
chaos_port=${WDE_SMOKE_CHAOS_PORT:-38081}
revision=$(git rev-parse HEAD)

compose() {
	COMPOSE_PROJECT_NAME="$project" WDE_POSTGRES_PORT="$postgres_port" WDE_API_PORT="$api_port" \
		WDE_API_OPS_PORT="$api_ops_port" WDE_WORKER_OPS_PORT="$worker_ops_port" \
		WDE_CONSOLE_PORT="$console_port" WDE_CONSOLE_OPS_PORT="$console_ops_port" \
		WDE_CHAOS_PORT="$chaos_port" WDE_VERSION=s6-smoke WDE_REVISION="$revision" \
		docker compose --profile demo "$@"
}

cleanup() {
	compose down --volumes --remove-orphans >/dev/null 2>&1 || true
}

verify_contract() {
	go test ./test/imagelayout -count=1
}

verify_contract
if [ "$#" -eq 1 ] && [ "$1" = "--layout-contract" ]; then
	echo "Image layout contract probes complete."
	exit
fi
[ "$#" -eq 0 ] || { echo "usage: $0 [--layout-contract]" >&2; exit 64; }
for command in docker curl grep; do
	command -v "$command" >/dev/null 2>&1 || { echo "container-smoke: missing $command" >&2; exit 1; }
done
trap cleanup EXIT HUP INT TERM

echo "[1/4] build pinado e ambiente descartavel"
for service in migrate api worker console chaoslab; do
	compose build "$service"
done
compose up --detach --wait --no-build postgres api worker console chaoslab

echo "[2/4] healthchecks e separacao da superficie operacional"
curl --fail --silent "http://127.0.0.1:$api_ops_port/livez" >/dev/null
curl --fail --silent "http://127.0.0.1:$api_ops_port/readyz" >/dev/null
curl --fail --silent "http://127.0.0.1:$worker_ops_port/livez" >/dev/null
curl --fail --silent "http://127.0.0.1:$worker_ops_port/readyz" >/dev/null
curl --fail --silent "http://127.0.0.1:$console_ops_port/livez" >/dev/null
curl --fail --silent "http://127.0.0.1:$console_ops_port/readyz" >/dev/null
curl --fail --silent "http://127.0.0.1:$chaos_port/livez" >/dev/null
[ "$(curl --silent --output /dev/null --write-out '%{http_code}' "http://127.0.0.1:$api_port/metrics")" = 404 ]

echo "[3/4] usuario, filesystem e privilegios"
for service in api worker console chaoslab migrate; do
	container=$(compose ps --all -q "$service")
	[ -n "$container" ]
	[ "$(docker inspect --format '{{.Config.User}}' "$container")" = "nonroot:nonroot" ]
	[ "$(docker inspect --format '{{.HostConfig.ReadonlyRootfs}}' "$container")" = true ]
	docker inspect --format '{{json .HostConfig.CapDrop}}' "$container" | grep -q 'ALL'
	docker inspect --format '{{json .HostConfig.SecurityOpt}}' "$container" | grep -q 'no-new-privileges:true'
	docker inspect --format '{{json .HostConfig.Tmpfs}}' "$container" | grep -q '"/tmp"'
done

echo "[4/4] schema e conteudo minimo da imagem"
[ "$(compose exec -T postgres psql -U postgres -d wde -Atc 'SELECT wde.schema_version()')" = 6 ]
"$(dirname "$0")/verify-image-layout.sh" webhook-delivery-engine-api runtime
"$(dirname "$0")/verify-image-layout.sh" webhook-delivery-engine-worker runtime
"$(dirname "$0")/verify-image-layout.sh" webhook-delivery-engine-console runtime
"$(dirname "$0")/verify-image-layout.sh" webhook-delivery-engine-chaoslab runtime
"$(dirname "$0")/verify-image-layout.sh" webhook-delivery-engine-migrate migrator
echo "Container smoke complete."
