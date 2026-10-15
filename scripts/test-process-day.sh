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
# or with $dir/dl-mode set, a corrupt one (badgzip) or a failed transfer (fail).
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
  gen=$(cat "$FAKE/gen")
  printf '[{"link":"https://www.googleapis.com/download/storage/v1/b/iex/o/data%%2Ff.pcap.gz%s","feed":"TOPS","version":"1.6"}]' "$gen" > "$out"
  printf 200
else
  echo download >> "$FAKE/downloads"
  case "$(cat "$FAKE/dl-mode" 2>/dev/null)" in
    badgzip) printf 'pcap' | gzip -c | head -c 12 ;;
    fail) printf 'pcap' | gzip -c | head -c 12; exit 18 ;;
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
chmod +x "$dir/bin/curl" "$dir/bin/iexagg"
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
for mode in badgzip fail; do
  echo "$mode" > "$dir/dl-mode"
  run 1 IEX_INCOMPLETE="$list"
  [ ! -s "$list" ] || { echo "FAIL: recorded a broken download ($mode)" >&2; exit 1; }
done
rm -f "$dir/dl-mode"
# The same generation, downloaded whole this time, is read normally.
run 4 IEX_INCOMPLETE="$list"
[ "$(cat "$list")" = "20010102 444" ] || { echo "FAIL: list holds: $(cat "$list")" >&2; exit 1; }

echo "process-day.sh: ok"
