#!/usr/bin/env bash
# Downloads the newest earnings estimates from the release "earnings" and
# unpacks them to FILE. Exits 0 with no FILE when there are none, or when the
# asset is too big or does not unpack to a bounded size (the build then leaves
# v1/earnings.json out); the build checks the file again. FILE should be
# outside the checkout.
#
#   scripts/fetch-earnings.sh FILE
set -euo pipefail
file="${1:?file}"
max_asset=10000000    # bytes, compressed (the file is about 50 KB)
max_unpacked=2097152  # bytes, the build's own limit
skip() { echo "::warning::$*, not published"; rm -f "$file"; exit 0; }

assets=$(gh release view earnings --json assets --jq '.assets[] | "\(.name) \(.size)"' 2>/dev/null) || skip "no earnings release"
newest=$(awk '$1 ~ /^earnings-[0-9]{8}\.json\.gz$/ {print $1}' <<<"$assets" | sort | tail -n 1)
[ -n "$newest" ] || skip "no earnings snapshot"
size=$(awk -v n="$newest" '$1 == n {print $2}' <<<"$assets")
[ "$size" -le "$max_asset" ] || skip "$newest is $size bytes"
tmp=$(mktemp -d)
gh release download earnings -p "$newest" -D "$tmp"
# One byte past the limit is enough to know it is too big.
set +o pipefail
timeout 60 gzip -dc "$tmp/$newest" 2>/dev/null | head -c "$((max_unpacked + 1))" > "$tmp/earnings.json"
rc=${PIPESTATUS[0]}
set -o pipefail
unpacked=$(stat -c %s "$tmp/earnings.json")
{ [ "$rc" = 0 ] || [ "$unpacked" -gt "$max_unpacked" ]; } || skip "$newest could not be unpacked"
[ "$unpacked" -le "$max_unpacked" ] || skip "$newest unpacks to over $max_unpacked bytes"
mv "$tmp/earnings.json" "$file"
echo "$newest: $unpacked bytes"
