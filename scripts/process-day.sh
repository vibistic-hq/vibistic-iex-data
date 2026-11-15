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
# When IEX's index refuses (403, since 2026-11-14), the file is found by its
# fixed name in IEX's public bucket instead (issue #25).
#
#   scripts/process-day.sh YYYYMMDD DIR
set -euo pipefail
d="${1:?date YYYYMMDD}"
out="${2:?output dir}"
[[ "$d" =~ ^20[0-9]{6}$ ]] || { echo "bad date: $d" >&2; exit 2; }
bin="${IEXAGG:-$(dirname "$0")/../bin/iexagg}"

index="$(mktemp)"
# The index for one day is a few hundred bytes: bounded in size and time.
# A transport failure becomes a plain failure (exit 1), never curl's own exit
# code: 3, 4 and 5 mean "no file", "incomplete" and "skip" to the workflows.
code="$(curl -s --retry 3 --max-time 60 --max-filesize 1000000 -o "$index" -w '%{http_code}' "https://iextrading.com/api/1.0/hist?date=$d")" || code="curl-failed"
if [ "$code" = 404 ]; then
  echo "no IEX HIST file for $d" >&2
  exit 3
fi
if [ "$code" = 403 ]; then
  # Since 2026-11-14 IEX refuses the index ("not currently available for direct
  # download") while still putting each day's file in its public bucket under a
  # fixed name (issue #25). Read that object's metadata instead: 404 means no
  # file that day, as the index's 404 did, and its generation makes the link
  # name this exact file, so the incomplete-file record below still works. The
  # index is used again whenever it answers.
  rm -f "$index"
  object_of() { printf 'data%%2Ffeeds%%2F%s%%2F%s_IEXTP1_TOPS1.6.pcap.gz' "$1" "$1"; }
  meta_code() { # meta_code DAY FILE: the bucket's answer for DAY's object, body in FILE
    curl -s --retry 3 --max-time 60 --max-filesize 100000 -o "$2" -w '%{http_code}' "https://www.googleapis.com/storage/v1/b/iex/o/$(object_of "$1")"
  }
  object="$(object_of "$d")"
  meta="$(mktemp)"
  # A transport failure must not leak curl's own exit code: 3, 4 and 5 mean
  # something else to the workflows. It becomes a plain failure below.
  code="$(meta_code "$d" "$meta")" || code="curl-failed"
  # A missing object answers 401 to anonymous callers (they may not list the
  # bucket), so 401 or 404 is "no file that day" (today before IEX posts it, a
  # holiday). That also is what a bucket closed to us would answer, so it counts
  # only while one of the 10 days before is readable; otherwise it is a failure.
  if [ "$code" = 404 ] || [ "$code" = 401 ]; then
    readable=""
    for back in 1 2 3 4 5 6 7 8 9 10; do
      prev="$(date -u -d "${d:0:4}-${d:4:2}-${d:6:2} -$back day" +%Y%m%d)"
      [ "$(meta_code "$prev" "$meta")" = 200 ] && { readable="$prev"; break; }
    done
    rm -f "$meta"
    [ -n "$readable" ] || { echo "IEX's bucket has no readable file for $d or the 10 days before: closed?" >&2; exit 1; }
    echo "no IEX HIST file for $d (index refused, none in IEX's bucket)" >&2
    exit 3
  fi
  [ "$code" = 200 ] || { rm -f "$meta"; echo "HIST index refused and IEX's bucket answered $code for $d" >&2; exit 1; }
  gen="$(jq -r '.generation // empty' "$meta" 2>/dev/null || true)"
  rm -f "$meta"
  [[ "$gen" =~ ^[0-9]{1,20}$ ]] || { echo "IEX's bucket gave no usable generation for $d" >&2; exit 1; }
  echo "HIST index refused; reading $d from IEX's bucket" >&2
  link="https://www.googleapis.com/download/storage/v1/b/iex/o/$object?generation=$gen&alt=media"
else
  [ "$code" = 200 ] || { echo "HIST index answered $code for $d" >&2; exit 1; }
  link="$(jq -r '[.[] | select(.feed == "TOPS" and .version == "1.6")][0].link // empty' "$index")"
  rm -f "$index"
  [ -n "$link" ] || { echo "no TOPS 1.6 file listed for $d" >&2; exit 3; }
fi
# Only IEX's own storage bucket, never wherever a changed index might point. The
# object name is one percent-encoded path segment (data%2Ffeeds%2F...), so the link
# must have no further '/', no '..', no encoded dot or backslash, and only a plain
# query. A prefix match alone let '../../other-bucket' through, which curl then
# resolved to another bucket (security audit run-1); curl also gets --path-as-is
# below, so the path requested is exactly the path checked.
object_re='^https://www\.googleapis\.com/download/storage/v1/b/iex/o/[A-Za-z0-9._%-]+(\?[A-Za-z0-9=&._-]*)?$'
if ! [[ "$link" =~ $object_re ]] || [[ "$link" == *..* ]] || [[ "${link,,}" == *%2e* ]] || [[ "${link,,}" == *%5c* ]]; then
  echo "unexpected download link for $d" >&2
  exit 1
fi
gen="$(sed -n 's/.*[?&]generation=\([0-9]\{1,20\}\)\(&.*\)\{0,1\}$/\1/p' <<<"$link")"
if [ -n "${IEX_INCOMPLETE:-}" ] && [ -n "$gen" ] && [ -f "$IEX_INCOMPLETE" ] && grep -qx "$d $gen" "$IEX_INCOMPLETE"; then
  echo "IEX's file for $d is still the incomplete one already read; skipped" >&2
  exit 5
fi

tmp="$out/$d.jsonl.gz.part"
# A cut-off download fails gzip's own length and CRC check at the end. Each
# stage's status is checked: a broken download, decompression or output is a
# plain failure (exit 1), and only a whole file that iexagg finds incomplete is 4.
set +e
curl -sf --path-as-is --retry 3 "$link" | gzip -dc | "$bin" "${d:0:4}-${d:4:2}-${d:6:2}" | gzip -9 > "$tmp"
st=("${PIPESTATUS[@]}")
set -e
rc=0
if [ "${st[0]}" != 0 ] || [ "${st[1]}" != 0 ] || [ "${st[3]}" != 0 ]; then
  echo "reading or writing IEX's file for $d failed (curl ${st[0]}, gunzip ${st[1]}, gzip ${st[3]})" >&2
  rc=1
elif [ "${st[2]}" != 0 ]; then
  rc=${st[2]}
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
