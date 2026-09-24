#!/bin/sh
set -eu

for command in docker go curl sed grep; do
	command -v "$command" >/dev/null 2>&1 || { echo "quickstart: missing $command" >&2; exit 1; }
done

demo_dir=$(mktemp -d "${TMPDIR:-/tmp}/wde-demo.XXXXXX")
chmod 700 "$demo_dir"
project="wde-demo-$$"
postgres_port=${WDE_DEMO_POSTGRES_PORT:-15432}
api_port=${WDE_DEMO_API_PORT:-18080}
api_ops_port=${WDE_DEMO_API_OPS_PORT:-19090}
worker_ops_port=${WDE_DEMO_WORKER_OPS_PORT:-19091}
chaos_port=${WDE_DEMO_CHAOS_PORT:-18081}
pids=""

cleanup() {
	for pid in $pids; do kill "$pid" 2>/dev/null || true; done
	for pid in $pids; do wait "$pid" 2>/dev/null || true; done
	COMPOSE_PROJECT_NAME="$project" WDE_POSTGRES_PORT="$postgres_port" \
		docker compose --profile test down -v --remove-orphans >/dev/null 2>&1 || true
	rm -rf "$demo_dir"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

fail() {
	echo "quickstart: $1" >&2
	for log in "$demo_dir"/*.log; do
		[ -f "$log" ] && { echo "--- $(basename "$log")" >&2; tail -20 "$log" >&2; }
	done
	exit 1
}

wait_url() {
	url=$1
	count=0
	until curl --silent --fail --max-time 1 "$url" >/dev/null 2>&1; do
		count=$((count + 1))
		[ "$count" -lt 120 ] || fail "timeout waiting for $url"
		sleep 0.25
	done
}

json_value() {
	pattern=$1
	file=$2
	sed -n "s/.*$pattern.*/\\1/p" "$file" | head -1
}

wait_delivery() {
	delivery_id=$1
	want=$2
	count=0
	while [ "$count" -lt 200 ]; do
		curl --config "$demo_dir/curl.conf" --silent --fail --max-time 2 \
			"http://127.0.0.1:$api_port/v1/deliveries/$delivery_id" >"$demo_dir/delivery.json"
		status=$(json_value '"status":"\([^"]*\)"' "$demo_dir/delivery.json")
		[ "$status" = "$want" ] && return 0
		case "$status" in failed_permanent|dead_letter)
			[ "$status" = "$want" ] || fail "delivery $delivery_id ended as $status" ;;
		esac
		count=$((count + 1))
		sleep 0.1
	done
	fail "delivery $delivery_id did not reach $want"
}

create_endpoint() {
	path=$1
	event_type=$2
	response=$3
	printf '{"url":"http://127.0.0.1:%s/%s","event_types":["%s"]}\n' \
		"$chaos_port" "$path" "$event_type" >"$demo_dir/request.json"
	status=$(curl --config "$demo_dir/curl.conf" --silent --show-error --max-time 5 \
		--output "$response" --write-out '%{http_code}' --data-binary "@$demo_dir/request.json" \
		"http://127.0.0.1:$api_port/v1/endpoints")
	[ "$status" = "201" ] || fail "create endpoint returned HTTP $status"
}

publish_event() {
	event_type=$1
	key=$2
	response=$3
	printf '{"type":"%s","data":{"demo":true}}\n' "$event_type" >"$demo_dir/request.json"
	status=$(curl --config "$demo_dir/curl.conf" --silent --show-error --max-time 5 \
		--output "$response" --write-out '%{http_code}' -H "Idempotency-Key: $key" \
		--data-binary "@$demo_dir/request.json" "http://127.0.0.1:$api_port/v1/events")
	[ "$status" = "202" ] || fail "publish event returned HTTP $status"
}

echo "[1/7] starting isolated PostgreSQL 17 and migrations"
COMPOSE_PROJECT_NAME="$project" WDE_POSTGRES_PORT="$postgres_port" \
	docker compose --profile test up -d --wait postgres >/dev/null
COMPOSE_PROJECT_NAME="$project" WDE_POSTGRES_PORT="$postgres_port" \
	docker compose --profile test run --rm migrate >/dev/null

echo "[2/7] building and starting API, worker and local-only Chaos Lab"
go build -trimpath -o "$demo_dir/api" ./cmd/api
go build -trimpath -o "$demo_dir/worker" ./cmd/worker
go build -trimpath -o "$demo_dir/chaoslab" ./cmd/chaoslab
WDE_PROFILE=local WDE_CHAOSLAB_HTTP_ADDR="127.0.0.1:$chaos_port" \
	"$demo_dir/chaoslab" >"$demo_dir/chaoslab.log" 2>&1 & pids="$pids $!"
