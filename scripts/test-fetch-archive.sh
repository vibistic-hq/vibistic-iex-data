#!/usr/bin/env bash
# Tests scripts/fetch-archive.sh with a stand-in gh (issue #10): a month's
# release with no day file is skipped with a warning instead of failing the
# whole fetch, and the others are still downloaded.
set -euo pipefail
fetch="$(cd "$(dirname "$0")" && pwd)/fetch-archive.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin" "$tmp/releases"
this_month=$(date -u +%Y-%m)
mkdir -p "$tmp/releases/days-$this_month" "$tmp/releases/days-2001-01" "$tmp/releases/days-empty-$this_month"
echo '{}' > "$tmp/releases/days-$this_month/20990101.jsonl.gz"
echo '{}' > "$tmp/releases/days-2001-01/20010102.jsonl.gz"
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
    cp "$RELEASES/$3"/*.jsonl.gz "$5/" ;;
esac
GH
chmod +x "$tmp/bin/gh"
fail() { echo "FAIL: $*" >&2; exit 1; }

# This month's release has a file; one old release is outside the 49 months.
export PATH="$tmp/bin:$PATH" RELEASES="$tmp/releases" EMPTY_MONTH=none
bash "$fetch" "$tmp/out1" >/dev/null 2>&1 || fail "fetch failed"
[ "$(ls "$tmp/out1")" = "20990101.jsonl.gz" ] || fail "wrong files: $(ls "$tmp/out1")"

# This month's release is empty: skipped with a warning, the fetch still succeeds.
export EMPTY_MONTH="$this_month"
err=$(bash "$fetch" "$tmp/out2" 2>&1 >/dev/null) || fail "fetch failed on an empty release: $err"
echo "$err" | grep -q "has no day files" || fail "no warning for the empty release: $err"
[ -z "$(ls "$tmp/out2")" ] || fail "downloaded from an empty release"

echo "ok"
