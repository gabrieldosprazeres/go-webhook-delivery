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
    docker compose -p "$project" --env-file "$secrets" $compose_files logs --no-color 2>/dev/null || true
  fi
  docker compose -p "$project" --env-file "$secrets" $compose_files down --volumes --remove-orphans >/dev/null 2>&1 || true
  find "$temporary" -depth -delete
  trap - EXIT
  exit "$status"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

./scripts/generate-easypanel-secrets.sh "$secrets"
export WDE_SHOWCASE_API_URL=https://api.example.test
export WDE_SHOWCASE_DOCS_URL=https://docs.example.test
export WDE_SHOWCASE_CONSOLE_URL=https://console.example.test
export WDE_DOCS_API_URL=https://api.example.test
export WDE_CONSOLE_ORIGIN=https://console.example.test
export WDE_GRAFANA_LOCAL_PORT=13000
export WDE_GRAFANA_ROOT_URL=http://localhost:13000

base_config=$(docker compose -p "$project" --env-file "$secrets" -f compose.easypanel.yaml config --format json)
printf '%s' "$base_config" | jq -e '
  .services["otel-collector"].ports == null and
  .services.prometheus.ports == null and
  .services.tempo.ports == null and
  (.services.tempo.tmpfs | index("/var/tempo:size=256m,mode=0750,uid=10001,gid=10001")) != null and
  ([.services.tempo.volumes[]? | select(.target == "/var/tempo")] | length) == 0 and
  ([.services.prometheus.volumes[]? | select(.target == "/etc/prometheus/alerts.yaml" and .read_only == true)] | length) == 1 and
  (.services.grafana.ports | length) == 1 and
  .services.grafana.ports[0].target == 3000 and
  .services.grafana.ports[0].published == "13000" and
  .services.grafana.ports[0].host_ip == "127.0.0.1"
' >/dev/null
docker compose -p "$project" --env-file "$secrets" $compose_files config --quiet
docker compose -p "$project" --env-file "$secrets" $compose_files up --build --detach --wait --wait-timeout 180

echo "[smoke] API readiness"
curl --fail --silent --show-error --retry 12 --retry-delay 1 http://127.0.0.1:19090/readyz >/dev/null
echo "[smoke] worker readiness"
curl --fail --silent --show-error --retry 12 --retry-delay 1 http://127.0.0.1:19091/readyz >/dev/null
echo "[smoke] console readiness"
curl --fail --silent --show-error --retry 12 --retry-delay 1 http://127.0.0.1:19092/readyz >/dev/null
echo "[smoke] showcase surface"
curl --fail --silent --show-error http://127.0.0.1:18081/ | grep -q 'Webhooks que chegam'
echo "[smoke] Swagger surface"
curl --fail --silent --show-error http://127.0.0.1:18082/ | grep -q 'Swagger UI'
echo "[smoke] console surface"
curl --fail --silent --show-error http://127.0.0.1:18083/login | grep -q 'Conectar workspace'
echo "[smoke] Grafana login"
grafana_login_ready=false
attempt=0
while [ "$attempt" -lt 30 ]; do
  if [ "$(curl --silent --output /dev/null --write-out '%{http_code}' http://localhost:13000/login)" = "200" ]; then
    grafana_login_ready=true
    break
  fi
  attempt=$((attempt + 1))
  sleep 1
