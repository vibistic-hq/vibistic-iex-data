#!/usr/bin/env bash
# Tests scripts/upload-earnings.sh with a stand-in gh: the file goes up gzipped
# under today's name, the release is created once, and only the newest 14
# snapshots are kept (other assets untouched).
set -euo pipefail
upload="$(cd "$(dirname "$0")" && pwd)/upload-earnings.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin" "$tmp/assets" "$tmp/work"
cat > "$tmp/bin/gh" <<'GH'
#!/usr/bin/env bash
echo "$*" >> "$LOG"
case "$1 $2" in
  "release view")
    [ -e "$ASSETS/.release" ] || exit 1
    if [ "${3:-}" = earnings ] && [ "${4:-}" = --json ]; then for f in "$ASSETS"/*; do [ -e "$f" ] && basename "$f"; done; fi ;;
  "release create") touch "$ASSETS/.release" ;;
  "release upload") cp "$4" "$ASSETS/" ;;
  "release delete-asset") rm "$ASSETS/$4" ;;
esac
GH
chmod +x "$tmp/bin/gh"
export PATH="$tmp/bin:$PATH" ASSETS="$tmp/assets" LOG="$tmp/log"
fail() { echo "FAIL: $*" >&2; exit 1; }

echo '{"v":1}' > "$tmp/e.json"
(cd "$tmp/work" && bash "$upload" "$tmp/e.json") >/dev/null
name="earnings-$(date -u +%Y%m%d).json.gz"
[ "$(gzip -dc "$ASSETS/$name")" = '{"v":1}' ] || fail "not uploaded as $name"
[ "$(grep -c 'release create' "$LOG")" = 1 ] || fail "release not created once"

# 20 older snapshots and another asset: the 14 newest snapshots stay.
for i in $(seq -w 1 20); do touch "$ASSETS/earnings-202601$i.json.gz"; done
touch "$ASSETS/notes.txt"
(cd "$tmp/work" && bash "$upload" "$tmp/e.json") >/dev/null
[ "$(grep -c 'release create' "$LOG")" = 1 ] || fail "release created again"
n=$(ls "$ASSETS" | grep -cE '^earnings-[0-9]{8}\.json\.gz$')
[ "$n" = 14 ] || fail "$n snapshots kept"
[ -e "$ASSETS/$name" ] || fail "today's removed"
[ -e "$ASSETS/notes.txt" ] || fail "another asset removed"
[ ! -e "$ASSETS/earnings-20260101.json.gz" ] || fail "oldest kept"

# A missing or empty file uploads nothing.
: > "$tmp/empty.json"
if (cd "$tmp/work" && bash "$upload" "$tmp/empty.json") >/dev/null 2>&1; then fail "uploaded an empty file"; fi
echo "upload-earnings.sh: ok"
