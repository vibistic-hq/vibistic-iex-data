#!/usr/bin/env bash
# Downloads the newest SEC snapshot from the release "sec" and unpacks it into
# DIR. Exits 0 with nothing in DIR when there is none or it is not a plain
# snapshot (the build then leaves v1/sec out); the build checks every file.
#
#   scripts/fetch-sec-snapshot.sh DIR
set -euo pipefail
dir="${1:?dir}"
newest=$(gh release view sec --json assets --jq '.assets[].name' 2>/dev/null | grep -E '^sec-[0-9]{8}\.tar\.gz$' | sort | tail -n 1 || true)
if [ -z "$newest" ]; then
  echo "::warning::no SEC snapshot to publish"
  exit 0
fi
tmp=$(mktemp -d)
gh release download sec -p "$newest" -D "$tmp"
# Only symbol files and snapshot.json, nothing else (no paths, no links).
if tar -tvzf "$tmp/$newest" | awk '{print substr($1,1,1)}' | grep -qv '^[-d]' ||
  tar -tzf "$tmp/$newest" | grep -vxE '\./|\./snapshot\.json|\./[A-Z]{1,5}(\.[A-Z])?\.json' | grep -q .; then
  echo "::warning::$newest holds unexpected entries, not published"
  exit 0
fi
mkdir -p "$dir"
tar -C "$dir" -xzf "$tmp/$newest" --no-same-owner --no-same-permissions
echo "$newest: $(ls "$dir" | wc -l) files"
