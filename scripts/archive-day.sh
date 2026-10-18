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
# A new month's release is created WITH its first file, in one call: a
# release created empty, then a failed upload, left an empty release that
# made scripts/fetch-archive.sh fail (issue #10). Parallel jobs may race to
# create it; the loser falls through to a plain upload.
if ! gh release view "$tag" >/dev/null 2>&1 &&
  gh release create "$tag" "$dir/$d.jsonl.gz" --title "IEX day summaries ${d:0:4}-${d:4:2}" \
    --notes "Per-symbol daily summaries of IEX trades (one file per session), built from IEX HIST TOPS. Data provided for free by IEX. By accessing or using IEX Historical Data, you agree to the IEX Historical Data Terms of Use: https://iextrading.com/iex-historical-data-terms/" \
    >/dev/null 2>&1; then
  exit 0
fi
gh release upload "$tag" "$dir/$d.jsonl.gz" --clobber
