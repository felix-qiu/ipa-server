#!/bin/sh
set -eu

port="${PORT:-8080}"
data_dir="${DATA_DIR:-/app/upload}"
db_path="${DB_PATH:-${data_dir}/ipa-server.db}"
meta_path="${META_PATH:-appList.json}"
public_url="${PUBLIC_URL:-${DOMAIN:-}}"
remote="${REMOTE:-}"
remote_url="${REMOTE_URL:-}"

if { [ -n "$remote" ] && [ -z "$remote_url" ]; } || { [ -z "$remote" ] && [ -n "$remote_url" ]; }; then
  echo "REMOTE and REMOTE_URL must be configured together" >&2
  exit 1
fi

set -- /usr/local/bin/ipasd \
  -addr 0.0.0.0 \
  -port "$port" \
  -dir "$data_dir" \
  -db-path "$db_path" \
  -meta-path "$meta_path"

if [ -n "$public_url" ]; then
  set -- "$@" -public-url "$public_url"
fi
if [ -n "$remote" ]; then
  set -- "$@" -remote "$remote" -remote-url "$remote_url"
fi
if [ -n "${LOGIN_USER:-}" ]; then
  set -- "$@" -user "$LOGIN_USER"
fi
if [ -n "${LOGIN_PASS:-}" ]; then
  set -- "$@" -pass "$LOGIN_PASS"
fi
case "${DELETE_ENABLED:-false}" in
  true|1) set -- "$@" -del ;;
esac
case "${UPLOAD_DISABLED:-false}" in
  true|1) set -- "$@" -upload-disabled ;;
esac

exec "$@"
