#!/usr/bin/env bash
# Downloads the archived day files the site needs (the last 49 months) into DIR.
set -euo pipefail
dir="${1:?dir}"
mkdir -p "$dir"
oldest="$(date -u -d '49 months ago' +%Y-%m)"
for tag in $(gh release list --limit 1000 --json tagName --jq '.[].tagName | select(startswith("days-"))'); do
  [[ "${tag#days-}" < "$oldest" ]] && continue
  gh release download "$tag" -D "$dir" -p '20*.jsonl.gz' --clobber
done
ls "$dir" | wc -l
