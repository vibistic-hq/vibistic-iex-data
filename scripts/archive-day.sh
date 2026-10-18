#!/usr/bin/env bash
# Uploads DIR/YYYYMMDD.jsonl.gz as an asset of the monthly release
# days-YYYY-MM (created if missing). Needs GH_TOKEN with contents: write.
#
#   scripts/archive-day.sh YYYYMMDD DIR
set -euo pipefail
d="${1:?date YYYYMMDD}"
dir="${2:?dir}"
[[ "$d" =~ ^20[0-9]{6}$ ]] || { echo "bad date: $d" >&2; exit 2; }
tag="days-${d:0:4}-${d:4:2}"
if ! gh release view "$tag" >/dev/null 2>&1; then
  # Parallel jobs may race to create it; a failure here is fine if it exists.
  gh release create "$tag" --title "IEX day summaries ${d:0:4}-${d:4:2}" \
    --notes "Per-symbol daily summaries of IEX trades (one file per session), built from IEX HIST TOPS. Data provided for free by IEX. By accessing or using IEX Historical Data, you agree to the IEX Historical Data Terms of Use: https://iextrading.com/iex-historical-data-terms/" \
    >/dev/null 2>&1 || gh release view "$tag" >/dev/null
fi
gh release upload "$tag" "$dir/$d.jsonl.gz" --clobber
