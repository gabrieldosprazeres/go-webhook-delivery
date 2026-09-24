#!/bin/sh
set -eu
umask 077

if [ "${WDE_BENCH_SINGLE:-0}" != 1 ]; then
	suite=$(mktemp -d "${TMPDIR:-/tmp}/wde-benchmark-suite.XXXXXX")
	mkdir "$suite/rounds"
	mode=${WDE_BENCH_MODE:-benchmark}
	events=${WDE_BENCH_EVENTS:-1000}
	warmup=${WDE_BENCH_WARMUP_EVENTS:-50}
	rounds=${WDE_BENCH_ROUNDS:-3}
	output=${WDE_BENCH_OUTPUT:-$suite/summary.json}
	cleanup_suite() { find "$suite" -depth -delete; }
	trap cleanup_suite EXIT HUP INT TERM
	case "$mode" in benchmark|soak) ;; *) echo "benchmark: invalid mode" >&2; exit 64 ;; esac
	case "$events|$warmup|$rounds" in *[!0123456789|]*) echo "benchmark: counts must be integers" >&2; exit 64 ;; esac
	[ "$events" -ge 2 ] && [ "$events" -le 5000 ] && [ "$warmup" -ge 2 ] && [ "$warmup" -le 5000 ] || {
		echo "benchmark: event counts must be between 2 and 5000" >&2; exit 64;
	}
	if [ "$mode" = benchmark ]; then
		[ "$rounds" -ge 3 ] && [ "$rounds" -le 10 ] || { echo "benchmark: benchmark needs 3-10 rounds" >&2; exit 64; }
	else
		[ "$rounds" -eq 1 ] || { echo "benchmark: soak uses exactly one measured round" >&2; exit 64; }
	fi
	case "$output" in /*) ;; *) echo "benchmark: WDE_BENCH_OUTPUT must be absolute" >&2; exit 64 ;; esac
	[ ! -e "$output" ] || { echo "benchmark: output already exists" >&2; exit 64; }
	echo "[suite] warmup separado ($warmup eventos; excluido das estatisticas)"
	WDE_BENCH_SINGLE=1 WDE_BENCH_ROUND=0 WDE_BENCH_EVENTS="$warmup" \
		WDE_BENCH_OUTPUT="$suite/warmup.json" WDE_BENCH_CPU_PROFILE= WDE_BENCH_HEAP_PROFILE= \
		WDE_BENCH_QUIET=1 "$0"
	round=1
	while [ "$round" -le "$rounds" ]; do
		echo "[suite] rodada medida $round/$rounds ($events eventos)"
		cpu_profile= heap_profile=
		if [ "$round" -eq 1 ]; then
			cpu_profile=${WDE_BENCH_CPU_PROFILE:-}
			heap_profile=${WDE_BENCH_HEAP_PROFILE:-}
		fi
		WDE_BENCH_SINGLE=1 WDE_BENCH_ROUND="$round" WDE_BENCH_EVENTS="$events" \
			WDE_BENCH_OUTPUT="$suite/rounds/round-$round.json" WDE_BENCH_CPU_PROFILE="$cpu_profile" \
			WDE_BENCH_HEAP_PROFILE="$heap_profile" WDE_BENCH_QUIET=1 "$0"
		round=$((round + 1))
	done
	go run ./test/benchmark aggregate "$output" "$suite/rounds" "$mode"
	[ "${WDE_BENCH_QUIET:-0}" = 1 ] || cat "$output"
	exit
fi

for command in docker go curl ps; do
	command -v "$command" >/dev/null 2>&1 || { echo "benchmark: missing $command" >&2; exit 1; }
done

bench_dir=$(mktemp -d "${TMPDIR:-/tmp}/wde-benchmark.XXXXXX")
chmod 700 "$bench_dir"
project="wde-bench-$$"
postgres_port=${WDE_BENCH_POSTGRES_PORT:-25432}
api_port=${WDE_BENCH_API_PORT:-28080}
api_ops_port=${WDE_BENCH_API_OPS_PORT:-29090}
worker_ops_port=${WDE_BENCH_WORKER_OPS_PORT:-29091}
receiver_port=${WDE_BENCH_RECEIVER_PORT:-28082}
events=${WDE_BENCH_EVENTS:-1000}
concurrency=${WDE_BENCH_CONCURRENCY:-4}
timeout=${WDE_BENCH_TIMEOUT:-2m}
output=${WDE_BENCH_OUTPUT:-$bench_dir/result.json}
pids=""

compose() {
	COMPOSE_PROJECT_NAME="$project" WDE_POSTGRES_PORT="$postgres_port" docker compose \
		-f compose.yaml -f deployments/docker/compose.benchmark.yaml --profile test "$@"
}

case "$output" in /*) ;; *) echo "benchmark: WDE_BENCH_OUTPUT must be absolute" >&2; exit 64 ;; esac
[ ! -e "$output" ] || { echo "benchmark: output already exists" >&2; exit 64; }

cleanup() {
	for pid in $pids; do kill "$pid" 2>/dev/null || true; done
	for pid in $pids; do wait "$pid" 2>/dev/null || true; done
	compose down --volumes --remove-orphans >/dev/null 2>&1 || true
	find "$bench_dir" -depth -delete
}
trap cleanup EXIT HUP INT TERM

fail() {
	echo "benchmark: $1" >&2
	for log in "$bench_dir"/*.log; do
		[ -f "$log" ] && { echo "--- $(basename "$log")" >&2; tail -20 "$log" >&2; }
	done
	exit 1
}

wait_url() {
	url=$1 count=0
	until curl --silent --fail --max-time 1 "$url" >/dev/null 2>&1; do
		count=$((count + 1))
		[ "$count" -lt 160 ] || fail "timeout waiting for service"
		sleep 0.25
	done
}

api_url="postgres://wde_api:api-local-only@127.0.0.1:$postgres_port/wde?sslmode=disable"
worker_url="postgres://wde_worker:worker-local-only@127.0.0.1:$postgres_port/wde?sslmode=disable"
admin_url="postgres://wde_admin:admin-local-only@127.0.0.1:$postgres_port/wde?sslmode=disable"
migrator_url="postgres://wde_migrator:migrator-local-only@127.0.0.1:$postgres_port/wde?sslmode=disable"

echo "[1/5] PostgreSQL 17 e migrations em ambiente efemero"
compose up --detach --wait postgres >/dev/null
GOOSE_DRIVER=postgres GOOSE_DBSTRING="$migrator_url" go tool goose -dir db/migrations up >/dev/null

echo "[2/5] binarios e credencial sintetica 0600"
go build -trimpath -o "$bench_dir/api" ./cmd/api
go build -trimpath -o "$bench_dir/worker" ./cmd/worker
go build -trimpath -o "$bench_dir/benchmark" ./test/benchmark
WDE_PROFILE=local WDE_ADMIN_DATABASE_URL="$admin_url" WDE_DATABASE_URL="$api_url" \
	"$bench_dir/api" credentials bootstrap --name "Benchmark synthetic" --output-file="$bench_dir/credential.json"

echo "[3/5] API com perfil de carga publicado"
WDE_PROFILE=local WDE_DATABASE_URL="$api_url" WDE_API_HTTP_ADDR="127.0.0.1:$api_port" \
	WDE_API_OPERATIONAL_ADDR="127.0.0.1:$api_ops_port" WDE_ALLOW_HTTP_DESTINATIONS=true \
	WDE_EDGE_MAX_IN_FLIGHT=512 WDE_EDGE_GLOBAL=200000 WDE_EDGE_ORIGIN=200000 WDE_EDGE_PREFIX=200000 \
	WDE_QUOTA_INGEST_GLOBAL=200000 WDE_QUOTA_INGEST_WORKSPACE=200000 WDE_QUOTA_INGEST_API_KEY=200000 \
	"$bench_dir/api" >"$bench_dir/api.log" 2>&1 & api_pid=$! pids="$pids $!"
wait_url "http://127.0.0.1:$api_ops_port/readyz"
api_rss_before=$(ps -o rss= -p "$api_pid" | tr -d ' ')

echo "[4/5] ingestao e depois delivery assinado ($events eventos, concorrencia $concurrency)"
"$bench_dir/benchmark" -api "http://127.0.0.1:$api_port" -credential "$bench_dir/credential.json" \
	-listen "127.0.0.1:$receiver_port" -target "http://127.0.0.1:$receiver_port/webhook" \
	-events "$events" -concurrency "$concurrency" -timeout "$timeout" -output "$output" \
	-round "${WDE_BENCH_ROUND:-1}" \
	-ingested-signal "$bench_dir/ingested" -cpu-profile "${WDE_BENCH_CPU_PROFILE:-}" \
	-delivered-signal "$bench_dir/delivered" -database-status "$bench_dir/database.json" \
	-system-samples "$bench_dir/system.tsv" \
	-heap-profile "${WDE_BENCH_HEAP_PROFILE:-}" >"$bench_dir/benchmark.log" 2>&1 &
bench_pid=$! pids="$pids $!"
phase_wait=0
while [ ! -f "$bench_dir/ingested" ]; do
	kill -0 "$bench_pid" 2>/dev/null || fail "load generator failed before completing ingestion"
	phase_wait=$((phase_wait + 1))
	[ "$phase_wait" -lt 1200 ] || fail "ingestion phase timed out"
	sleep 0.1
done
# The dataset is loaded faster than autovacuum's analyze cycle. Refreshing statistics
# makes the delivery phase representative instead of measuring a stale cold plan.
compose exec -T postgres \
	psql -U postgres -d wde -v ON_ERROR_STOP=1 -c \
	"ANALYZE wde.events; ANALYZE wde.deliveries; ANALYZE wde.delivery_attempts;" >/dev/null
WDE_PROFILE=local WDE_DATABASE_URL="$worker_url" WDE_WORKER_OPERATIONAL_ADDR="127.0.0.1:$worker_ops_port" \
	WDE_ALLOW_HTTP_DESTINATIONS=true WDE_WORKER_CONCURRENCY=100 WDE_WORKER_CLAIM_BATCH_SIZE=100 \
	WDE_WORKER_WORKSPACE_BATCH_LIMIT=100 WDE_WORKER_ENDPOINT_BATCH_LIMIT=100 WDE_WORKER_POLL_INTERVAL=2s \
	WDE_WORKER_CLAIM_TIMEOUT=2s WDE_WORKER_REQUEST_TIMEOUT=1s WDE_WORKER_LEASE_TTL=20s \
	"$bench_dir/worker" >"$bench_dir/worker.log" 2>&1 & worker_pid=$! pids="$pids $!"
wait_url "http://127.0.0.1:$worker_ops_port/readyz"
worker_rss_before=$(ps -o rss= -p "$worker_pid" | tr -d ' ')

sample_system() {
	elapsed=0 samples=0
	while :; do
		api_rss=$(ps -o rss= -p "$api_pid" | tr -d ' ')
		worker_rss=$(ps -o rss= -p "$worker_pid" | tr -d ' ')
		queue=$(compose exec -T postgres psql -U postgres -d wde -Atc \
			"SELECT count(*) FILTER (WHERE status IN ('pending','retry_scheduled','in_progress')),count(*) FILTER (WHERE status='succeeded') FROM wde.deliveries")
		case "$api_rss|$worker_rss|$queue" in *[!0123456789|]*) return 1 ;; esac
		old_ifs=$IFS; IFS='|'; set -- $queue; IFS=$old_ifs
		[ "$#" -eq 2 ] || return 1
		printf '%s|%s|%s|%s|%s\n' "$elapsed" "$api_rss" "$worker_rss" "$1" "$2" >>"$bench_dir/system.tmp"
		samples=$((samples + 1))
		if [ -f "$bench_dir/sampling.done" ] && [ "$samples" -ge 2 ]; then break; fi
		elapsed=$((elapsed + 250))
		sleep 0.25
	done
}
sample_system & sampler_pid=$!; pids="$pids $sampler_pid"

database_wait=0
while [ ! -f "$bench_dir/delivered" ]; do
	kill -0 "$bench_pid" 2>/dev/null || fail "load generator failed before unique deliveries completed"
	database_wait=$((database_wait + 1))
	[ "$database_wait" -lt 2400 ] || fail "unique delivery phase timed out"
	sleep 0.1
done
while :; do
	counts=$(compose exec -T postgres \
		psql -U postgres -d wde -Atc "SELECT count(*) FILTER (WHERE status IN ('pending','retry_scheduled','in_progress')),count(*) FILTER (WHERE status='succeeded'),count(*) FILTER (WHERE status IN ('failed_permanent','dead_letter','payload_expired')),count(*) FROM wde.deliveries")
	case "$counts" in *[!0123456789|]*) fail "database returned unsafe queue counters" ;; esac
	old_ifs=$IFS; IFS='|'; set -- $counts; IFS=$old_ifs
	[ "$#" -eq 4 ] || fail "database returned incomplete queue counters"
	backlog=$1 succeeded=$2 failed=$3 total=$4
	if [ "$backlog" -eq 0 ] && [ "$succeeded" -eq "$events" ] && [ "$failed" -eq 0 ] && [ "$total" -eq "$events" ]; then
		break
	fi
	database_wait=$((database_wait + 1))
	[ "$database_wait" -lt 2400 ] || fail "database queue did not converge"
	sleep 0.1
done
: >"$bench_dir/sampling.done"
if ! wait "$sampler_pid"; then fail "system sampler failed"; fi
mv "$bench_dir/system.tmp" "$bench_dir/system.tsv"
printf '{"backlog":%s,"succeeded":%s,"failed":%s,"total":%s}\n' \
	"$backlog" "$succeeded" "$failed" "$total" >"$bench_dir/database.tmp"
mv "$bench_dir/database.tmp" "$bench_dir/database.json"
if ! wait "$bench_pid"; then fail "load generator failed"; fi

rss_before="api=${api_rss_before}KiB worker=${worker_rss_before}KiB"
rss_after="api=$(ps -o rss= -p "$api_pid" | tr -d ' ')KiB worker=$(ps -o rss= -p "$worker_pid" | tr -d ' ')KiB"
echo "[5/5] resultado (RSS before: $rss_before; after: $rss_after; backlog/succeeded: $backlog|$succeeded)"
[ "${WDE_BENCH_QUIET:-0}" = 1 ] || cat "$output"
