#!/bin/sh
set -eu
umask 077

[ -n "${WDE_TEST_API_DATABASE_URL:-}" ] || { echo "adversarial: WDE_TEST_API_DATABASE_URL is required" >&2; exit 64; }
[ -n "${WDE_TEST_WORKER_DATABASE_URL:-}" ] || { echo "adversarial: WDE_TEST_WORKER_DATABASE_URL is required" >&2; exit 64; }
[ -n "${WDE_TEST_ADMIN_DATABASE_URL:-}" ] || { echo "adversarial: WDE_TEST_ADMIN_DATABASE_URL is required" >&2; exit 64; }
[ -n "${WDE_TEST_CONSOLE_DATABASE_URL:-}" ] || { echo "adversarial: WDE_TEST_CONSOLE_DATABASE_URL is required" >&2; exit 64; }
[ -n "${WDE_TEST_SUPERUSER_DATABASE_URL:-}" ] || { echo "adversarial: WDE_TEST_SUPERUSER_DATABASE_URL is required" >&2; exit 64; }

report=$(mktemp "${TMPDIR:-/tmp}/wde-adversarial.XXXXXX")
cleanup() { rm -f "$report"; }
trap cleanup EXIT HUP INT TERM

if ! go test -p 1 -count=1 -timeout=12m -v ./... >"$report" 2>&1; then
	cat "$report"
	exit 1
fi
cat "$report"
if grep -q -- '--- SKIP:' "$report"; then
	echo "adversarial: skipped tests are forbidden" >&2
	exit 1
fi
