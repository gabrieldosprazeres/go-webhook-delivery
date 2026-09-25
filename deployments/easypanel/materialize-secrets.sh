#!/usr/bin/env bash
set -euo pipefail
umask 077

secret_names=(
  migrator_pgpass
  api_pgpass
  worker_pgpass
  auth_pepper
  idempotency_pepper
  fingerprint_pepper
  rate_limit_pepper
  cursor_pepper
  payload_keyring
  signing_keyring
)

for secret_name in "${secret_names[@]}"; do
  source_path="/run/secrets/${secret_name}"
  temporary_path="/materialized/.${secret_name}.new"
  target_path="/materialized/${secret_name}"
  if [[ ! -f "${source_path}" || ! -r "${source_path}" ]]; then
    echo "missing required runtime secret: ${secret_name}" >&2
    exit 1
  fi
  install -o 65532 -g 65532 -m 0400 "${source_path}" "${temporary_path}"
  mv -f "${temporary_path}" "${target_path}"
done
