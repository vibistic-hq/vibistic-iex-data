#!/usr/bin/env bash
# Downloads the newest SEC snapshot from the release "sec" and unpacks it into
# DIR. Exits 0 with nothing in DIR when there is none, or when it is not a
# plain snapshot of bounded size (the build then leaves v1/sec out); the build
# checks every file again. DIR should be outside the checkout.
#
#   scripts/fetch-sec-snapshot.sh DIR
set -euo pipefail
dir="${1:?dir}"
max_asset=100000000     # bytes, compressed (a snapshot is a few MB)
max_entries=20000
max_unpacked=500000000  # bytes
skip() { echo "::warning::$*, not published"; exit 0; }

assets=$(gh release view sec --json assets --jq '.assets[] | "\(.name) \(.size)"' 2>/dev/null) || skip "no SEC release"
newest=$(awk '$1 ~ /^sec-[0-9]{8}\.tar\.gz$/ {print $1}' <<<"$assets" | sort | tail -n 1)
[ -n "$newest" ] || skip "no SEC snapshot"
size=$(awk -v n="$newest" '$1 == n {print $2}' <<<"$assets")
[ "$size" -le "$max_asset" ] || skip "$newest is $size bytes"
tmp=$(mktemp -d)
gh release download sec -p "$newest" -D "$tmp"
# The whole listing first (no early exit that could hide a bad entry), then
# every line checked: plain files named like a symbol or snapshot.json, and the
# top directory; bounded count and size.
# The listing itself is capped (long owner names or PAX headers could make it
# huge): a listing cut short, or one tar could not finish, is rejected.
max_list=4000000
set +o pipefail
timeout 120 tar --numeric-owner -tvzf "$tmp/$newest" 2>/dev/null | head -c "$max_list" > "$tmp/list"
rc=${PIPESTATUS[0]}
set -o pipefail
[ "$rc" = 0 ] && [ "$(stat -c %s "$tmp/list")" -lt "$max_list" ] || skip "$newest could not be listed"
# (Plain POSIX patterns: mawk has no {m,n}.)
awk -v max_n="$max_entries" -v max_b="$max_unpacked" \
  -v sym='^[.]/[A-Z][A-Z]?[A-Z]?[A-Z]?[A-Z]?[.]json$' -v cls='^[.]/[A-Z][A-Z]?[A-Z]?[A-Z]?[A-Z]?[.][A-Z][.]json$' '
  { n++ }
  substr($1, 1, 1) == "d" && $NF == "./" && NF == 6 { next }
  substr($1, 1, 1) == "-" && NF == 6 && ($NF == "./snapshot.json" || $NF ~ sym || $NF ~ cls) { b += $3; next }
  { bad++ }
  END { exit (bad > 0 || n > max_n || b > max_b) ? 1 : 0 }
' "$tmp/list" || skip "$newest holds unexpected entries or is too big"
mkdir -p "$dir"
timeout 120 tar -C "$dir" -xzf "$tmp/$newest" --no-same-owner --no-same-permissions || { rm -rf "$dir"; skip "$newest could not be unpacked"; }
echo "$newest: $(ls "$dir" | wc -l) files"
