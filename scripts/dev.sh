#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

build_dir="$(mktemp -d "${TMPDIR:-/tmp}/finfe-dev.XXXXXX")"
app_pid=""

stop_app() {
  if [[ -n "$app_pid" ]]; then
    kill "$app_pid" 2>/dev/null || true
    wait "$app_pid" 2>/dev/null || true
    app_pid=""
  fi
}

cleanup() {
  stop_app
  rm -rf "$build_dir"
}

trap 'exit 130' INT
trap 'exit 143' TERM
trap cleanup EXIT

fingerprint() {
  {
    find cmd internal -type f -name '*.go' -print
    printf '%s\n' go.mod go.sum
  } | LC_ALL=C sort | while IFS= read -r file; do
    [[ ! -f "$file" ]] || cksum "$file"
  done | cksum
}

restart_app() {
  stop_app
  echo 'Building API...'
  if go build -o "$build_dir/finfe-api" ./cmd/api; then
    "$build_dir/finfe-api" &
    app_pid=$!
    echo "API started (PID $app_pid). Watching Go files, go.mod, and go.sum."
  else
    echo 'Build failed. Waiting for another file change.' >&2
  fi
}

last_fingerprint="$(fingerprint)"
restart_app

while true; do
  sleep 1
  current_fingerprint="$(fingerprint)"
  if [[ "$current_fingerprint" != "$last_fingerprint" ]]; then
    last_fingerprint="$current_fingerprint"
    echo 'Change detected; restarting API...'
    restart_app
  fi
done
