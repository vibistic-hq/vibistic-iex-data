# vibistic-iex-data

Free end-of-day stock data for [Vibistic Stocks](https://stocks.vibistic.com), built from IEX's historical trade data and published as static files on GitHub Pages.

Data provided for free by IEX. By accessing or using IEX Historical Data, you agree to the [IEX Historical Data Terms of Use](https://iextrading.com/iex-historical-data-terms/).

The data reflects trading on IEX only, not other markets. It is a reference point only, not a basis for trading decisions, and not investment advice.

## What is published

`https://vibistic-hq.github.io/vibistic-iex-data/`

- `v1/manifest.json`: the session the data runs to (`asOf`), when it was built, and how many symbols it covers.
- `v1/s/{SYMBOL}.json`: one file per symbol, for symbols shaped like the app's (`AAPL`, `BRK.B`):
  - `coverage`: `"ok"`, or `"sparse"` when IEX trading in the stock is too thin for reliable numbers (fewer than 90% of the last 60 sessions with an IEX price, that is a trade that is not an odd lot, or under $50,000 of IEX trading on the median session). Sparse files carry only `metrics`, no bars.
  - `bars`: `[yyyymmdd, open, high, low, close, volume, vwap]` per session, oldest first, the last four years, adjusted for the splits listed in `splits`.
  - `splits` and `breaks`: see below.

## How it is made

1. **Each session** (`cmd/iexagg`): stream IEX's TOPS 1.6 file for the day (about 9 GB compressed) and sum up each symbol's regular-session IEX trades. Regular hours come from IEX's own system events, so half days are right. A file whose regular session does not open at 09:30 and close at 16:00 New York time (13:00 on the exchanges' half days: July 3 and December 24 from Monday to Thursday, and the day after Thanksgiving) is incomplete and the day is left out for every symbol, rather than summed from part of the day; IEX's 2024-12-23 file, which starts at 11:50, is the one case so far. Such a day is never archived (2024-12-23, archived before this check, was removed; the build also skips any such file). To pick up a corrected file if IEX ever republishes one, delete the day's asset from its `days-YYYY-MM` release and run the backfill for that date. Broken trades are removed. Open, high, low and close use trades that are neither odd lots nor extended hours; volume and VWAP use every regular-session trade, as the consolidated tape does. The summary (about 0.4 MB) is archived as a release asset (`days-YYYY-MM` releases).
2. **Every night** (`cmd/build`): rebuild every symbol's file from the archive.
   - **Splits.** IEX's data is not split-adjusted and there is no free split list we may redistribute, so splits are read from the prices:
     - an overnight gap of 10x or more either way, within 5% of a ratio companies use (10, 12, 15, 20, 25, ...), is a split;
     - a gap of 30% or more within 8% of a whole-number ratio companies use (2 to 8, 10, 12, 15, 20, either way) is a split only if that day's IEX dollar volume stays within 2.5x of its usual level (a split leaves the dollars traded about the same; a real move that size comes with a burst of trading). Ratios like 3:2 are not adjusted: in the maintainer's checks against a licensed split list, most such gaps were earnings moves, not splits. A -33% gap at no more than 2x the usual dollar volume could be either a 3-for-2 split or an earnings drop, so the history is cut there (fail closed).
   - **Breaks (fail closed).** Any other overnight gap of 40% or more, or more than 20 sessions without an IEX trade (a delisting, or a ticker reused by another company), cuts the history: the file starts after it.
   - **Reviewed exceptions** in `splits-reviewed.json` (by pull request only) win over the rules: `split` (with the share ratio), `real` (a real move, not a break) or `break`. Each must sit on a real gap (a split near its ratio, a real move of 30% or more; a forced break anywhere), or the build fails rather than adjust the wrong bars.
   - Dividends are not adjusted, like the split-only bars Vibistic Stocks uses from Alpaca.

Workflows: `nightly.yml` (07:23 UTC Tuesday to Saturday, again at 10:23 in case GitHub drops a scheduled run, catching up on the last 10 days), `backfill.yml` (a date range, by hand), `publish.yml` (build and deploy Pages). They use no stored secrets: release assets go up with the workflow's own token, Pages deploys with GitHub's.

`tools/validate.py` is a developer-only check against Alpaca's full-market data (it needs a personal Alpaca key and is never run in CI). Alpaca licenses that data for the key holder's own use, so its results are never published: see [`docs/validation.md`](docs/validation.md).

## Develop

```
go test ./...
go build -o bin/iexagg ./cmd/iexagg
scripts/process-day.sh 20261009 out      # one session, about 9 GB streamed
go run ./cmd/build -days out -out site   # build the site from day files
```
