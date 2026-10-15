#!/usr/bin/env python3
"""Developer-only check of a built site against Alpaca's full-market data.

Not part of the pipeline and never run in CI: it needs a personal Alpaca key
(ALPACA_KEY_ID / ALPACA_SECRET in an env file), calls only
https://data.alpaca.markets, and prints a Markdown report for the key holder only.
Alpaca licenses its data for the account holder's own use, and figures computed
from it count as its data: never commit, publish or share the report. Write it
to a gitignored *.local.md file; its first line is a marker that CI rejects
(scripts/check-no-licensed-figures.sh).

    python3 -I tools/validate.py --site site --env ../vibistic-stocks/.env > validation.local.md

What it measures, for symbols the site marks coverage "ok":
  * how far IEX closes, highs, lows, day VWAPs, 20-day SMAs and a long anchored
    VWAP are from Alpaca's split-adjusted SIP bars, by size of daily trading;
  * split handling: every split Alpaca lists in the window, whether the site
    adjusted it (right ratio, within a few days), cut the history there (a break), or
    missed it; and every split or break the site made that Alpaca does not list.
"""
import argparse
import datetime as dt
import glob
import json
import os
import statistics
import sys
import urllib.parse
import urllib.request

API = "https://data.alpaca.markets"
# First line of every report: scripts/check-no-licensed-figures.sh fails CI on it.
MARKER = "<!-- licensed-data: personal use only, never commit -->"


def load_env(path):
    env = {}
    for line in open(path):
        line = line.strip()
        if "=" in line and not line.startswith("#"):
            k, v = line.split("=", 1)
            env[k.strip()] = v.strip().strip("'\"")
    return {"APCA-API-KEY-ID": env["ALPACA_KEY_ID"], "APCA-API-SECRET-KEY": env["ALPACA_SECRET"]}


def get(headers, path, query):
    req = urllib.request.Request(API + path + "?" + urllib.parse.urlencode(query), headers=headers)
    with urllib.request.urlopen(req, timeout=60) as r:
        return json.load(r)


def sip_bars(headers, symbols, start, end):
    out = {}
    for i in range(0, len(symbols), 100):
        chunk = symbols[i:i + 100]
        token = None
        while True:
            q = {"symbols": ",".join(chunk), "timeframe": "1Day", "start": start, "end": end, "feed": "sip", "adjustment": "split", "limit": 10000}
            if token:
                q["page_token"] = token
            d = get(headers, "/v2/stocks/bars", q)
            for s, bars in (d.get("bars") or {}).items():
                out.setdefault(s, []).extend(bars)
            token = d.get("next_page_token")
            if not token:
                break
    return out


def splits(headers, start, end):
    out = []
    s = dt.date.fromisoformat(start)
    e = dt.date.fromisoformat(end)
    while s <= e:
        stop = min(e, s + dt.timedelta(days=89))
        token = None
        while True:
            q = {"types": "forward_split,reverse_split", "start": s.isoformat(), "end": stop.isoformat(), "limit": 1000}
            if token:
                q["page_token"] = token
            d = get(headers, "/v1/corporate-actions", q)
            ca = d.get("corporate_actions") or {}
            out += (ca.get("forward_splits") or []) + (ca.get("reverse_splits") or [])
            token = d.get("next_page_token")
            if not token:
                break
        s = stop + dt.timedelta(days=1)
    return out


def ymd(n):
    n = int(n)
    return f"{n // 10000:04d}-{n // 100 % 100:02d}-{n % 100:02d}"


def pct(a, b):
    return abs(a / b - 1) * 100


