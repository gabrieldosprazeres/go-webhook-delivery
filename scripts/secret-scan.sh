#!/bin/sh
set -eu
umask 077

tools=$(mktemp -d "${TMPDIR:-/tmp}/wde-secret-tools.XXXXXX")
cleanup() { find "$tools" -depth -delete; }
trap cleanup EXIT HUP INT TERM
"$(dirname "$0")/fetch-security-tools.sh" "$tools" gitleaks
"$tools/gitleaks" git --redact --no-banner --exit-code 1 .
snapshot="$tools/worktree"
mkdir -m 700 "$snapshot"
git ls-files --cached --others --exclude-standard -z | tar --null -T - -cf - | tar -xf - -C "$snapshot"
"$tools/gitleaks" dir --redact --no-banner --exit-code 1 "$snapshot"
