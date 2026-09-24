#!/bin/sh
set -eu
umask 077

[ -n "${WDE_MIGRATOR_DATABASE_URL:-}" ] || {
	echo "guarded-down: WDE_MIGRATOR_DATABASE_URL is required" >&2
	exit 64
}
target=${WDE_ROLLBACK_TARGET:-}
case "$target" in ''|*[!0-9]*) echo "guarded-down: numeric WDE_ROLLBACK_TARGET is required" >&2; exit 64 ;; esac
[ "$target" -le 19 ] || { echo "guarded-down: target must be at most 19" >&2; exit 64; }

go run ./test/migrationguard
GOOSE_DRIVER=postgres GOOSE_DBSTRING="$WDE_MIGRATOR_DATABASE_URL" \
	go tool goose -dir db/migrations down-to "$target"
