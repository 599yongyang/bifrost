#!/usr/bin/env bash

set -euo pipefail

if [[ $# -eq 0 ]]; then
  echo "usage: scripts/with-moon-v2-cache.sh <command> [args...]" >&2
  exit 2
fi

if [[ -n "${BIFROST_V2_CACHE_ROOT:-}" ]]; then
  cache_root="$BIFROST_V2_CACHE_ROOT"
elif [[ "$(uname -s)" == "Darwin" ]]; then
  cache_root="/private/tmp/moon-bifrost-v2-cache"
else
  cache_root="${TMPDIR:-/tmp}/moon-bifrost-v2-cache"
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
workspace="$cache_root/go.work"
cache_max_gib="${BIFROST_V2_CACHE_MAX_GIB:-8}"

if [[ ! "$cache_max_gib" =~ ^[1-9][0-9]*$ ]]; then
  echo "BIFROST_V2_CACHE_MAX_GIB must be a positive integer" >&2
  exit 2
fi

if [[ -d "$cache_root" ]]; then
  cache_size_kib="$(du -sk "$cache_root" | awk '{print $1}')"
  cache_max_kib="$((cache_max_gib * 1024 * 1024))"
  if (( cache_size_kib > cache_max_kib )); then
    echo "Moon v2 cache exceeds ${cache_max_gib}GiB: $cache_root" >&2
    echo "Remove this single cache root or explicitly raise BIFROST_V2_CACHE_MAX_GIB." >&2
    exit 1
  fi
fi

module_dirs=("$repo_root/cli" "$repo_root/core" "$repo_root/framework" "$repo_root/transports")
for module_file in "$repo_root"/plugins/*/go.mod; do
  module_dirs+=("$(dirname "$module_file")")
done

mkdir -p "$cache_root/go-build-host"

export GOCACHE="$cache_root/go-build-host"
export GOTOOLCHAIN=go1.27.0+auto

workspace_needs_refresh=false
if [[ ! -f "$workspace" ]]; then
  workspace_needs_refresh=true
else
  for module_dir in "${module_dirs[@]}"; do
    if ! grep -Fq "$module_dir" "$workspace"; then
      workspace_needs_refresh=true
      break
    fi
  done
fi

if [[ "$workspace_needs_refresh" == true ]]; then
  rm -f "$workspace" "$cache_root/go.work.sum"
  (
    cd "$cache_root"
    GOWORK=off go work init "${module_dirs[@]}"
  )
fi

export GOWORK="$workspace"

exec "$@"
