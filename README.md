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

- `v1/og/{SYMBOL}.png`: for every `ok` symbol, the 1200x630 preview image of a [Vibistic Stocks](https://stocks.vibistic.com) share link (`stocks.vibistic.com/s/{SYMBOL}`): the IEX close and the date of that bar, the change from the previous close, the 52-week low and high, the 50 and 200 day simple moving averages, and six months of closes with the 50-day SMA. Each image carries IEX's citation with the terms address and "IEX trades only, end of day. A reference point only, not a basis for trading decisions. Not investment advice." An image cannot hold a clickable link; the share page it belongs to carries the citation with its link. Drawn by `internal/og` in Montserrat (SIL Open Font License, `internal/og/fonts/OFL.txt`, from github.com/JulietaUla/Montserrat at 555facfb). Its numbers are the app's: `internal/og/testdata/golden.json` is written by the app's own engine (vibistic-stocks `scripts/og-golden.mts`) from the synthetic files next to it, and the tests here must match it to the bit; `OG_DUMP_SITE`/`OG_DUMP_OUT`/`OG_DUMP_NOW` make `TestDumpTexts` write every card's text of a built site for a whole-site comparison.

- `v1/sec/{SYMBOL}.json` (issue #8): revenue and net income per fiscal quarter (up to 12) and fiscal year (up to 5), from the company's SEC EDGAR filings, for the [Vibistic Stocks](https://stocks.vibistic.com) share page. Numbers, dates and fixed codes only, never any text from a filing. See "SEC figures" below.

## How it is made

1. **Each session** (`cmd/iexagg`): stream IEX's TOPS 1.6 file for the day (about 9 GB compressed) and sum up each symbol's regular-session IEX trades. Regular hours come from IEX's own system events, so half days are right. A file whose regular session does not open at 09:30 and close at 16:00 New York time (13:00 on the exchanges' half days: July 3 and December 24 from Monday to Thursday, and the day after Thanksgiving) is incomplete and the day is left out for every symbol, rather than summed from part of the day; IEX's 2024-12-23 file, which starts at 11:50, is the one case so far. Such a day is never archived (2024-12-23, archived before this check, was removed; the build also skips any such file). To pick up a corrected file if IEX ever republishes one, delete the day's asset from its `days-YYYY-MM` release and run the backfill for that date. Broken trades are removed. Open, high, low and close use trades that are neither odd lots nor extended hours; volume and VWAP use every regular-session trade, as the consolidated tape does. The summary (about 0.4 MB) is archived as a release asset (`days-YYYY-MM` releases).
2. **Every night** (`cmd/build`): rebuild every symbol's file from the archive.
   - **Splits.** IEX's data is not split-adjusted and there is no free split list we may redistribute, so splits are read from the prices:
     - an overnight gap of 10x or more either way, within 5% of a ratio companies use (10, 12, 15, 20, 25, ...), is a split;
     - a gap of 30% or more within 8% of a whole-number ratio companies use (2 to 8, 10, 12, 15, 20, either way) is a split only if that day's IEX dollar volume stays within 2.5x of its usual level (a split leaves the dollars traded about the same; a real move that size comes with a burst of trading). Ratios like 3:2 are not adjusted: in the maintainer's checks against a licensed split list, most such gaps were earnings moves, not splits. A -33% gap at no more than 2x the usual dollar volume could be either a 3-for-2 split or an earnings drop, so the history is cut there (fail closed).
   - **Breaks (fail closed).** Any other overnight gap of 40% or more, or more than 20 sessions without an IEX trade (a delisting, or a ticker reused by another company), cuts the history: the file starts after it.
   - **Reviewed exceptions** in `splits-reviewed.json` (by pull request only) win over the rules: `split` (with the share ratio), `real` (a real move, not a break) or `break`. Each must sit on a real gap (a split near its ratio, a real move of 30% or more; a forced break anywhere), or the build fails rather than adjust the wrong bars.
   - Dividends are not adjusted, like the split-only bars Vibistic Stocks uses from Alpaca.

Publishing runs `go test ./...` before the build and `scripts/check-site-size.sh` after it: a failing test, a site over 900 MB (GitHub Pages allows 1 GB) or a preview image over 300 KB stops the publish and keeps the last deployment.

## SEC figures

Built once a day by `sec.yml` (`cmd/secbuild`, `internal/sec`) from SEC's bulk [companyfacts.zip](https://www.sec.gov/search-filings/edgar-application-programming-interfaces) and its ticker list `company_tickers_exchange.json`. SEC filings are public; SEC's fair-access policy asks for a User-Agent naming who is asking with a contact address, which the two downloads send (the only place that address is used).

- **Which stocks.** For each company, only the first ticker SEC lists for it that is not a warrant, unit or right (its main common stock; the others are preferreds, notes, warrants, units and other classes), shaped like the app's symbols (`AAPL`, `BRK.B`; SEC writes `BRK-B`), not a warrant, unit or right (`.W`/`.U`/`.R`, or a 5-letter ticker ending in W, U or R). Other common share classes only when listed in `sec-reviewed.json` (by pull request only) with the company's CIK, so a reused ticker cannot inherit them: revenue and net income are the company's, the same for every class. A symbol listed under two companies is dropped. Funds and trusts (gold, oil, currency, crypto and volatility products, matched on the name SEC lists, which is used for nothing else) are left out: their "net income" is the change in value of what they hold. Companies reporting in US GAAP in US dollars on 10-Q and 10-K; not most foreign companies (20-F, 40-F, IFRS). A company whose newest period ended more than 400 days ago gets no file.
- **Which figures.** Revenue from `Revenues`, `RevenueFromContractWithCustomerExcludingAssessedTax`, `RevenueFromContractWithCustomerIncludingAssessedTax`, `SalesRevenueNet`, `RevenuesNetOfInterestExpense` (banks' net revenue), `RegulatedAndUnregulatedOperatingRevenue` (utilities) and `RealEstateRevenueNet`: shown only when every one of them the filing reports for the period agrees within 1%; when two differ, which one is the total on the income statement cannot be told from these facts (Berkshire's and Chevron's larger figure is the total, Republic Services' counts intercompany sales), so it is null (`why: "conflict"`; owner's decision 2026-10-18). Nor is a figure smaller than the sum of the lease income, net interest income and noninterest income the same filing reports (a real estate company's contract revenue leaves out its rents, a bank's its interest income). A year-earlier `prev` is published only when every concept the filing reports for that earlier period agrees too. Net income: `NetIncomeLoss` (attributable to the company). The concept is recorded with each value (`c`). No per-share figures: a share basis (splits, classes, ADR ratios) cannot be checked from free data.
- **As first reported.** Each period comes from the earliest 10-Q, 10-K or 10-KT that reports it (a fiscal year only from an annual report: a 10-Q's trailing twelve months is not one) (later restatements, reclassifications and amendments are not reflected), so a year's quarters and the year are on one basis. Two different values in filings of that one day: null (`why: "ambiguous"`).
- **Periods.** Quarters of 83 to 120 days, both ends counted (12- to 17-week fiscal quarters: Costco's 12-week quarters, Kroger's 16-week first quarter), nine months of 250 to 288 days, years of 358 to 372 days. A period never ends after the filing that reports it.
- **Fourth quarters.** No 10-Q reports them: Q4 is the year minus the nine months that start with it, both as first reported (the nine months filed before the year), under the year's largest concept that the nine months also report, the remainder being a quarter. Marked `derived` with both filings; a revenue result below zero or above the year is null (`why: "check"`). A fourth quarter filed by itself is kept only when it agrees with that (within 0.5% of the year); one equal to the whole year (some filers tag the year's total with the quarter's dates) gives way to the computed one; any other disagreement is null.
- **Consistency.** Companies' own tags have slips, so revenue is checked against the arithmetic between periods; a figure that fails shows as null (`why: "check"`):
  - the first three quarters must add up to the nine months (within 0.5%) for a computed fourth quarter, and a year over ten times its nine months is not used;
  - a year smaller than one of its quarters is not shown, nor the fourth quarter computed from it; a year over ten times three or more of its quarters is not shown, nor those quarters;
  - each figure is compared with the same period as the next year's filing shows it again under the same concept: off by more than 3 times, it is not shown; off by about a thousand or a million times (thousands tagged as dollars) in a period shown, none of the company's revenue is shown;
  - a computed fourth quarter needs nine months the company did not restate later (shown again more than 1% off in the year's 10-K or the next year's third-quarter 10-Q: the year may be on the new basis);
  - a computed fourth quarter's `prev` (from two filings) is kept only when it matches that quarter as first reported a year before;
  - net income is compared with the same period in the next year's filing too: more than 25% (and $1 million) off, it is not shown;
  - a quarter's revenue or net income equal to the dollar to the quarter before, from another filing (a tag carried over with the wrong dates), is not shown, nor that quarter's;
  - nine months revised in the year's 10-K count as restated.
  These catch inconsistent tags, not a tag that is wrong the same way in every filing (Kinetik tagged adjusted EBITDA as net income in 2023 and 2024): figures are as the companies tagged them.
  A negative reported revenue is null (`why: "negative"`).
- **Year-earlier values.** `prev` is the period a year earlier from the same filing under the same concept, so a change between them is like for like.
- **Checks.** Each company file must be one JSON document. `secbuild` reads each zip entry to its end (so its checksum is checked) and skips a company with two entries or one over 256 MB. It refuses a snapshot with fewer than 4,000 files or without a quarter ending in the last 200 days for AAPL, MSFT and NVDA; every file is validated (`sec.Validate`, the same rules as the app). Snapshots are kept as `sec-YYYYMMDD.tar.gz` assets of the release `sec` (newest 14). Publishing fetches the newest one outside the checkout (at most 100 MB, 20,000 entries and 500 MB unpacked, only `snapshot.json` and symbol files), re-validates it (each file at most 256 KB, one JSON document, known fields only) and leaves `v1/sec` out, without stopping the IEX files, if it is missing, older than 10 days or has any bad file.

Workflows: `nightly.yml` (every 30 minutes from 03:07 to 11:37 UTC Tuesday to Saturday, and at 21:07, so each day goes up soon after IEX posts it; catches up on the last 10 days and does not download an incomplete IEX file again until IEX replaces it), `backfill.yml` (a date range, by hand), `publish.yml` (build and deploy Pages), `sec.yml` (daily SEC snapshot, then publish). They use no stored secrets: release assets go up with the workflow's own token, Pages deploys with GitHub's.

`tools/validate.py` is a developer-only check against Alpaca's full-market data (it needs a personal Alpaca key and is never run in CI). Alpaca licenses that data for the key holder's own use, so its results are never published: see [`docs/validation.md`](docs/validation.md).

## Develop

```
go test ./...
go build -o bin/iexagg ./cmd/iexagg
scripts/process-day.sh 20261009 out      # one session, about 9 GB streamed
go run ./cmd/build -days out -out site   # build the site from day files
```