WDE_PROFILE=local WDE_DATABASE_URL="postgres://wde_api:api-local-only@127.0.0.1:$postgres_port/wde?sslmode=disable" \
	WDE_API_HTTP_ADDR="127.0.0.1:$api_port" WDE_API_OPERATIONAL_ADDR="127.0.0.1:$api_ops_port" \
	WDE_ALLOW_HTTP_DESTINATIONS=true "$demo_dir/api" >"$demo_dir/api.log" 2>&1 & pids="$pids $!"
WDE_PROFILE=local WDE_DATABASE_URL="postgres://wde_worker:worker-local-only@127.0.0.1:$postgres_port/wde?sslmode=disable" \
	WDE_WORKER_OPERATIONAL_ADDR="127.0.0.1:$worker_ops_port" WDE_ALLOW_HTTP_DESTINATIONS=true \
	WDE_WORKER_POLL_INTERVAL=250ms WDE_WORKER_CLAIM_TIMEOUT=200ms WDE_WORKER_REQUEST_TIMEOUT=250ms \
	WDE_WORKER_LEASE_TTL=20s WDE_WORKER_RETRY_BASE=25ms WDE_WORKER_RETRY_CAP=25ms \
	"$demo_dir/worker" >"$demo_dir/worker.log" 2>&1 & pids="$pids $!"
wait_url "http://127.0.0.1:$chaos_port/livez"
wait_url "http://127.0.0.1:$api_ops_port/readyz"
wait_url "http://127.0.0.1:$worker_ops_port/readyz"

echo "[3/7] bootstrapping a synthetic credential in a 0600 temporary file"
WDE_PROFILE=local WDE_ADMIN_DATABASE_URL="postgres://wde_admin:admin-local-only@127.0.0.1:$postgres_port/wde?sslmode=disable" \
	WDE_DATABASE_URL="postgres://wde_api:api-local-only@127.0.0.1:$postgres_port/wde?sslmode=disable" \
	"$demo_dir/api" credentials bootstrap --name "Quickstart synthetic" --output-file="$demo_dir/credential.json"
[ "$(stat -f '%Lp' "$demo_dir/credential.json" 2>/dev/null || stat -c '%a' "$demo_dir/credential.json")" = "600" ] || fail "credential file is not 0600"
api_key=$(json_value '"api_key": "\([^"]*\)"' "$demo_dir/credential.json")
[ -n "$api_key" ] || fail "could not read generated API key"
printf 'silent\nshow-error\nheader = "Authorization: Bearer %s"\nheader = "Content-Type: application/json"\n' \
	"$api_key" >"$demo_dir/curl.conf"
chmod 600 "$demo_dir/curl.conf"

echo "[4/7] verifying signed delivery and dual-key rotation"
create_endpoint verify-signature demo.signed "$demo_dir/endpoint.json"
endpoint_id=$(json_value '"endpoint":{"id":"\([^"]*\)"' "$demo_dir/endpoint.json")
key_id=$(json_value '"key_id":"\([^"]*\)"' "$demo_dir/endpoint.json")
secret=$(json_value '"secret":"\([^"]*\)"' "$demo_dir/endpoint.json")
printf '{"key_id":"%s","secret":"%s"}\n' "$key_id" "$secret" >"$demo_dir/request.json"
status=$(curl --silent --show-error --max-time 2 --output /dev/null --write-out '%{http_code}' \
	--data-binary "@$demo_dir/request.json" "http://127.0.0.1:$chaos_port/configure")
[ "$status" = "204" ] || fail "configure Chaos Lab returned HTTP $status"
publish_event demo.signed signed-001 "$demo_dir/event.json"
delivery_id=$(json_value '"delivery_ids":\["\([^"]*\)"' "$demo_dir/event.json")
wait_delivery "$delivery_id" succeeded
printf '{"overlap_seconds":3600}\n' >"$demo_dir/request.json"
status=$(curl --config "$demo_dir/curl.conf" --silent --show-error --max-time 5 \
	--output "$demo_dir/rotation.json" --write-out '%{http_code}' -H 'Idempotency-Key: rotation-001' \
	--data-binary "@$demo_dir/request.json" "http://127.0.0.1:$api_port/v1/endpoints/$endpoint_id/secret-rotations")
[ "$status" = "202" ] || fail "rotate secret returned HTTP $status"
key_id=$(json_value '"key_id":"\([^"]*\)"' "$demo_dir/rotation.json")
secret=$(json_value '"secret":"\([^"]*\)"' "$demo_dir/rotation.json")
printf '{"key_id":"%s","secret":"%s"}\n' "$key_id" "$secret" >"$demo_dir/request.json"
status=$(curl --silent --show-error --max-time 2 --output /dev/null --write-out '%{http_code}' \
	--data-binary "@$demo_dir/request.json" "http://127.0.0.1:$chaos_port/configure")
