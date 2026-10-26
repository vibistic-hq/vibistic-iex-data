#!/usr/bin/env bash
# Tests scripts/fetch-archive.sh with a stand-in gh (issue #10): a month's
# release with no day file is skipped with a warning instead of failing the
# whole fetch, and the others are still downloaded.
set -euo pipefail
fetch="$(cd "$(dirname "$0")" && pwd)/fetch-archive.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin" "$tmp/releases"
# A fixed clock on a 31st, the case '49 months ago' got wrong: from
# 2026-10-31 it gave 2022-10, skipping 2022-09.
real_date=$(command -v date)
cat > "$tmp/bin/date" <<'DATE'
#!/usr/bin/env bash
# Now is 2026-10-31: no -d, or a relative one ("49 months ago"), is read from
# there; an absolute -d date is passed through.
now=2026-10-31T12:00:00Z
args=()
dated=false
while [ $# -gt 0 ]; do
  if [ "$1" = "-d" ]; then
    dated=true
    if [[ "$2" =~ ^[0-9]{4}- ]]; then args+=(-d "$2"); else args+=(-d "$now $2"); fi
    shift 2
  else
    args+=("$1")
    shift
  fi
done
$dated || args=(-d "$now" "${args[@]}")
exec "$REAL_DATE" "${args[@]}"
DATE
export REAL_DATE="$real_date"
chmod +x "$tmp/bin/date"
this_month=2026-10
mkdir -p "$tmp/releases/days-$this_month" "$tmp/releases/days-2022-09" "$tmp/releases/days-2022-08" "$tmp/releases/days-empty-$this_month"
echo '{}' > "$tmp/releases/days-$this_month/20261030.jsonl.gz"
echo '{}' > "$tmp/releases/days-2022-09/20220930.jsonl.gz"
echo '{}' > "$tmp/releases/days-2022-08/20220831.jsonl.gz"
cat > "$tmp/bin/gh" <<'GH'
#!/usr/bin/env bash
# gh release list --limit N --json tagName --jq ... | gh release view TAG --json assets --jq ...
# | gh release download TAG -D DIR -p PATTERN --clobber
case "$1 $2" in
  "release list") ls "$RELEASES" | sed 's/^days-empty-/days-/' | sort -u ;;
  "release view")
    dir="$RELEASES/$3"; [ -d "$RELEASES/days-empty-${3#days-}" ] && [ "$3" = "days-$EMPTY_MONTH" ] && dir="$RELEASES/days-empty-${3#days-}"
    ls "$dir" | grep -c '^20.*\.jsonl\.gz$' || true ;;
  "release download")
    [ "$3" = "days-$EMPTY_MONTH" ] && { echo "no assets match the file pattern" >&2; exit 1; }
    mkdir -p "$5"
    cp "$RELEASES/$3"/*.jsonl.gz "$5/" ;;
esac
[ -f "$RELEASES/.list-fails" ] && [ "$1 $2" = "release list" ] && exit 1
exit 0
GH
chmod +x "$tmp/bin/gh"
fail() { echo "FAIL: $*" >&2; exit 1; }

# 49 months back from 2026-10 is 2022-09 (kept); 2022-08 is 50 (left out).
export PATH="$tmp/bin:$PATH" RELEASES="$tmp/releases" EMPTY_MONTH=none
bash "$fetch" "$tmp/out1" >/dev/null 2>&1 || fail "fetch failed"
[ "$(ls "$tmp/out1" | tr '\n' ' ')" = "20220930.jsonl.gz 20261030.jsonl.gz " ] || fail "wrong files: $(ls "$tmp/out1" | tr '\n' ' ')"

# This month's release is empty: skipped with a warning, the fetch still succeeds.
export EMPTY_MONTH="$this_month"
err=$(bash "$fetch" "$tmp/out2" 2>&1 >/dev/null) || fail "fetch failed on an empty release: $err"
echo "$err" | grep -q "has no day files" || fail "no warning for the empty release: $err"
[ "$(ls "$tmp/out2")" = "20220930.jsonl.gz" ] || fail "wrong files with an empty release: $(ls "$tmp/out2" | tr '\n' ' ')"

# Security audit run-1: a failed listing fails the fetch; a file filed under the
# wrong month, or present in two releases, fails it too.
export EMPTY_MONTH=none
touch "$tmp/releases/.list-fails"
bash "$fetch" "$tmp/out3" >/dev/null 2>&1 && fail "a failed release listing passed"
rm "$tmp/releases/.list-fails"
echo '{}' > "$tmp/releases/days-$this_month/20220930.jsonl.gz"
err=$(bash "$fetch" "$tmp/out4" 2>&1 >/dev/null) && fail "a misfiled day file passed"
echo "$err" | grep -q "not a day of that month" || fail "no misfiled message: $err"
rm "$tmp/releases/days-$this_month/20220930.jsonl.gz"

echo "ok"
