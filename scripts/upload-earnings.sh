#!/usr/bin/env bash
# Uploads an earningsbuild FILE as earnings-YYYYMMDD.json.gz to the release
# "earnings" (created if missing), then keeps only the newest 14. Needs
# GH_TOKEN with contents: write.
#
#   scripts/upload-earnings.sh FILE
set -euo pipefail
file="${1:?file}"
[ -s "$file" ] || { echo "no $file" >&2; exit 2; }
name="earnings-$(date -u +%Y%m%d).json.gz"
gzip -9 -c "$file" > "$name"
if ! gh release view earnings >/dev/null 2>&1; then
  gh release create earnings --title "Earnings date estimates" \
    --notes "Daily snapshots of v1/earnings.json (each company's next earnings release date, estimated from its past SEC 8-K Item 2.02 filings by cmd/earningsbuild from SEC's bulk submissions.zip). Published to GitHub Pages by the publish workflow." \
    >/dev/null 2>&1 || gh release view earnings >/dev/null
fi
gh release upload earnings "$name" --clobber
for old in $(gh release view earnings --json assets --jq '.assets[].name' | grep -E '^earnings-[0-9]{8}\.json\.gz$' | sort -r | tail -n +15); do
  gh release delete-asset earnings "$old" --yes
done
