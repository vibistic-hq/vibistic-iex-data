#!/usr/bin/env bash
# Uploads a secbuild snapshot DIR as sec-YYYYMMDD.tar.gz to the release "sec"
# (created if missing), then keeps only the newest 14 snapshots. Needs GH_TOKEN
# with contents: write.
#
#   scripts/upload-sec-snapshot.sh DIR
set -euo pipefail
dir="${1:?dir}"
[ -f "$dir/snapshot.json" ] || { echo "no snapshot.json in $dir" >&2; exit 2; }
name="sec-$(date -u +%Y%m%d).tar.gz"
tar -C "$dir" -czf "$name" .
if ! gh release view sec >/dev/null 2>&1; then
  gh release create sec --title "SEC revenue and net income snapshots" \
    --notes "Daily snapshots of v1/sec (revenue and net income from SEC EDGAR filings, built by cmd/secbuild from SEC's bulk companyfacts.zip). Published to GitHub Pages by the publish workflow." \
    >/dev/null 2>&1 || gh release view sec >/dev/null
fi
gh release upload sec "$name" --clobber
for old in $(gh release view sec --json assets --jq '.assets[].name' | grep -E '^sec-[0-9]{8}\.tar\.gz$' | sort -r | tail -n +15); do
  gh release delete-asset sec "$old" --yes
done
