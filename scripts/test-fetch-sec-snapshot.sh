#!/usr/bin/env bash
# Tests scripts/fetch-sec-snapshot.sh with a stand-in gh: the newest snapshot
# is unpacked; one with anything but symbol files and snapshot.json is not.
set -euo pipefail
fetch="$(cd "$(dirname "$0")" && pwd)/fetch-sec-snapshot.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin" "$tmp/assets"
cat > "$tmp/bin/gh" <<'GH'
#!/usr/bin/env bash
# gh release view sec --json assets --jq ... | gh release download sec -p NAME -D DIR
case "$1 $2" in
  "release view") for f in "$ASSETS"/*; do [ -e "$f" ] && echo "$(basename "$f") $(stat -c %s "$f")"; done ;;
  "release download") cp "$ASSETS/$5" "$7/" ;;
esac
GH
chmod +x "$tmp/bin/gh"
export PATH="$tmp/bin:$PATH" ASSETS="$tmp/assets"
snap() { # snap NAME ENTRY... (written as is, ../ included)
  python3 - "$ASSETS/$1" "${@:2}" <<'PY'
import io, sys, tarfile
with tarfile.open(sys.argv[1], "w:gz") as t:
    for name in sys.argv[2:]:
        i = tarfile.TarInfo("./" + name if not name.startswith("..") else name)
        i.size = 2
        t.addfile(i, io.BytesIO(b"{}"))
PY
}
fail() { echo "FAIL: $*" >&2; exit 1; }

# None at all: nothing, and no error.
bash "$fetch" "$tmp/out0" >/dev/null
[ ! -e "$tmp/out0" ] || fail "unpacked with no snapshot"

snap sec-20261016.tar.gz snapshot.json AAPL.json
snap sec-20261017.tar.gz snapshot.json AAPL.json BRK.B.json
bash "$fetch" "$tmp/out1" >/dev/null
[ "$(ls "$tmp/out1" | tr '\n' ' ')" = "AAPL.json BRK.B.json snapshot.json " ] || fail "newest not unpacked: $(ls "$tmp/out1")"

for bad in "../evil.json" "sub/AAPL.json" "aapl.json" "AAPL.js" "index.html" "x_test.go" "AAPL.json AAPL.json"; do
  rm -f "$ASSETS"/*
  snap sec-20261018.tar.gz snapshot.json "$bad"
  bash "$fetch" "$tmp/out2" >/dev/null 2>&1 || true
  [ ! -e "$tmp/out2" ] || fail "unpacked a snapshot holding $bad"
done
# A symbolic link.
rm -f "$ASSETS"/*
d=$(mktemp -d); echo '{}' > "$d/snapshot.json"; ln -s /etc/passwd "$d/AAPL.json"
tar -C "$d" -czf "$ASSETS/sec-20261018.tar.gz" .; rm -rf "$d"
bash "$fetch" "$tmp/out3" >/dev/null 2>&1 || true
[ ! -e "$tmp/out3" ] || fail "unpacked a snapshot holding a link"
# A bad entry first, then many good ones: the whole listing is checked.
rm -f "$ASSETS"/*
d=$(mktemp -d); echo '{}' > "$d/snapshot.json"; ln -s /etc/passwd "$d/AAA.json"
for i in $(seq 1 3000); do printf -v n 'B%04d' "$i"; n=$(tr '0-9' 'A-J' <<<"$n"); echo '{}' > "$d/${n:0:5}.json"; done
tar -C "$d" -czf "$ASSETS/sec-20261018.tar.gz" .; rm -rf "$d"
bash "$fetch" "$tmp/out4" >/dev/null 2>&1 || true
[ ! -e "$tmp/out4" ] || fail "unpacked a snapshot with a link before many good files"
# Too many entries.
rm -f "$ASSETS"/*
d=$(mktemp -d); echo '{}' > "$d/snapshot.json"
python3 -c "
import sys,itertools,string
for i,t in enumerate(itertools.product(string.ascii_uppercase, repeat=4)):
    if i > 20001: break
    open(sys.argv[1]+'/'+''.join(t)+'.json','w').write('{}')
" "$d"
tar -C "$d" -czf "$ASSETS/sec-20261018.tar.gz" .; rm -rf "$d"
bash "$fetch" "$tmp/out5" >/dev/null 2>&1 || true
[ ! -e "$tmp/out5" ] || fail "unpacked a snapshot of over 20,000 entries"
# A small archive whose listing is huge (PAX headers: long names; owner
# names are not listed).
rm -f "$ASSETS"/*
python3 - "$ASSETS/sec-20261018.tar.gz" <<'PY'
import io, sys, tarfile
with tarfile.open(sys.argv[1], "w:gz", format=tarfile.PAX_FORMAT) as t:
    for i in range(100):
        info = tarfile.TarInfo("./" + "A" * 100000 + str(i) + ".json")
        info.uname = "u" * 100000
        info.size = 2
        t.addfile(info, io.BytesIO(b"{}"))
PY
[ "$(stat -c %s "$ASSETS/sec-20261018.tar.gz")" -lt 200000 ] || fail "test archive not small"
bash "$fetch" "$tmp/out6" >/dev/null 2>&1 || true
[ ! -e "$tmp/out6" ] || fail "unpacked an archive with a huge listing"
echo "fetch-sec-snapshot.sh: ok"