done
test "$grafana_login_ready" = true
echo "[smoke] collector readiness"
curl --fail --silent --show-error --retry 12 --retry-delay 1 http://127.0.0.1:13133/ >/dev/null
echo "[smoke] Prometheus readiness"
curl --fail --silent --show-error --retry 12 --retry-delay 1 http://127.0.0.1:19093/-/ready >/dev/null
echo "[smoke] Tempo readiness"
curl --fail --silent --show-error --retry 12 --retry-delay 1 http://127.0.0.1:13200/ready >/dev/null
echo "[smoke] API public boundary"
curl --fail --silent --show-error http://127.0.0.1:18080/ | grep -q '"authentication":"required"'
test "$(curl --silent --output /dev/null --write-out '%{http_code}' http://127.0.0.1:18080/livez)" = "404"
test -z "$(docker compose -p "$project" --env-file "$secrets" $compose_files port postgres 5432 2>/dev/null || true)"
echo "[smoke] materialized secret permissions"
docker compose -p "$project" --env-file "$secrets" $compose_files run --rm --no-deps --entrypoint bash secrets-init -euc '
  count=0
  for path in /materialized/*; do
    test -f "$path"
    expected="400:65532:65532"
    if [ "$(basename "$path")" = "grafana_admin_password" ]; then
      expected="400:472:0"
    fi
    test "$(stat -c "%a:%u:%g" "$path")" = "$expected"
    count=$((count + 1))
  done
  test "$count" = 14
'

api_id=$(docker compose -p "$project" --env-file "$secrets" $compose_files ps -q api)
worker_id=$(docker compose -p "$project" --env-file "$secrets" $compose_files ps -q worker)
console_id=$(docker compose -p "$project" --env-file "$secrets" $compose_files ps -q console)
showcase_id=$(docker compose -p "$project" --env-file "$secrets" $compose_files ps -q showcase)
swagger_id=$(docker compose -p "$project" --env-file "$secrets" $compose_files ps -q swagger)
for container_id in "$api_id" "$worker_id" "$console_id" "$showcase_id" "$swagger_id"; do
  test "$(docker inspect --format '{{.Config.User}}' "$container_id")" = "nonroot:nonroot"
  test "$(docker inspect --format '{{.HostConfig.ReadonlyRootfs}}' "$container_id")" = "true"
done
echo "[smoke] container hardening"
for service in otel-collector prometheus tempo grafana; do
  container_id=$(docker compose -p "$project" --env-file "$secrets" $compose_files ps -q "$service")
  user=$(docker inspect --format '{{.Config.User}}' "$container_id")
  test -n "$user" && test "$user" != "0" && test "$user" != "root"
  test "$(docker inspect --format '{{.HostConfig.ReadonlyRootfs}}' "$container_id")" = "true"
  docker inspect --format '{{json .HostConfig.CapDrop}}' "$container_id" | grep -q 'ALL'
  docker inspect --format '{{json .HostConfig.SecurityOpt}}' "$container_id" | grep -q 'no-new-privileges'
done
tempo_id=$(docker compose -p "$project" --env-file "$secrets" $compose_files ps -q tempo)
docker inspect --format '{{json .HostConfig.Tmpfs}}' "$tempo_id" |
  jq -e '.["/var/tempo"] | contains("size=268435456")' >/dev/null

targets_ready=false
echo "[smoke] Prometheus targets and rules"
attempt=0
while [ "$attempt" -lt 30 ]; do
  targets=$(curl --fail --silent --show-error --get --data-urlencode 'query=up' \
    http://127.0.0.1:19093/api/v1/query)
  if [ "$(printf '%s' "$targets" | jq '[.data.result[] | select(.value[1] == "1")] | length')" -ge 5 ]; then
    targets_ready=true
    break
  fi
  attempt=$((attempt + 1))
  sleep 1
done
test "$targets_ready" = true
curl --fail --silent --show-error http://127.0.0.1:19093/api/v1/rules |
  jq -e '.data.groups[].rules[] | select(.name == "WDETempoDiscardingSpans" and .type == "alerting")' >/dev/null

trace_id=0123456789abcdef0123456789abcdef
echo "[smoke] OTLP to Tempo canary"
start_ns=$(($(date +%s) * 1000000000))
end_ns=$((start_ns + 1000000))
curl --fail --silent --show-error -H 'Content-Type: application/json' \
  --data "{\"resourceSpans\":[{\"resource\":{\"attributes\":[{\"key\":\"service.name\",\"value\":{\"stringValue\":\"wde-smoke\"}}]},\"scopeSpans\":[{\"scope\":{\"name\":\"wde-smoke\"},\"spans\":[{\"traceId\":\"$trace_id\",\"spanId\":\"0123456789abcdef\",\"name\":\"smoke.trace\",\"kind\":1,\"startTimeUnixNano\":\"$start_ns\",\"endTimeUnixNano\":\"$end_ns\"}]}]}]}" \
  http://127.0.0.1:14318/v1/traces >/dev/null
trace_found=false
attempt=0
while [ "$attempt" -lt 20 ]; do
  if curl --fail --silent --show-error "http://127.0.0.1:13200/api/traces/$trace_id" | jq -e '.batches | length > 0' >/dev/null 2>&1; then
    trace_found=true
    break
  fi
  attempt=$((attempt + 1))
  sleep 1
done
test "$trace_found" = true

set -a
. "$secrets"
set +a
echo "[smoke] Grafana provisioning"
grafana_auth="${WDE_GRAFANA_ADMIN_USER:-operator}:$WDE_GRAFANA_ADMIN_PASSWORD"
curl --fail --silent --show-error --user "$grafana_auth" \
  http://localhost:13000/api/datasources/uid/prometheus | jq -e '.name == "Prometheus"' >/dev/null
curl --fail --silent --show-error --user "$grafana_auth" \
  http://localhost:13000/api/datasources/uid/tempo | jq -e '.name == "Tempo"' >/dev/null
test "$(curl --fail --silent --show-error --user "$grafana_auth" \
  'http://localhost:13000/api/search?query=WDE' | jq 'length')" -ge 2
unset grafana_auth WDE_GRAFANA_ADMIN_PASSWORD

docker compose -p "$project" --env-file "$secrets" $compose_files stop grafana prometheus tempo otel-collector >/dev/null
curl --fail --silent --show-error http://127.0.0.1:19090/readyz >/dev/null
curl --fail --silent --show-error http://127.0.0.1:19091/readyz >/dev/null
curl --fail --silent --show-error http://127.0.0.1:19092/readyz >/dev/null

echo "EasyPanel production topology smoke test passed"
