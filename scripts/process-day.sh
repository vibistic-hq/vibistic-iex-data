#!/usr/bin/env bash
# Downloads one day's IEX HIST TOPS 1.6 file, streams it through iexagg and
# writes DIR/YYYYMMDD.jsonl.gz. Exit 3: IEX has no file for that day (not a
# trading day, or not published yet). Exit 4: IEX's file does not cover the
# whole regular session (as on 2024-12-23), so the day is left out.
#
#   scripts/process-day.sh YYYYMMDD DIR
set -euo pipefail
d="${1:?date YYYYMMDD}"
out="${2:?output dir}"
[[ "$d" =~ ^20[0-9]{6}$ ]] || { echo "bad date: $d" >&2; exit 2; }
bin="$(dirname "$0")/../bin/iexagg"

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

tmp="$out/$d.jsonl.gz.part"
# A cut-off download fails gzip's own length and CRC check at the end, and
# pipefail turns that into a failure here.
rc=0
curl -sf --retry 3 "$link" | gzip -dc | "$bin" "${d:0:4}-${d:4:2}-${d:6:2}" | gzip -9 > "$tmp" || rc=$?
if [ "$rc" != 0 ]; then
  rm -f "$tmp"
  # pipefail reports the last stage that failed: 4 only when iexagg said so.
  [ "$rc" = 4 ] && echo "IEX's file for $d does not cover the regular session; day left out" >&2
  exit "$rc"
fi
mv "$tmp" "$out/$d.jsonl.gz"
