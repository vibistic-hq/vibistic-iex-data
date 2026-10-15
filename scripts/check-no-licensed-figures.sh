#!/usr/bin/env bash
# Fails if a file carries output of tools/validate.py, whose figures are computed
# from Alpaca's licensed data and may not be published. Checks the
# files given, or every tracked file except the validator and this script.
# A tripwire for pasted reports or parts of them (every kind of line the report
# prints that carries a figure from Alpaca's data; lines about the site alone,
# like its session or break counts, are public and pass), not a guarantee: never
# publish anything the validator prints.
set -euo pipefail

# The report's marker line, its headings (older reports have no marker) and the
# shape of each kind of line it prints with a figure from Alpaca's data.
patterns=(
  'licensed-data: personal use only'
  'Validation against Alpaca SIP'
  "Gap from Alpaca's"
  'Splits Alpaca lists'
  "Not in Alpaca's list"
  'Alpaca [0-9.]+:[0-9.]'
  'adjusted with (the right|a different) ratio: [0-9]'
  'history cut there instead \(fail closed\): [0-9]'
  'history runs across an unadjusted split\): [0-9]'
  'false adjustments\): [0-9]'
  '^- [A-Z][A-Z0-9.-]* [0-9]{4}-[0-9]{2}-[0-9]{2} \([0-9.]+:[0-9.]+\)'
  '^- [A-Z][A-Z0-9.-]* [0-9]{4}-[0-9]{2}-[0-9]{2} [0-9.]+:[0-9.]+ \((detected|reviewed|break)'
  '\| [0-9]+ \| [0-9.]+ / [0-9.]+ \|'
)

if [ $# -gt 0 ]; then
  files=("$@")
else
  mapfile -t files < <(git ls-files | grep -v -x -e tools/validate.py -e scripts/check-no-licensed-figures.sh -e scripts/test-check-no-licensed-figures.sh)
fi

status=0
for f in "${files[@]}"; do
  case "$f" in
    *.local.md) echo "$f: a local validation report is tracked" >&2; status=1; continue ;;
  esac
  [ -f "$f" ] || continue
  for p in "${patterns[@]}"; do
    if grep -a -n -E -e "$p" -- "$f" >&2; then
      echo "$f: carries validation output computed from Alpaca's data (never publish it)" >&2
      status=1
      break
    fi
  done
done
exit $status
