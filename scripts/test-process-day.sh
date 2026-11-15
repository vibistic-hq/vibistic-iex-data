#!/usr/bin/env bash
# Tests how scripts/process-day.sh treats an incomplete IEX file, with stand-ins
# for curl (IEX's index and download) and iexagg, so nothing is fetched.
set -euo pipefail
script="$(cd "$(dirname "$0")" && pwd)/process-day.sh"
dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT
mkdir -p "$dir/bin" "$dir/out"

# curl: with -o, writes the index (generation from $dir/gen) and prints 200;
# otherwise it is the download, logged to $dir/downloads: a tiny gzip stream,
# or with $dir/dl-mode set, a corrupt one (badgzip), a cut-off transfer (fail)
# or a whole stream that curl still reports as failed (curlfail). With
# $dir/index set to 404 or nofeed, the index has no file for the day.
cat > "$dir/bin/curl" <<'EOF'
#!/usr/bin/env bash
args="$*"
out=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift ;;
  esac
  shift
done
if [ -n "$out" ] && [[ "$args" == *storage/v1/b/iex/o/* ]]; then
  # The bucket's object metadata (asked only once the index refuses), logged to
  # $dir/meta-urls. For the day processed (20010102), $dir/meta is 401, 404,
  # 500, nogen, badgen, or else a generation; any other (earlier) day answers
  # $dir/meta-prev, a generation by default.
  url="${args##* }"
  printf '%s\n' "$url" >> "$FAKE/meta-urls"
  answer="$(cat "$FAKE/meta")"
  [[ "$url" == *20010102* ]] || answer="$(cat "$FAKE/meta-prev" 2>/dev/null || echo 1)"
  case "$answer" in
    curlexit*) exit "${answer#curlexit}" ;;
    401) printf 401 ;;
    404) printf 404 ;;
    500) printf 500 ;;
    nogen) printf '{"name":"x"}' > "$out"; printf 200 ;;
    badgen) printf '{"generation":"12x4"}' > "$out"; printf 200 ;;
    *) jq -n --arg g "$answer" '{generation: $g, size: "9"}' > "$out"; printf 200 ;;
  esac
  exit 0
fi
if [ -n "$out" ]; then
  case "$(cat "$FAKE/index" 2>/dev/null)" in
    404) printf 404; exit 0 ;;
    403) printf 403; exit 0 ;;
    curlexit*) i="$(cat "$FAKE/index")"; exit "${i#curlexit}" ;;
    500) printf 500; exit 0 ;;
    nofeed) printf '[{"link":"x","feed":"DEEP","version":"1.0"}]' > "$out"; printf 200; exit 0 ;;
  esac
  gen=$(cat "$FAKE/gen")
  link="https://www.googleapis.com/download/storage/v1/b/iex/o/data%2Ff.pcap.gz$gen"
  [ -f "$FAKE/link" ] && link=$(cat "$FAKE/link")
  jq -n --arg link "$link" '[{link: $link, feed: "TOPS", version: "1.6"}]' > "$out"
  printf 200
else
  echo download >> "$FAKE/downloads"
  printf '%s\n' "$args" >> "$FAKE/download-args"
  case "$(cat "$FAKE/dl-mode" 2>/dev/null)" in
    badgzip) printf 'pcap' | gzip -c | head -c 12 ;;
    fail) printf 'pcap' | gzip -c | head -c 12; exit 18 ;;
    curlfail) printf 'pcap' | gzip -c; exit 18 ;;
    *) printf 'pcap' | gzip -c ;;
  esac
fi
EOF
# iexagg: reads its input, then exits with $dir/agg-rc (4: incomplete).
cat > "$dir/bin/iexagg" <<'EOF'
#!/usr/bin/env bash
cat > /dev/null
rc=$(cat "$FAKE/agg-rc")
[ "$rc" = 0 ] && echo '{}'
exit "$rc"
EOF
# gzip: the real one, except that compressing (-9) fails with $dir/gzip-out-fail.
real_gzip=$(command -v gzip)
printf '#!/usr/bin/env bash\nif [ "$1" = -9 ] && [ -f "%s/gzip-out-fail" ]; then cat > /dev/null; exit 1; fi\nexec "%s" "$@"\n' "$dir" "$real_gzip" > "$dir/bin/gzip"
chmod +x "$dir/bin/curl" "$dir/bin/iexagg" "$dir/bin/gzip"
export FAKE="$dir" IEXAGG="$dir/bin/iexagg" PATH="$dir/bin:$PATH"
list="$dir/incomplete.txt"

run() { # run want-rc [env...]: process-day.sh for 20010102, checks the exit code
  local want=$1 rc=0; shift
  env "$@" bash "$script" 20010102 "$dir/out" 2>/dev/null || rc=$?
  [ "$rc" = "$want" ] || { echo "FAIL: want exit $want, got $rc" >&2; exit 1; }
}
downloads() { [ -f "$dir/downloads" ] && wc -l < "$dir/downloads" | tr -d ' ' || echo 0; }
want_downloads() { [ "$(downloads)" = "$1" ] || { echo "FAIL: want $1 downloads, got $(downloads)" >&2; exit 1; }; }

echo '?generation=111&alt=media' > "$dir/gen"
echo 4 > "$dir/agg-rc"

# Incomplete: left out, its generation recorded.
run 4 IEX_INCOMPLETE="$list"
want_downloads 1
[ "$(cat "$list")" = "20010102 111" ] || { echo "FAIL: list holds: $(cat "$list")" >&2; exit 1; }
# The same file again: skipped without a download.
run 5 IEX_INCOMPLETE="$list"
want_downloads 1
# IEX replaced the file (new generation), now whole: read and written.
echo '?generation=222&alt=media' > "$dir/gen"
echo 0 > "$dir/agg-rc"
run 0 IEX_INCOMPLETE="$list"
want_downloads 2
[ -s "$dir/out/20010102.jsonl.gz" ] || { echo "FAIL: no output written" >&2; exit 1; }
rm -f "$dir/out/20010102.jsonl.gz"

# Without IEX_INCOMPLETE, an incomplete file is read every time, as before.
echo '?generation=333&alt=media' > "$dir/gen"
echo 4 > "$dir/agg-rc"
run 4
run 4
want_downloads 4

# A link without a generation is never recorded, so it is never skipped.
echo '?alt=media' > "$dir/gen"
: > "$list"
run 4 IEX_INCOMPLETE="$list"
run 4 IEX_INCOMPLETE="$list"
want_downloads 6
[ ! -s "$list" ] || { echo "FAIL: recorded a file without a generation" >&2; exit 1; }

# A generation that only looks like one (not all digits) is not trusted either.
echo '?generation=12x4&alt=media' > "$dir/gen"
run 4 IEX_INCOMPLETE="$list"
[ ! -s "$list" ] || { echo "FAIL: recorded a malformed generation" >&2; exit 1; }

# A broken download is a plain failure (exit 1), never recorded as incomplete,
# even when iexagg, fed a cut-off stream, says the session is not covered.
echo '?generation=444&alt=media' > "$dir/gen"
: > "$list"
for mode in badgzip fail curlfail; do
  echo "$mode" > "$dir/dl-mode"
  run 1 IEX_INCOMPLETE="$list"
  [ ! -s "$list" ] || { echo "FAIL: recorded a broken download ($mode)" >&2; exit 1; }
done
rm -f "$dir/dl-mode"
# The same generation, downloaded whole this time, is read normally.
run 4 IEX_INCOMPLETE="$list"
[ "$(cat "$list")" = "20010102 444" ] || { echo "FAIL: list holds: $(cat "$list")" >&2; exit 1; }

# Writing the output fails while iexagg says incomplete: exit 1, not recorded.
: > "$list"
touch "$dir/gzip-out-fail"
run 1 IEX_INCOMPLETE="$list"
[ ! -s "$list" ] || { echo "FAIL: recorded a file whose output failed" >&2; exit 1; }
rm -f "$dir/gzip-out-fail"
# iexagg failing for another reason is passed on, not recorded.
echo 1 > "$dir/agg-rc"
run 1 IEX_INCOMPLETE="$list"
[ ! -s "$list" ] || { echo "FAIL: recorded an iexagg failure" >&2; exit 1; }
# No file for the day: exit 3, nothing downloaded.
n=$(downloads)
for index in 404 nofeed; do
  echo "$index" > "$dir/index"
  run 3 IEX_INCOMPLETE="$list"
done
rm -f "$dir/index"
want_downloads "$n"

# The download link must stay inside IEX's bucket (security audit run-1): dot
# segments, encoded dots, backslashes, extra path segments and other hosts are
# refused before anything is downloaded.
echo '?generation=777&alt=media' > "$dir/gen"
n=$(downloads)
for bad in \
  'https://www.googleapis.com/download/storage/v1/b/iex/o/../../attacker/o/day.pcap.gz?generation=1&alt=media' \
  'https://www.googleapis.com/download/storage/v1/b/iex/o/%2e%2e/%2E%2E/attacker/o/day.pcap.gz' \
  'https://www.googleapis.com/download/storage/v1/b/iex/o/data%5c..%5cday.pcap.gz' \
  'https://www.googleapis.com/download/storage/v1/b/iex/o/data/day.pcap.gz' \
  'https://www.googleapis.com/download/storage/v1/b/iex/o/data%2Fday.pcap.gz#frag' \
  'https://www.googleapis.com.evil.example/download/storage/v1/b/iex/o/data%2Fday.pcap.gz' \
  'http://www.googleapis.com/download/storage/v1/b/iex/o/data%2Fday.pcap.gz'; do
  printf '%s' "$bad" > "$dir/link"
  run 1
done
rm -f "$dir/link"
want_downloads "$n"
# A real-shaped link is downloaded with --path-as-is, so the path curl sends is
# the one checked.
: > "$dir/download-args"
echo 0 > "$dir/agg-rc"
run 0
grep -q -- '--path-as-is' "$dir/download-args" || { echo "FAIL: download without --path-as-is" >&2; exit 1; }
[ ! -f "$dir/meta-urls" ] || { echo "FAIL: asked the bucket while the index answered" >&2; exit 1; }

# Issue #25: the index refuses (403). The file is found by its fixed name in
# IEX's bucket and that exact generation is downloaded.
rm -f "$dir/out/20010102.jsonl.gz"
echo 403 > "$dir/index"
echo 555 > "$dir/meta"
: > "$dir/download-args"
: > "$list"
echo 0 > "$dir/agg-rc"
run 0 IEX_INCOMPLETE="$list"
[ -s "$dir/out/20010102.jsonl.gz" ] || { echo "FAIL: no output from the bucket fallback" >&2; exit 1; }
[ "$(tail -1 "$dir/meta-urls")" = "https://www.googleapis.com/storage/v1/b/iex/o/data%2Ffeeds%2F20010102%2F20010102_IEXTP1_TOPS1.6.pcap.gz" ] \
  || { echo "FAIL: metadata asked at $(tail -1 "$dir/meta-urls")" >&2; exit 1; }
grep -qF 'https://www.googleapis.com/download/storage/v1/b/iex/o/data%2Ffeeds%2F20010102%2F20010102_IEXTP1_TOPS1.6.pcap.gz?generation=555&alt=media' "$dir/download-args" \
  || { echo "FAIL: fallback downloaded $(cat "$dir/download-args")" >&2; exit 1; }
grep -q -- '--path-as-is' "$dir/download-args" || { echo "FAIL: fallback download without --path-as-is" >&2; exit 1; }
# An incomplete file found this way is recorded by its generation and then skipped.
rm -f "$dir/out/20010102.jsonl.gz"
echo 4 > "$dir/agg-rc"
n=$(downloads)
run 4 IEX_INCOMPLETE="$list"
[ "$(cat "$list")" = "20010102 555" ] || { echo "FAIL: list holds: $(cat "$list")" >&2; exit 1; }
run 5 IEX_INCOMPLETE="$list"
want_downloads $((n + 1))
# No object in the bucket (401 to anonymous callers, or 404) while an earlier
# day's is readable: no file that day (exit 3), nothing downloaded.
n=$(downloads)
for meta in 401 404; do
  echo "$meta" > "$dir/meta"
  : > "$dir/meta-urls"
  run 3 IEX_INCOMPLETE="$list"
  grep -qF '%2F20010101_IEXTP1_TOPS1.6.pcap.gz' "$dir/meta-urls" || { echo "FAIL: did not check the day before" >&2; exit 1; }
done
# The day before and 9 more unreadable too (a holiday week still finds one in
# 10 days): the bucket looks closed, a failure, not a quiet "no file".
: > "$dir/meta-urls"
echo 401 > "$dir/meta"
echo 401 > "$dir/meta-prev"
run 1 IEX_INCOMPLETE="$list"
[ "$(wc -l < "$dir/meta-urls" | tr -d ' ')" = 11 ] || { echo "FAIL: want 11 bucket checks, got $(wc -l < "$dir/meta-urls")" >&2; exit 1; }
grep -qF '%2F20001223_IEXTP1_TOPS1.6.pcap.gz' "$dir/meta-urls" || { echo "FAIL: did not look back 10 days" >&2; exit 1; }
rm -f "$dir/meta-prev"
# The bucket failing, or metadata without a usable generation: a plain failure.
for meta in 500 nogen badgen; do
  echo "$meta" > "$dir/meta"
  run 1 IEX_INCOMPLETE="$list"
done
want_downloads "$n"
# curl itself failing on the metadata (exit 3, 4 or 5: codes the workflows read
# as no file, incomplete, skip) is a plain failure, never passed on.
echo 403 > "$dir/index"
for c in 3 4 5 6; do
  echo "curlexit$c" > "$dir/meta"
  run 1 IEX_INCOMPLETE="$list"
done
# A transport failure on an earlier day only means that day is not proof the
# bucket is readable; the next earlier day is tried.
echo 401 > "$dir/meta"
echo curlexit5 > "$dir/meta-prev"
run 1 IEX_INCOMPLETE="$list"
rm -f "$dir/meta-prev"
want_downloads "$n"
# The same for the index request itself.
for c in 3 4 5 6; do
  echo "curlexit$c" > "$dir/index"
  run 1 IEX_INCOMPLETE="$list"
done
want_downloads "$n"
# Any other index answer is still a plain failure, and the bucket is not asked.
echo 500 > "$dir/index"
: > "$dir/meta-urls"
run 1
[ ! -s "$dir/meta-urls" ] || { echo "FAIL: asked the bucket after an index 500" >&2; exit 1; }
want_downloads "$n"
rm -f "$dir/index" "$dir/meta"

echo "process-day.sh: ok"
