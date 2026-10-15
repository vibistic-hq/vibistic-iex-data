#!/usr/bin/env bash
# Tests scripts/session-days.sh: New York dates, weekdays only, today included.
set -euo pipefail
days="$(dirname "$0")/session-days.sh"
expect() { # expect NOW want-last want-first
  local got; got=$(bash "$days" "$1")
  [ "$(tail -1 <<<"$got")" = "$2" ] || { echo "FAIL: $1: last $(tail -1 <<<"$got"), want $2" >&2; exit 1; }
  [ "$(head -1 <<<"$got")" = "$3" ] || { echo "FAIL: $1: first $(head -1 <<<"$got"), want $3" >&2; exit 1; }
  if grep -q . <<<"$(for d in $got; do case $(date -d "$d" +%u) in 6|7) echo "$d" ;; esac; done)"; then echo "FAIL: $1: weekend listed" >&2; exit 1; fi
}
# Tuesday 03:37 UTC is still Monday evening in New York (summer time):
# Monday's session is looked for.
expect "2026-10-13 03:37 UTC" 20261012 20261002
# Winter: 04:37 UTC is still the evening before (UTC-5).
expect "2026-01-13 04:37 UTC" 20260112 20260102
# Saturday 21:07 UTC: Saturday in New York, so Friday is the latest.
expect "2026-10-17 21:07 UTC" 20261016 20261007
echo "session-days.sh: ok"
