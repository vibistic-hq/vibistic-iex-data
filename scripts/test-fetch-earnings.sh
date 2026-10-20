#!/usr/bin/env bash
# Tests scripts/fetch-earnings.sh with a stand-in gh: the newest file is
# unpacked; a missing, oversized or broken one is not.
set -euo pipefail
fetch="$(cd "$(dirname "$0")" && pwd)/fetch-earnings.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin" "$tmp/assets"
cat > "$tmp/bin/gh" <<'GH'
#!/usr/bin/env bash
# gh release view earnings --json assets --jq ... | gh release download earnings -p NAME -D DIR
case "$1 $2" in
  "release view") for f in "$ASSETS"/*; do [ -e "$f" ] && echo "$(basename "$f") $(stat -c %s "$f")"; done ;;
  "release download") [ -z "${FAIL_DOWNLOAD:-}" ] || exit 1; cp "$ASSETS/$5" "$7/" ;;
esac
GH
chmod +x "$tmp/bin/gh"
export PATH="$tmp/bin:$PATH" ASSETS="$tmp/assets"
fail() { echo "FAIL: $*" >&2; exit 1; }

# None at all: nothing, and no error.
bash "$fetch" "$tmp/e0.json" >/dev/null
[ ! -e "$tmp/e0.json" ] || fail "unpacked with no asset"

echo '{"old":1}' | gzip > "$ASSETS/earnings-20261016.json.gz"
echo '{"new":1}' | gzip > "$ASSETS/earnings-20261017.json.gz"
echo 'x' | gzip > "$ASSETS/other-20261018.json.gz"
bash "$fetch" "$tmp/e1.json" >/dev/null
[ "$(cat "$tmp/e1.json")" = '{"new":1}' ] || fail "newest not unpacked: $(cat "$tmp/e1.json")"

# Unpacks to more than 2 MB (a small archive).
rm -f "$ASSETS"/*
head -c 3000000 /dev/zero | gzip > "$ASSETS/earnings-20261018.json.gz"
bash "$fetch" "$tmp/e2.json" >/dev/null 2>&1 || true
[ ! -e "$tmp/e2.json" ] || fail "unpacked a file over the limit"

# Not gzip.
rm -f "$ASSETS"/*
echo '{"plain":1}' > "$ASSETS/earnings-20261018.json.gz"
bash "$fetch" "$tmp/e3.json" >/dev/null 2>&1 || true
[ ! -e "$tmp/e3.json" ] || fail "unpacked a broken archive"

# A stale FILE from before is removed when nothing good is found.
echo stale > "$tmp/e4.json"
bash "$fetch" "$tmp/e4.json" >/dev/null 2>&1 || true
[ ! -e "$tmp/e4.json" ] || fail "kept an old file"

# The listing works but the download fails: an old FILE is removed too.
rm -f "$ASSETS"/*
echo '{"new":1}' | gzip > "$ASSETS/earnings-20261019.json.gz"
echo stale > "$tmp/e5.json"
FAIL_DOWNLOAD=1 bash "$fetch" "$tmp/e5.json" >/dev/null 2>&1 || true
[ ! -e "$tmp/e5.json" ] || fail "kept an old file after a failed download"
echo "fetch-earnings.sh: ok"
