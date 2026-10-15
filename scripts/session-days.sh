#!/usr/bin/env bash
# Prints the weekdays (YYYYMMDD, New York dates) whose IEX files a run looks
# for, oldest first: the last 10 days and today. Today is included because
# IEX can post a session's file before midnight in New York (03:20 UTC is
# still the evening before there). A day not posted yet is a cheap 404.
#
#   scripts/session-days.sh [NOW]   (NOW: anything date -d reads; default now)
set -euo pipefail
now="${1:-now}"
base=$(TZ=America/New_York date -d "$now" +%Y-%m-%d)
for i in $(seq 10 -1 0); do
  d=$(date -d "$base $i days ago" +%Y%m%d)
  case $(date -d "$d" +%u) in 6|7) continue ;; esac
  echo "$d"
done
