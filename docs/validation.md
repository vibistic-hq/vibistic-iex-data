# Validation

The published data is checked against a licensed full-market feed with `tools/validate.py`: closes, highs, lows, day VWAPs, 20-day SMAs and a long anchored VWAP against split-adjusted full-market bars, and every split the site adjusted, cut or missed against a corporate-actions list.

That feed's license covers the account holder's own use only, and anything computed from it counts as its data. So the results are not published here: no gap statistics, no split counts, no per-symbol comparisons. The script prints its report to a file outside version control (see its docstring), and CI fails if one of its figures is committed (`scripts/check-no-licensed-figures.sh`).

What the checks led to is in the rules themselves (README): incomplete IEX files are refused, and split detection leaves 3-for-2 gaps unadjusted and cuts the history there.
