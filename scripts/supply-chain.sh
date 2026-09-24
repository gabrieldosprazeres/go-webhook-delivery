#!/bin/sh
set -eu
umask 077

if [ "$#" -lt 2 ]; then
  echo "usage: $0 OUTPUT_DIRECTORY IMAGE [IMAGE ...]" >&2
  exit 64
fi

output=$1
shift
case "$output" in
  /*) ;;
  *) echo "OUTPUT_DIRECTORY must be absolute" >&2; exit 64 ;;
esac
mkdir -p "$output"
chmod 700 "$output"
tools=$(mktemp -d "${TMPDIR:-/tmp}/wde-supply-tools.XXXXXX")
db=$(mktemp -d "${TMPDIR:-/tmp}/wde-trivy-db.XXXXXX")
cleanup() { find "$tools" "$db" -depth -delete; }
trap cleanup EXIT HUP INT TERM
"$(dirname "$0")/fetch-security-tools.sh" "$tools" syft,trivy,gitleaks
cp "$tools/tools.tsv" "$output/tools.tsv"
if [ -z "${DOCKER_HOST:-}" ]; then
  DOCKER_HOST=$(docker context inspect "$(docker context show)" --format '{{.Endpoints.docker.Host}}')
  export DOCKER_HOST
fi

manifest="$output/images.tsv"
: > "$manifest"
for image in "$@"; do
  image_id=$(docker image inspect --format '{{.Id}}' "$image")
  case "$image_id" in sha256:*) ;; *) echo "invalid image ID: $image_id" >&2; exit 65 ;; esac
  name=$(printf '%s' "$image" | tr '/:@' '____' | tr -cd 'A-Za-z0-9_.-')
  cyclonedx="$output/$name.cyclonedx.json"
  spdx="$output/$name.spdx.json"
  scan="$output/$name.trivy.json"
  "$tools/syft" "docker:$image_id" --quiet -o "cyclonedx-json=$cyclonedx"
  "$tools/syft" "docker:$image_id" --quiet -o "spdx-json=$spdx"
  "$tools/trivy" image --cache-dir "$db" --scanners vuln --severity HIGH,CRITICAL \
    --exit-code 1 --format json --output "$scan" "$image_id"
  printf '%s\t%s\n' "$image" "$image_id" >> "$manifest"
done
"$tools/gitleaks" git --redact --no-banner --exit-code 1 --report-format json \
  --report-path "$output/gitleaks.json" .
test -s "$output/gitleaks.json" || { echo "Gitleaks report missing" >&2; exit 68; }
test -s "$db/db/metadata.json" || { echo "Trivy DB metadata missing" >&2; exit 67; }
cp "$db/db/metadata.json" "$output/trivy-db-metadata.json"
chmod 600 "$output"/*.json "$output"/*.tsv
