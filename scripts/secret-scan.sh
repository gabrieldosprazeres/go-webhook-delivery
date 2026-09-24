#!/bin/sh
set -eu
umask 077

tools=$(mktemp -d "${TMPDIR:-/tmp}/wde-secret-tools.XXXXXX")
cleanup() { find "$tools" -depth -delete; }
trap cleanup EXIT HUP INT TERM
"$(dirname "$0")/fetch-security-tools.sh" "$tools" gitleaks
"$tools/gitleaks" git --redact --no-banner --exit-code 1 .
