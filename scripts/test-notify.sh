#!/usr/bin/env bash
# Tests scripts/notify.sh with a stand-in for gh, so nothing reaches GitHub.
set -euo pipefail
script="$(cd "$(dirname "$0")" && pwd)/notify.sh"
dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT
mkdir -p "$dir/bin"
# gh: logs each call to $dir/calls; "issue list" prints the numbers in
# $dir/open-<label> (open issues with that label).
cat > "$dir/bin/gh" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$FAKE/calls"
if [ "$1 $2" = "issue list" ]; then
  [ -f "$FAKE/list-fails" ] && exit 1
  all=""
  while [ $# -gt 0 ]; do [ "$1" = --label ] && label=$2; [ "$1" = all ] && all=1; shift; done
  # --state all: the newest issue's labels, from $dir/last-<label>.
  if [ -n "$all" ]; then cat "$FAKE/last-$label" 2>/dev/null || true; else cat "$FAKE/open-$label" 2>/dev/null || true; fi
fi
EOF
chmod +x "$dir/bin/gh"
export FAKE="$dir" PATH="$dir/bin:$PATH" STATE="$dir/state" RUN_URL="https://example.test/run/1"
fail() { echo "FAIL: $*" >&2; exit 1; }
calls() { grep -c "$1" "$dir/calls" 2>/dev/null || true; }
acted() { grep -cE '^issue (create|comment|close)' "$dir/calls" 2>/dev/null || true; }
nightly() { # nightly ARCHIVE_RESULT INDEX_CODE
  : > "$dir/calls"; : > "$dir/out"
  ARCHIVE_RESULT=$1 INDEX_CODE=$2 GITHUB_OUTPUT="$dir/out" bash "$script" nightly
}

# First run, no state: list refused and nightly ok are assumed, so nothing is sent.
nightly success 403
[ "$(calls 'issue create')" = 0 ] || fail "alerted on the first run"
[ "$(cat "$STATE")" = $'list=refused\nnightly=ok' ] || fail "state: $(cat "$STATE")"
grep -q changed=1 "$dir/out" || fail "first state not saved"
# Same again: nothing sent, state unchanged, not saved again.
nightly success 403
[ "$(acted)" = 0 ] || fail "acted with nothing new: $(cat "$dir/calls")"
! grep -q changed=1 "$dir/out" || fail "saved an unchanged state"

# The list answers again (200, or 404 for a day without a file): one issue.
for code in 200 404; do
  printf 'list=refused\nnightly=ok\n' > "$STATE"
  nightly success "$code"
  [ "$(calls 'issue create --label hist-list-back --assignee paragkalra')" = 1 ] || fail "no list-back issue for $code"
  grep -q '@paragkalra' "$dir/calls" || fail "owner not named"
  grep -q '^list=working$' "$STATE" || fail "state not working after $code"
  grep -q changed=1 "$dir/out" || fail "state change not saved"
done
# Still working: no second issue.
nightly success 200
[ "$(calls 'issue create')" = 0 ] || fail "second list-back issue"
# A network error says nothing: state kept, nothing sent.
nightly success curl-failed
[ "$(acted)" = 0 ] && grep -q '^list=working$' "$STATE" || fail "network error changed the list state"
# Refused again: the open issue is commented on and closed.
echo 41 > "$dir/open-hist-list-back"
nightly success 403
grep -q '^issue comment 41 ' "$dir/calls" && grep -q '^issue close 41$' "$dir/calls" || fail "list-back issue not closed: $(cat "$dir/calls")"
grep -q '^list=refused$' "$STATE" || fail "state not refused"
rm -f "$dir/open-hist-list-back"

# Archive fails after working: one issue with the log tail, fences defused.
printf 'line one\n```\nHIST index refused; IEX bucket answered 500\n' > "$dir/log"
FAILED_LOG="$dir/log" nightly failure 403
[ "$(calls 'issue create --label nightly-failing --assignee paragkalra')" = 1 ] || fail "no failure issue"
grep -q 'IEX bucket answered 500' "$dir/calls" || fail "log tail missing"
[ "$(grep -c '```' "$dir/calls")" = 2 ] || fail "log fence not defused"
grep -q '^nightly=failing$' "$STATE" || fail "state not failing"
# Failing again: no new issue (one alert per outage).
nightly failure 403
[ "$(calls 'issue create')" = 0 ] || fail "second failure issue"
# Cancelled or skipped: unknown, nothing changes.
for r in cancelled skipped; do
  nightly "$r" 403
  [ "$(acted)" = 0 ] && grep -q '^nightly=failing$' "$STATE" || fail "$r changed the state"
done
# Works again: comment + close.
echo 77 > "$dir/open-nightly-failing"
nightly success 403
grep -q '^issue comment 77 ' "$dir/calls" && grep -q '^issue edit 77 --add-label auto-closed$' "$dir/calls" && grep -q '^issue close 77$' "$dir/calls" || fail "failure issue not closed and marked"
grep -q '^nightly=ok$' "$STATE" || fail "state not ok"
# A failure while an issue is already open (state lost): no duplicate.
rm -f "$STATE"; printf 'list=refused\nnightly=ok\n' > "$STATE"
nightly failure 403
[ "$(calls 'issue create')" = 0 ] || fail "duplicate issue while one is open"
rm -f "$dir/open-nightly-failing"
# State lost (evicted cache) during an outage: a good run still closes the
# open failure issue, so it cannot hide the next outage's alert.
echo 78 > "$dir/open-nightly-failing"
rm -f "$STATE"
nightly success 403
grep -q '^issue close 78$' "$dir/calls" || fail "open failure issue not closed after state loss"
rm -f "$dir/open-nightly-failing"
# Same for the list: refused again while the state says refused.
echo 42 > "$dir/open-hist-list-back"
printf 'list=refused\nnightly=ok\n' > "$STATE"
nightly success 403
grep -q '^issue close 42$' "$dir/calls" || fail "open list-back issue not closed while refused"
rm -f "$dir/open-hist-list-back"
# GitHub failing to list issues stops the run and keeps the old state, both
# when closing (recovery) and when opening (a new failure): no duplicates.
touch "$dir/list-fails"
printf 'list=refused\nnightly=failing\n' > "$STATE"
rc=0; nightly success 403 2>/dev/null || rc=$?
[ "$rc" != 0 ] && grep -q '^nightly=failing$' "$STATE" || fail "recovery recorded although the lookup failed (rc $rc)"
printf 'list=refused\nnightly=ok\n' > "$STATE"
rc=0; FAILED_LOG=/dev/null nightly failure 500 2>/dev/null || rc=$?
[ "$rc" != 0 ] && [ "$(calls 'issue create')" = 0 ] && grep -q '^nightly=ok$' "$STATE" || fail "failure handled although the lookup failed (rc $rc)"
echo 5 > "$dir/open-fund-index-local"
rc=0; bash "$script" local ok 2>/dev/null || rc=$?
[ "$rc" != 0 ] || fail "local ok succeeded although the lookup failed"
rm -f "$dir/open-fund-index-local"
rm -f "$dir/list-fails"
# A recovery whose saved state was lost (cache save failed): the state still
# says failing, but the newest issue was closed by the script. A new failure
# must alert again.
printf 'list=refused\nnightly=failing\n' > "$STATE"
echo auto-closed > "$dir/last-nightly-failing"
FAILED_LOG=/dev/null nightly failure 403
[ "$(calls 'issue create --label nightly-failing')" = 1 ] || fail "no alert after a lost recovery state"
# Closed by the owner by hand instead (no auto-closed): stays muted.
printf 'list=refused\nnightly=failing\n' > "$STATE"
printf 'bug\n' > "$dir/last-nightly-failing"
FAILED_LOG=/dev/null nightly failure 403
[ "$(calls 'issue create')" = 0 ] || fail "alerted although the owner closed the issue"
rm -f "$dir/last-nightly-failing"
# The same for the list: state working (lost), newest list-back auto-closed.
printf 'list=working\nnightly=ok\n' > "$STATE"
echo auto-closed > "$dir/last-hist-list-back"
nightly success 200
[ "$(calls 'issue create --label hist-list-back')" = 1 ] || fail "no list-back alert after a lost state"
printf 'list=working\nnightly=ok\n' > "$STATE"
: > "$dir/last-hist-list-back"
nightly success 200
[ "$(calls 'issue create')" = 0 ] || fail "list-back alerted again while working"
rm -f "$dir/last-hist-list-back"
# A state file with junk falls back to the defaults.
printf 'list=$(rm -rf /)\nnightly=maybe\n' > "$STATE"
nightly success 403
[ "$(cat "$STATE")" = $'list=refused\nnightly=ok' ] || fail "junk state not reset: $(cat "$STATE")"

# Local events from the desktop.
: > "$dir/calls"
bash "$script" local failed 'GET / failed: 401 `x`'
[ "$(calls 'issue create --label fund-index-local --assignee paragkalra')" = 1 ] || fail "no local issue"
grep -q "GET / failed: 401 'x'" "$dir/calls" || fail "detail missing or backticks kept"
echo 9 > "$dir/open-fund-index-local"
: > "$dir/calls"
bash "$script" local failed 'again'
[ "$(calls 'issue create')" = 0 ] || fail "second local issue while one is open"
bash "$script" local ok
grep -q '^issue close 9$' "$dir/calls" || fail "local issue not closed"
rm -f "$dir/open-fund-index-local"
# ok with nothing open: nothing done (every run sends its result).
: > "$dir/calls"
bash "$script" local ok
[ "$(acted)" = 0 ] || fail "ok with nothing open acted"
rc=0; bash "$script" local recovered 2>/dev/null || rc=$?
[ "$rc" = 2 ] || fail "old event name accepted"
rc=0; bash "$script" local other 2>/dev/null || rc=$?
[ "$rc" = 2 ] || fail "unknown local event accepted"
rc=0; bash "$script" 2>/dev/null || rc=$?
[ "$rc" = 2 ] || fail "no mode accepted"

echo "notify.sh: ok"
