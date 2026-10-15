#!/usr/bin/env bash
# Downloads one day's IEX HIST TOPS 1.6 file, streams it through iexagg and
# writes DIR/YYYYMMDD.jsonl.gz. Exit 3: IEX has no file for that day (not a
# trading day, or not published yet). Exit 4: IEX's file does not cover the
# whole regular session (as on 2024-12-23), so the day is left out.
#
# With IEX_INCOMPLETE set to a file, an incomplete file's date and generation
# (from IEX's download link, new whenever IEX replaces the file) are added to
# it, and exit 5 skips that same file on later runs without downloading it
# again (about 9 GB). A replaced file has a new generation and is read again.
#
#   scripts/process-day.sh YYYYMMDD DIR
set -euo pipefail
d="${1:?date YYYYMMDD}"
out="${2:?output dir}"
[[ "$d" =~ ^20[0-9]{6}$ ]] || { echo "bad date: $d" >&2; exit 2; }
bin="${IEXAGG:-$(dirname "$0")/../bin/iexagg}"

index="$(mktemp)"
code="$(curl -s --retry 3 -o "$index" -w '%{http_code}' "https://iextrading.com/api/1.0/hist?date=$d")"
if [ "$code" = 404 ]; then
  echo "no IEX HIST file for $d" >&2
  exit 3
fi
[ "$code" = 200 ] || { echo "HIST index answered $code for $d" >&2; exit 1; }
link="$(jq -r '[.[] | select(.feed == "TOPS" and .version == "1.6")][0].link // empty' "$index")"
rm -f "$index"
[ -n "$link" ] || { echo "no TOPS 1.6 file listed for $d" >&2; exit 3; }
# Only IEX's own storage bucket, never wherever a changed index might point.
case "$link" in
  https://www.googleapis.com/download/storage/v1/b/iex/o/*) ;;
  *) echo "unexpected download host for $d" >&2; exit 1 ;;
esac
gen="$(sed -n 's/.*[?&]generation=\([0-9]\{1,20\}\)\(&.*\)\{0,1\}$/\1/p' <<<"$link")"
if [ -n "${IEX_INCOMPLETE:-}" ] && [ -n "$gen" ] && [ -f "$IEX_INCOMPLETE" ] && grep -qx "$d $gen" "$IEX_INCOMPLETE"; then
  echo "IEX's file for $d is still the incomplete one already read; skipped" >&2
  exit 5
fi

tmp="$out/$d.jsonl.gz.part"
# A cut-off download fails gzip's own length and CRC check at the end. Each
# stage's status is checked: a broken download or decompression is a plain
# failure (exit 1), and only a whole file that iexagg finds incomplete is 4.
set +e
curl -sf --retry 3 "$link" | gzip -dc | "$bin" "${d:0:4}-${d:4:2}-${d:6:2}" | gzip -9 > "$tmp"
st=("${PIPESTATUS[@]}")
set -e
rc=0
if [ "${st[0]}" != 0 ] || [ "${st[1]}" != 0 ]; then
  echo "download of IEX's file for $d failed (curl ${st[0]}, gzip ${st[1]})" >&2
  rc=1
elif [ "${st[2]}" != 0 ]; then
  rc=${st[2]}
elif [ "${st[3]}" != 0 ]; then
  rc=1
fi
if [ "$rc" != 0 ]; then
  rm -f "$tmp"
  if [ "$rc" = 4 ]; then
    echo "IEX's file for $d does not cover the regular session; day left out" >&2
    # Without a generation the file cannot be told apart from a replacement, so
    # it is not recorded and later runs read it again.
    if [ -n "${IEX_INCOMPLETE:-}" ] && [ -n "$gen" ]; then echo "$d $gen" >> "$IEX_INCOMPLETE"; fi
  fi
  exit "$rc"
fi
mv "$tmp" "$out/$d.jsonl.gz"
