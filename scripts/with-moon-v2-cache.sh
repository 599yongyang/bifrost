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

# Resolve the active Go 1.27 toolchain before switching GOMODCACHE. When the
# bootstrap `go` is an older release using automatic toolchains, its downloaded
# Go 1.27 binary lives in the user module cache. Changing GOMODCACHE first would
# make it download the same toolchain again and fail in offline/sandboxed runs.
bootstrap_go="${BIFROST_V2_GO:-$(command -v go)}"
resolved_go_root="$(GOTOOLCHAIN=go1.27.0+auto "$bootstrap_go" env GOROOT)"
resolved_go="$resolved_go_root/bin/go"
if [[ ! -x "$resolved_go" || "$("$resolved_go" version)" != go\ version\ go1.27.* ]]; then
  echo "Moon v2 requires an installed Go 1.27 toolchain; resolved: $resolved_go" >&2
  exit 1
fi

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

mkdir -p "$cache_root/go-build-host" "$cache_root/go-mod"

export GOCACHE="$cache_root/go-build-host"
export GOMODCACHE="$cache_root/go-mod"
export GOTOOLCHAIN=local
export PATH="$resolved_go_root/bin:$PATH"

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
  workspace_tmp_dir="$(mktemp -d "$cache_root/.go-work.XXXXXX")"
  if ! (
    cd "$workspace_tmp_dir"
    GOWORK=off "$resolved_go" work init "${module_dirs[@]}"
  ); then
    rm -rf "$workspace_tmp_dir"
    exit 1
  fi
  mv -f "$workspace_tmp_dir/go.work" "$workspace"
  rmdir "$workspace_tmp_dir"
fi

export GOWORK="$workspace"

if [[ "$1" == "go" ]]; then
  shift
  exec "$resolved_go" "$@"
fi
exec "$@"
