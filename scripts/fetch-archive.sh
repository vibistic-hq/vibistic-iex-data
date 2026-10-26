#!/usr/bin/env bash
# Downloads the archived day files the site needs (the last 49 months) into DIR.
set -euo pipefail
dir="${1:?dir}"
mkdir -p "$dir"
# Counted from the 1st of this month: '49 months ago' on the 31st of a month
# rolls forward when the target month is shorter, skipping the oldest month
# (issue #10).
oldest="$(date -u -d "$(date -u +%Y-%m-01) -49 months" +%Y-%m)"
# Listed first, so a failed listing stops the script (inside a for list it would
# just give no tags). Only days-YYYY-MM releases, and each file must belong to its
# release's month: a misfiled asset would otherwise silently replace another
# month's file of the same name (security audit run-1).
tags="$(gh release list --limit 1000 --json tagName --jq '.[].tagName | select(test("^days-[0-9]{4}-[0-9]{2}$"))')"
for tag in $tags; do
  [[ "${tag#days-}" < "$oldest" ]] && continue
  # A month's release with no day file yet (archive-day.sh creates the
  # release before uploading, so a failed upload leaves it empty) holds
  # nothing to fetch.
  if [ "$(gh release view "$tag" --json assets --jq '[.assets[].name | select(startswith("20") and endswith(".jsonl.gz"))] | length')" = 0 ]; then
    echo "::warning::$tag has no day files" >&2
    continue
  fi
  month="${tag#days-}"; month="${month/-/}"
  part="$dir/.part-$tag"
  rm -rf "$part"
  gh release download "$tag" -D "$part" -p '20*.jsonl.gz'
  for f in "$part"/*; do
    name="$(basename "$f")"
    [[ "$name" =~ ^${month}[0-9]{2}\.jsonl\.gz$ ]] || { echo "$tag holds $name, not a day of that month" >&2; exit 1; }
    # Tags are unique and each day must be in its own month's release, so no day
    # comes from two releases; a file already in DIR from an earlier or interrupted
    # run is simply replaced (Codex review of #17).
    mv -f "$f" "$dir/$name"
  done
  rmdir "$part"
done
ls "$dir" | wc -l
