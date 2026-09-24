#!/bin/sh
set -eu
umask 077

fail() {
	echo "image-layout: $*" >&2
	exit 1
}

for command in find go mktemp; do
	command -v "$command" >/dev/null 2>&1 || fail "missing $command"
done

repo=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/wde-image-layout.XXXXXX")
base_container=""
target_container=""
cleanup() {
	[ -z "$base_container" ] || docker rm -f "$base_container" >/dev/null 2>&1 || true
	[ -z "$target_container" ] || docker rm -f "$target_container" >/dev/null 2>&1 || true
	find "$work" -depth -delete
}
trap cleanup EXIT HUP INT TERM

(cd "$repo" && go build -trimpath -o "$work/image-manifest" ./test/imagelayout)

if [ "$#" -eq 4 ] && [ "$1" = "--manifests" ]; then
	"$work/image-manifest" verify "$2" "$3" "$4"
	exit
fi
if [ "$#" -ne 2 ]; then
	echo "usage: $0 IMAGE runtime|migrator" >&2
	echo "       $0 --manifests BASE TARGET EXPECTED" >&2
	exit 64
fi
for command in awk docker grep; do
	command -v "$command" >/dev/null 2>&1 || fail "missing $command"
done

image=$1
kind=$2
case "$kind" in
	runtime)
		base=$(awk '$1 == "FROM" && $0 ~ / AS runtime$/ { print $2 }' "$repo/deployments/docker/Dockerfile")
		;;
	migrator)
		base=$(awk '$1 == "FROM" { found=$2 } END { print found }' "$repo/deployments/docker/Dockerfile.migrate")
		;;
	*) fail "invalid kind" ;;
esac
printf '%s\n' "$base" | grep -Eq '^.+@sha256:[0-9a-f]{64}$' || fail "final base is not pinned by digest"

base_container=$(docker create --entrypoint /__wde_inventory__ "$base")
docker export --output "$work/base.tar" "$base_container"
target_container=$(docker create --entrypoint /__wde_inventory__ "$image")
docker export --output "$work/target.tar" "$target_container"

"$work/image-manifest" manifest "$work/base.tar" "$work/base.manifest"
"$work/image-manifest" manifest "$work/target.tar" "$work/target.manifest"
"$work/image-manifest" expected "$kind" "$repo" "$work/expected.manifest"
"$work/image-manifest" verify "$work/base.manifest" "$work/target.manifest" "$work/expected.manifest"
