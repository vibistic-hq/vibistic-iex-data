#!/usr/bin/env bash
# Emails the owner through GitHub (issue #27): each alert is an issue opened by
# the workflow's bot, assigned to the owner and naming them, so GitHub emails
# them (it never emails anyone about their own actions). Only changes alert:
# never one issue per run.
#
#   scripts/notify.sh nightly   (nightly.yml's notify job)
#     env: STATE (file kept in the Actions cache: list=refused|working,
#          nightly=ok|failing; missing = refused, ok), ARCHIVE_RESULT (the
#          archive job's result), INDEX_CODE (HTTP status of IEX's HIST list,
#          or curl-failed), RUN_URL, FAILED_LOG (file: the failed run's log tail)
#     - the list answering again after being refused: one issue
#       (label hist-list-back), closed again if IEX refuses it later
#     - archive failing after it worked: one issue (label nightly-failing) with
#       the log tail; when a run works again: a comment and the issue closed
#   scripts/notify.sh local RESULT DETAIL   (notify.yml, sent from the desktop)
#     The desktop sends every run's result, not changes: failed opens an issue
#     (label fund-index-local) unless one is open; ok comments on and closes it.
#     So the open issue is the record, and a lost or replaced dispatch is put
#     right by the next run.
#
# Writes changed=1 to $GITHUB_OUTPUT (if set) when STATE changed.
set -euo pipefail
OWNER="${NOTIFY_OWNER:-paragkalra}"

open_issues() { # open_issues LABEL: numbers of open issues with that label
  gh issue list --state open --label "$1" --json number --jq '.[].number'
}
# A failed lookup must stop the run (and so keep the old state): read as "no
# issue open", it would close nothing yet record the change, or open duplicates.
open_alert() { # open_alert LABEL TITLE BODY: an issue unless one is open
  local open
  open=$(open_issues "$1") || { echo "could not list $1 issues" >&2; return 1; }
  if [ -n "$open" ]; then echo "an open $1 issue already exists" >&2; return 0; fi
  gh issue create --label "$1" --assignee "$OWNER" --title "$2" --body "@$OWNER $3"
}
close_alerts() { # close_alerts LABEL COMMENT: comment on and close every open one
  local n open
  open=$(open_issues "$1") || { echo "could not list $1 issues" >&2; return 1; }
  for n in $open; do
    gh issue comment "$n" --body "@$OWNER $2"
    # Marks the alert as ended by the problem going away (not muted by hand).
    gh issue edit "$n" --add-label auto-closed
    gh issue close "$n"
  done
}
# last_auto_closed LABEL: true if the newest issue with that label was closed by
# this script. Then a "still failing" state is stale (a recovery whose saved
# state was lost) and a new problem must alert again; one the owner closed by
# hand stays muted.
last_auto_closed() {
  local labels
  labels=$(gh issue list --state all --label "$1" --limit 1 --json labels --jq '.[].labels[].name') \
    || { echo "could not list $1 issues" >&2; return 1; }
  grep -qx auto-closed <<<"$labels"
}
# Text from logs or the desktop goes inside a fenced block; a fence in it would
# end the block early, so backticks are swapped for quotes, and it is bounded.
fenced() { printf '```\n%s\n```' "$(printf '%s' "$1" | tr '`' "'" | tail -c 4000)"; }

case "${1:-}" in
nightly)
  list=refused; nightly=ok
  if [ -f "$STATE" ]; then
    l=$(sed -n 's/^list=\(refused\|working\)$/\1/p' "$STATE"); [ -n "$l" ] && list=$l
    n=$(sed -n 's/^nightly=\(ok\|failing\)$/\1/p' "$STATE"); [ -n "$n" ] && nightly=$n
  fi
  new_list=$list
  case "$INDEX_CODE" in
    200|404) new_list=working ;; # 404: the list works, that day has no file
    403) new_list=refused ;;
  esac # anything else (a network error) says nothing about the list
  new_nightly=$nightly
  case "$ARCHIVE_RESULT" in
    success) new_nightly=ok ;;
    failure) new_nightly=failing ;;
  esac # cancelled or skipped: unknown

  # Opening alerts only on a change (one per outage, not one per run); closing
  # on every run whose result says so, so an issue left open by a lost state
  # (an evicted cache) never hides the next alert.
  if [ "$new_list" = working ] && { [ "$list" = refused ] || last_auto_closed hist-list-back; }; then
    open_alert hist-list-back "IEX's HIST list is answering again" \
      "IEX's HIST list (https://iextrading.com/api/1.0/hist) answered $INDEX_CODE instead of 403. Nightly uses it again on its own; the bucket fallback (#25) stays for the next refusal, and \`backfill.yml\` works again. Run: $RUN_URL"
  elif [ "$INDEX_CODE" = 403 ]; then
    close_alerts hist-list-back "IEX refuses the HIST list again (403); Nightly reads its bucket. Run: $RUN_URL"
  fi
  if [ "$ARCHIVE_RESULT" = success ]; then
    close_alerts nightly-failing "Nightly works again. Run: $RUN_URL"
  elif [ "$new_nightly" = failing ] && { [ "$nightly" = ok ] || last_auto_closed nightly-failing; }; then
    log=""; [ -f "${FAILED_LOG:-}" ] && log=$(cat "$FAILED_LOG")
    open_alert nightly-failing "Nightly failed: IEX data is not being updated" \
      "Nightly's archive job failed, so new IEX days are not being added (with IEX's list refused, this includes reading IEX's bucket). One alert per outage; a working run closes this issue. Run: $RUN_URL

$(fenced "$log")"
  fi

  printf 'list=%s\nnightly=%s\n' "$new_list" "$new_nightly" > "$STATE.new"
  if [ "$new_list" != "$list" ] || [ "$new_nightly" != "$nightly" ] || [ ! -f "$STATE" ]; then
    [ -n "${GITHUB_OUTPUT:-}" ] && echo "changed=1" >> "$GITHUB_OUTPUT"
  fi
  mv "$STATE.new" "$STATE"
  ;;
local)
  event="${2:-}"; detail="${3:-}"
  case "$event" in
    failed)
      open_alert fund-index-local "TL;DR Fund index failed on the desktop" \
        "The local Fund index job (TL;DR 13F index, run by a systemd timer on the desktop while GitHub Actions is off) failed, so tldr.vibistic.com keeps its last index. Log: ~/vibistic-local-jobs/logs on the desktop. A later good run closes this issue.

$(fenced "$detail")" ;;
    ok) close_alerts fund-index-local "The local Fund index job works again." ;;
    *) echo "usage: notify.sh local failed|ok [DETAIL]" >&2; exit 2 ;;
  esac
  ;;
*) echo "usage: notify.sh nightly | local EVENT [DETAIL]" >&2; exit 2 ;;
esac
