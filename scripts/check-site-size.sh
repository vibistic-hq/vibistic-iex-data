#!/usr/bin/env bash
# Fails when the built site would not fit GitHub Pages with room to spare
# (published sites are limited to 1 GB) or a preview image is too big.
set -euo pipefail
site="${1:?site dir}"
max_site=$((900 * 1000 * 1000))
max_png=$((300 * 1024))
total=$(du -sb "$site" | cut -f1)
echo "site: $total bytes ($(find "$site/v1/og" -name '*.png' | wc -l) preview images)"
if (( total > max_site )); then
  echo "site is over $max_site bytes" >&2
  exit 1
fi
big=$(find "$site/v1/og" -name '*.png' -size +"${max_png}"c | head -5)
if [[ -n "$big" ]]; then
  echo "preview images over $max_png bytes:" >&2
  echo "$big" >&2
  exit 1
fi
