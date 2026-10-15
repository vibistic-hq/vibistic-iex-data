#!/usr/bin/env bash
# Prints the dates (YYYYMMDD) already archived as release assets.
set -euo pipefail
for tag in $(gh release list --limit 1000 --json tagName --jq '.[].tagName | select(startswith("days-"))'); do
  gh release view "$tag" --json assets --jq '.assets[].name' | sed -n 's/^\(20[0-9]\{6\}\)\.jsonl\.gz$/\1/p'
done | sort
