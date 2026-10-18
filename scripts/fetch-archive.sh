#!/usr/bin/env bash
# Downloads the archived day files the site needs (the last 49 months) into DIR.
set -euo pipefail
dir="${1:?dir}"
mkdir -p "$dir"
# Counted from the 1st of this month: '49 months ago' on the 31st of a month
# rolls forward when the target month is shorter, skipping the oldest month
# (issue #10).
oldest="$(date -u -d "$(date -u +%Y-%m-01) -49 months" +%Y-%m)"
for tag in $(gh release list --limit 1000 --json tagName --jq '.[].tagName | select(startswith("days-"))'); do
  [[ "${tag#days-}" < "$oldest" ]] && continue
  # A month's release with no day file yet (archive-day.sh creates the
  # release before uploading, so a failed upload leaves it empty) holds
  # nothing to fetch.
  if [ "$(gh release view "$tag" --json assets --jq '[.assets[].name | select(startswith("20") and endswith(".jsonl.gz"))] | length')" = 0 ]; then
    echo "::warning::$tag has no day files" >&2
    continue
  fi
  gh release download "$tag" -D "$dir" -p '20*.jsonl.gz' --clobber
done
ls "$dir" | wc -l
