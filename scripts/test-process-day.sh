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
out=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift ;;
  esac
  shift
done
if [ -n "$out" ]; then
  case "$(cat "$FAKE/index" 2>/dev/null)" in
    404) printf 404; exit 0 ;;
    nofeed) printf '[{"link":"x","feed":"DEEP","version":"1.0"}]' > "$out"; printf 200; exit 0 ;;
  esac
  gen=$(cat "$FAKE/gen")
  printf '[{"link":"https://www.googleapis.com/download/storage/v1/b/iex/o/data%%2Ff.pcap.gz%s","feed":"TOPS","version":"1.6"}]' "$gen" > "$out"
  printf 200
else
  echo download >> "$FAKE/downloads"
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

echo "process-day.sh: ok"
