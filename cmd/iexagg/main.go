// Command iexagg reads one day of IEX TOPS 1.6 (an uncompressed pcapng
// stream on stdin) and writes that day's per-symbol summary (JSON lines) to
// stdout. Exit 4: IEX's file does not cover the whole regular session, so
// the day is skipped rather than archived.
//
//	curl -sf "$LINK" | gzip -dc | iexagg 2026-10-09 | gzip > 20261009.jsonl.gz
package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"

	"github.com/vibistic-hq/vibistic-iex-data/internal/day"
	"github.com/vibistic-hq/vibistic-iex-data/internal/tops"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: iexagg YYYY-MM-DD < tops.pcapng > day.jsonl")
		os.Exit(2)
	}
	d, err := tops.Parse(bufio.NewReaderSize(os.Stdin, 1<<22))
	if err != nil {
		fmt.Fprintln(os.Stderr, "iexagg:", err)
		os.Exit(1)
	}
	if err := day.SessionTimes(os.Args[1], d.RegularStart, d.RegularEnd); err != nil {
		fmt.Fprintln(os.Stderr, "iexagg:", err)
		if errors.Is(err, day.ErrIncomplete) {
			os.Exit(4)
		}
		os.Exit(1)
	}
	if err := day.Write(os.Stdout, os.Args[1], d); err != nil {
		fmt.Fprintln(os.Stderr, "iexagg:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "iexagg: %s trades=%d breaks=%d symbols=%d\n", os.Args[1], d.Trades, d.Breaks, len(d.Symbols))
}