[ "$status" = "204" ] || fail "configure rotated key returned HTTP $status"
publish_event demo.signed signed-rotated-001 "$demo_dir/event.json"
rotated_delivery=$(json_value '"delivery_ids":\["\([^"]*\)"' "$demo_dir/event.json")
wait_delivery "$rotated_delivery" succeeded
curl --silent --show-error --fail "http://127.0.0.1:$chaos_port/state" >"$demo_dir/state.json"
grep -q '"valid_signatures":2' "$demo_dir/state.json" || fail "dual signature was not verified"

echo "[5/7] observing deterministic retry then success"
create_endpoint fail-n demo.retry "$demo_dir/endpoint.json"
publish_event demo.retry retry-001 "$demo_dir/event.json"
retry_delivery=$(json_value '"delivery_ids":\["\([^"]*\)"' "$demo_dir/event.json")
wait_delivery "$retry_delivery" succeeded
code=$(curl --silent --output /dev/null --write-out '%{http_code}' --data '' "http://127.0.0.1:$chaos_port/rate-limit")
[ "$code" = "429" ] || fail "rate-limit scenario returned $code"
code=$(curl --silent --output /dev/null --write-out '%{http_code}' --data '' "http://127.0.0.1:$chaos_port/permanent-failure")
[ "$code" = "400" ] || fail "permanent-failure scenario returned $code"

echo "[6/7] exhausting timeout retries into the DLQ and replaying it"
create_endpoint timeout demo.dlq "$demo_dir/endpoint.json"
publish_event demo.dlq dlq-001 "$demo_dir/event.json"
dlq_delivery=$(json_value '"delivery_ids":\["\([^"]*\)"' "$demo_dir/event.json")
wait_delivery "$dlq_delivery" dead_letter
printf '{"reason":"quickstart retry after receiver recovery window"}\n' >"$demo_dir/request.json"
status=$(curl --config "$demo_dir/curl.conf" --silent --show-error --max-time 5 \
	--output "$demo_dir/replay.json" --write-out '%{http_code}' -H 'Idempotency-Key: replay-001' \
	--data-binary "@$demo_dir/request.json" "http://127.0.0.1:$api_port/v1/deliveries/$dlq_delivery/replays")
[ "$status" = "202" ] || fail "replay returned HTTP $status"
replay_command=$(json_value '"command_id":"\([^"]*\)"' "$demo_dir/replay.json")
replay_run=$(json_value '"run_number":\([0-9]*\)' "$demo_dir/replay.json")
[ -n "$replay_command" ] && [ -n "$replay_run" ] || fail "replay identity missing"
status=$(curl --config "$demo_dir/curl.conf" --silent --show-error --max-time 5 \
	--output "$demo_dir/replay-duplicate.json" --write-out '%{http_code}' -H 'Idempotency-Key: replay-001' \
	--data-binary "@$demo_dir/request.json" "http://127.0.0.1:$api_port/v1/deliveries/$dlq_delivery/replays")
[ "$status" = "202" ] || fail "duplicate replay returned HTTP $status"
[ "$(json_value '"command_id":"\([^"]*\)"' "$demo_dir/replay-duplicate.json")" = "$replay_command" ] || fail "duplicate replay changed command"
[ "$(json_value '"delivery_id":"\([^"]*\)"' "$demo_dir/replay-duplicate.json")" = "$dlq_delivery" ] || fail "duplicate replay changed delivery"
[ "$(json_value '"run_number":\([0-9]*\)' "$demo_dir/replay-duplicate.json")" = "$replay_run" ] || fail "duplicate replay changed run"
grep -q '"duplicate":true' "$demo_dir/replay-duplicate.json" || fail "duplicate replay was not identified"
wait_delivery "$dlq_delivery" dead_letter

echo "[7/7] checking internal-only operational telemetry"
curl --silent --show-error --fail "http://127.0.0.1:$api_ops_port/metrics" >"$demo_dir/api.metrics"
curl --silent --show-error --fail "http://127.0.0.1:$worker_ops_port/metrics" >"$demo_dir/worker.metrics"
grep -q '^wde_http_requests_total' "$demo_dir/api.metrics" || fail "API metrics missing"
grep -q '^wde_delivery_attempts_total' "$demo_dir/worker.metrics" || fail "worker metrics missing"
echo "Quickstart complete: signed delivery, rotation, replay, retry, DLQ and telemetry verified."
