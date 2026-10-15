#!/usr/bin/env bash
# Tests scripts/check-no-licensed-figures.sh on reports written at run time (a
# fixture in the repo would trip the check itself). Every symbol, date and
# figure here is made up: never paste a real report line into this file.
set -euo pipefail
check="$(dirname "$0")/check-no-licensed-figures.sh"
dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT
A=Alpaca

expect() { # expect pass|fail file
  if bash "$check" "$2" 2>/dev/null; then got=pass; else got=fail; fi
  [ "$got" = "$1" ] || { echo "FAIL: $2: want $1, got $got" >&2; exit 1; }
}

printf '<!-- licensed-data: personal use only, never commit -->\n\n# Report\n' > "$dir/marker.md"
expect fail "$dir/marker.md"
printf '# Validation against %s SIP\n' "$A" > "$dir/old-title.md"
expect fail "$dir/old-title.md"
printf "Gap from %s's split-adjusted SIP bars\n" "$A" > "$dir/old-gap.md"
expect fail "$dir/old-gap.md"
printf 'Splits %s lists for covered symbols: 777.\n' "$A" > "$dir/old-splits.md"
expect fail "$dir/old-splits.md"
printf "### Not in %s's list\n" "$A" > "$dir/old-extra.md"
expect fail "$dir/old-extra.md"
printf -- '- XYZ 2001-01-02: site 1:7 on 2001-01-02, %s 1:9\n' "$A" > "$dir/old-ratio.md"
expect fail "$dir/old-ratio.md"
printf -- '- XYZ 2001-01-03: site 1:2, %s 12.5:13\n' "$A" > "$dir/old-decimal.md"
expect fail "$dir/old-decimal.md"
printf '%s 0.123456:1\n' "$A" > "$dir/old-decimal2.md"
expect fail "$dir/old-decimal2.md"
printf -- '- XYZ 2001-01-04 (0.654321:1)\n' > "$dir/missed-row.md"
expect fail "$dir/missed-row.md"
printf -- '- ABC.B 2001-01-05 (9:7)\n' > "$dir/missed-row2.md"
expect fail "$dir/missed-row2.md"
printf -- '- ABC 2001-01-08 1:3 (detected)\n' > "$dir/extra-row.md"
expect fail "$dir/extra-row.md"
printf '| over $1B/day | 111 | 9.91 / 9.92 | 9.93 / 9.94 |\n' > "$dir/gap-row.md"
expect fail "$dir/gap-row.md"
printf -- '- adjusted with the right ratio: 111 (2 of them late)\n- adjusted with a different ratio: 3\n' > "$dir/count-row.md"
expect fail "$dir/count-row.md"
printf -- '- history cut there instead (fail closed): 44\n' > "$dir/cut-row.md"
expect fail "$dir/cut-row.md"
printf -- '- missed (history runs across an unadjusted split): 55\n' > "$dir/missed-count.md"
expect fail "$dir/missed-count.md"
printf -- "- splits the site made that %s does not list (false adjustments): 66\n" "$A" > "$dir/false-count.md"
expect fail "$dir/false-count.md"
printf 'x\0y\n# Validation against %s SIP\n' "$A" > "$dir/binary.dat"
expect fail "$dir/binary.dat"
printf 'anything\n' > "$dir/validation.local.md"
expect fail "$dir/validation.local.md"

# Mentioning the validator or the license is fine.
printf "tools/validate.py checks against %s's full-market data; results are never published.\n" "$A" > "$dir/readme.md"
expect pass "$dir/readme.md"
expect pass "$(dirname "$0")/../docs/validation.md"
expect pass "$(dirname "$0")/../README.md"
printf -- '- 2001-01-10 was partial (11:11 start).\n| Stocks | n |\n' > "$dir/plain.md"
expect pass "$dir/plain.md"
# The report's lines about the site alone are the site's own public facts.
printf 'Breaks in served histories: jump 888, quiet 999.\n' > "$dir/site-breaks.md"
expect pass "$dir/site-breaks.md"
printf 'Site as of 2001-01-09, 333 sessions archived; 4444 symbols, 2222 with coverage "ok".\n' > "$dir/site-summary.md"
expect pass "$dir/site-summary.md"
echo ok
