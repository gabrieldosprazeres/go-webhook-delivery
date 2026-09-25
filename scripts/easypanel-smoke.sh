#!/bin/sh
set -eu
umask 077

project=wde-easypanel-smoke
temporary=$(mktemp -d "${TMPDIR:-/tmp}/wde-easypanel-smoke.XXXXXX")
secrets="$temporary/easypanel.env"
compose_files="-f compose.easypanel.yaml -f deployments/easypanel/compose.smoke.yaml"

cleanup() {
  status=$?
  if [ "$status" -ne 0 ]; then
    docker compose -p "$project" $compose_files logs --no-color 2>/dev/null || true
  fi
  docker compose -p "$project" $compose_files down --volumes --remove-orphans >/dev/null 2>&1 || true
  find "$temporary" -depth -delete
  trap - EXIT
  exit "$status"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

./scripts/generate-easypanel-secrets.sh "$secrets"
set -a
. "$secrets"
set +a
export WDE_SHOWCASE_API_URL=https://api.example.test
export WDE_SHOWCASE_DOCS_URL=https://docs.example.test
export WDE_DOCS_API_URL=https://api.example.test

docker compose -p "$project" --env-file "$secrets" $compose_files config --quiet
docker compose -p "$project" --env-file "$secrets" $compose_files up --build --detach --wait --wait-timeout 180

curl --fail --silent --show-error --retry 12 --retry-delay 1 http://127.0.0.1:19090/readyz >/dev/null
curl --fail --silent --show-error --retry 12 --retry-delay 1 http://127.0.0.1:19091/readyz >/dev/null
curl --fail --silent --show-error http://127.0.0.1:18081/ | grep -q 'Webhooks que chegam'
curl --fail --silent --show-error http://127.0.0.1:18082/ | grep -q 'Swagger UI'
curl --fail --silent --show-error http://127.0.0.1:18080/ | grep -q '"authentication":"required"'
test "$(curl --silent --output /dev/null --write-out '%{http_code}' http://127.0.0.1:18080/livez)" = "404"
test -z "$(docker compose -p "$project" $compose_files port postgres 5432 2>/dev/null || true)"
docker compose -p "$project" $compose_files run --rm --no-deps --entrypoint bash secrets-init -euc '
  count=0
  for path in /materialized/*; do
    test -f "$path"
    test "$(stat -c "%a:%u:%g" "$path")" = "400:65532:65532"
    count=$((count + 1))
  done
  test "$count" = 10
'

api_id=$(docker compose -p "$project" $compose_files ps -q api)
worker_id=$(docker compose -p "$project" $compose_files ps -q worker)
showcase_id=$(docker compose -p "$project" $compose_files ps -q showcase)
swagger_id=$(docker compose -p "$project" $compose_files ps -q swagger)
for container_id in "$api_id" "$worker_id" "$showcase_id" "$swagger_id"; do
  test "$(docker inspect --format '{{.Config.User}}' "$container_id")" = "nonroot:nonroot"
  test "$(docker inspect --format '{{.HostConfig.ReadonlyRootfs}}' "$container_id")" = "true"
done

echo "EasyPanel production topology smoke test passed"
