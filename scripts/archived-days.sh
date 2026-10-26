#!/usr/bin/env bash
# Prints the dates (YYYYMMDD) already archived as release assets.
set -euo pipefail
# Listed first, so a failed listing stops the script instead of looking like an
# empty archive (security audit run-1).
tags="$(gh release list --limit 1000 --json tagName --jq '.[].tagName | select(test("^days-[0-9]{4}-[0-9]{2}$"))')"
for tag in $tags; do
  gh release view "$tag" --json assets --jq '.assets[].name' | sed -n 's/^\(20[0-9]\{6\}\)\.jsonl\.gz$/\1/p'
done | sort