def q(xs, p):
    if not xs:
        return float("nan")
    xs = sorted(xs)
    return xs[min(len(xs) - 1, int(p * len(xs)))]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--site", required=True)
    ap.add_argument("--env", required=True)
    ap.add_argument("--recent", type=int, default=250, help="sessions compared for price accuracy")
    args = ap.parse_args()
    headers = load_env(args.env)

    files = {}
    for p in glob.glob(os.path.join(args.site, "v1", "s", "*.json")):
        f = json.load(open(p))
        files[f["symbol"]] = f
    ok = {s: f for s, f in files.items() if f["coverage"] == "ok" and f["bars"]}
    manifest = json.load(open(os.path.join(args.site, "v1", "manifest.json")))
    as_of = manifest["asOf"]
    first = min(ymd(f["bars"][0][0]) for f in ok.values())
    print(f"validating {len(ok)} symbols as of {as_of}", file=sys.stderr)

    syms = sorted(ok)
    recent_dates = sorted({ymd(b[0]) for f in ok.values() for b in f["bars"]})[-args.recent:]
    sip = sip_bars(headers, syms, recent_dates[0], as_of)
    sip = {s: {b["t"][:10]: b for b in bars} for s, bars in sip.items()}

    buckets = [("over $1B/day", 1e9), ("$100M-1B", 1e8), ("$10M-100M", 1e7), ("$1M-10M", 1e6), ("under $1M", 0)]
    stats = {n: {k: [] for k in ("close", "high", "low", "vwap", "sma20", "avwap")} for n, _ in buckets}
    counts = {n: 0 for n, _ in buckets}
    for s in syms:
        sb = sip.get(s)
        if not sb:
            continue
        bars = {ymd(b[0]): b for b in ok[s]["bars"]}
        common = [d for d in recent_dates if d in bars and d in sb]
        if len(common) < 20:
            continue
        dv = statistics.median(sb[d]["vw"] * sb[d]["v"] for d in common)
        name = next(n for n, lo in buckets if dv >= lo)
        counts[name] += 1
        r = stats[name]
        for d in common:
            b, a = bars[d], sb[d]
            r["close"].append(pct(b[4], a["c"]))
            r["high"].append(pct(b[2], a["h"]))
            r["low"].append(pct(b[3], a["l"]))
            r["vwap"].append(pct(b[6], a["vw"]))
        last = common[-20:]
        r["sma20"].append(pct(sum(bars[d][4] for d in last) / 20, sum(sb[d]["c"] for d in last) / 20))
        r["avwap"].append(pct(sum(bars[d][6] * bars[d][5] for d in common) / sum(bars[d][5] for d in common),
                              sum(sb[d]["vw"] * sb[d]["v"] for d in common) / sum(sb[d]["v"] for d in common)))

    out = [MARKER, "", f"# Validation against Alpaca SIP", "",
           f"Site as of {as_of}, {manifest['sessions']} sessions archived; {len(files)} symbols, {len(ok)} with coverage \"ok\" (the only ones with numbers). "
           f"Price accuracy over the last {len(recent_dates)} sessions ({recent_dates[0]} to {as_of}); the anchored VWAP runs over the same window. "
           f"Checked {dt.date.today().isoformat()} with tools/validate.py.", "",
           "Gap from Alpaca's split-adjusted SIP bars, median / 90th percentile, in %:", "",
           "| Stocks trading per day | n | Close | High | Low | Day VWAP | 20 SMA | Anchored VWAP |", "|---|---|---|---|---|---|---|---|"]
    for n, _ in buckets:
        r = stats[n]
        cell = lambda k: f"{q(r[k], .5):.2f} / {q(r[k], .9):.2f}"
        out.append(f"| {n} | {counts[n]} | {cell('close')} | {cell('high')} | {cell('low')} | {cell('vwap')} | {cell('sma20')} | {cell('avwap')} |")

    # Splits, over everything the site covers.
    truth = {}
    for x in splits(headers, first, as_of):
        if x["symbol"] in ok:
            truth.setdefault(x["symbol"], []).append((x["ex_date"], x["new_rate"], x["old_rate"]))
    # A site event up to LATE_DAYS calendar days after Alpaca's date is the
    # same event: the site dates a split by the first IEX round-lot price at
    # the new level, which can come a session or two after Alpaca's ex-date
    # (no round lot that day, or Alpaca's date a day early).
    LATE_DAYS = 5

    def near(site_date, date):
        d = (dt.date.fromisoformat(site_date) - dt.date.fromisoformat(date)).days
        return 0 <= d <= LATE_DAYS

    adjusted = late = wrong_ratio = cut = missed = 0
    missed_list, wrong_list, extra_splits = [], [], []
    matched = set()
    for s, ts in truth.items():
        f = ok[s]
        first_bar = ymd(f["bars"][0][0])
        for date, new, old in ts:
            # A cut starts the served history at the break, so a cut split
            # sits on (or a few days after) the first bar: check it first.
            cuts = [b["date"] for b in f["breaks"] if near(b["date"], date)]
            is_cut = bool(cuts)
            if date < first_bar and not is_cut:
                continue  # before the served history: nothing to adjust
            got = sorted((x for x in f["splits"] if near(x["date"], date) and (s, x["date"]) not in matched), key=lambda x: x["date"])
            # A cut and a site split both in the window: the one closer to
            # Alpaca's date is the event (a cut on a tie); the other stays
            # unmatched and is reported on its own.
            if got and cuts and min(cuts) <= got[0]["date"]:
                got = []
            if got:
                matched.add((s, got[0]["date"]))
                if abs(got[0]["new"] / got[0]["old"] - new / old) / (new / old) < 0.02:
                    adjusted += 1
                    late += got[0]["date"] != date
                else:
                    wrong_ratio += 1
                    wrong_list.append(f"{s} {date}: site {got[0]['new']}:{got[0]['old']} on {got[0]['date']}, Alpaca {new:g}:{old:g}")
            elif is_cut:
                cut += 1
            elif date == first_bar:
                continue  # the history starts on the split day: nothing to adjust
            else:
                missed += 1
                missed_list.append(f"{s} {date} ({new:g}:{old:g})")
    for s, f in ok.items():
        for x in f["splits"]:
            if (s, x["date"]) not in matched:
                extra_splits.append(f"{s} {x['date']} {x['new']}:{x['old']} ({x['how']})")
    breaks = {}
    for f in ok.values():
        for b in f["breaks"]:
            breaks[b["kind"]] = breaks.get(b["kind"], 0) + 1

    out += ["", "## Splits", "",
            f"Splits Alpaca lists for covered symbols inside their served history: {adjusted + wrong_ratio + cut + missed}.", "",
            f"- adjusted with the right ratio: {adjusted} ({late} of them a few days after Alpaca's date, on the first IEX round-lot price at the new level)",
            f"- adjusted with a different ratio: {wrong_ratio}",
            f"- history cut there instead (fail closed): {cut}",
            f"- missed (history runs across an unadjusted split): {missed}",
            f"- splits the site made that Alpaca does not list (false adjustments): {len(extra_splits)}", "",
            f"Breaks in served histories: {', '.join(f'{k} {v}' for k, v in sorted(breaks.items())) or 'none'}.", ""]
    for title, items in (("Different ratio", wrong_list), ("Missed", missed_list), ("Not in Alpaca's list", extra_splits)):
        if items:
            out += [f"### {title}", ""] + [f"- {x}" for x in sorted(items)] + [""]
    print("\n".join(out))


if __name__ == "__main__":
    main()
