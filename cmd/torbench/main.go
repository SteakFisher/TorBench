// Command torbench seeds a brokerage into Torment or Formance, runs load against it, and reports results.
//
//	torbench seed   -system torment|formance [flags]
//	torbench run    -system torment|formance [flags]
//	torbench report results/*.json > report.html
package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"torbench/internal/brokerage"
	"torbench/internal/ledger"
	"torbench/internal/load"
	"torbench/internal/report"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: torbench seed|run|report [flags]")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var err error
	switch os.Args[1] {
	case "seed":
		err = seed(ctx, os.Args[2:])
	case "run":
		err = run(ctx, os.Args[2:])
	case "report":
		err = report.Write(os.Stdout, os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		slog.Error("torbench failed", "err", err)
		os.Exit(1)
	}
}

// target holds the flags that pick and size the system under test.
type target struct {
	system, url, features, apiKey string
	cfg                           brokerage.Config
}

// register adds the target flags to fs.
func (t *target) register(fs *flag.FlagSet) {
	fs.StringVar(&t.system, "system", "", "torment or formance")
	fs.StringVar(&t.url, "url", "", "server URL (default http://localhost:8088 for torment, :3068 for formance)")
	fs.StringVar(&t.features, "features", "default", "formance feature set: default or minimal")
	fs.StringVar(&t.apiKey, "api-key", "bench-key", "torment API key")
	fs.IntVar(&t.cfg.Users, "users", 10_000, "number of users")
	fs.IntVar(&t.cfg.Tickers, "tickers", 20, "number of tickers")
}

// ledger returns the adapter for the target.
func (t *target) ledger() (ledger.Ledger, error) {
	switch t.system {
	case "torment":
		return ledger.NewTorment(cmp.Or(t.url, "http://localhost:8088"), t.apiKey), nil
	case "formance":
		return ledger.NewFormance(cmp.Or(t.url, "http://localhost:3068"), t.features, t.cfg.Assets())
	}
	return nil, fmt.Errorf("-system must be torment or formance, not %q", t.system)
}

// label names the system and, for Formance, its feature set.
func (t *target) label() string {
	if t.system == "formance" {
		return "formance/" + t.features
	}
	return t.system
}

// seed registers the brokerage and funds it.
func seed(ctx context.Context, args []string) error {
	var t target
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	t.register(fs)
	fs.Parse(args)
	t.cfg.Shards = 1
	if err := t.cfg.Validate(); err != nil {
		return err
	}
	l, err := t.ledger()
	if err != nil {
		return err
	}
	start := time.Now()
	if err := l.Setup(ctx, t.cfg.Assets(), t.cfg.Accounts()); err != nil {
		return err
	}
	slog.Info("registered", "accounts", len(t.cfg.Accounts()), "took", time.Since(start).Round(time.Millisecond))
	txs := t.cfg.Funding(ledger.NewIDs())
	for i := 0; i < len(txs); i += 500 {
		batch := txs[i:min(i+500, len(txs))]
		results, err := l.Submit(ctx, batch)
		if err != nil {
			return err
		}
		for j, r := range results {
			if r != nil {
				return fmt.Errorf("error funding %v: %w", batch[j].Postings, r)
			}
		}
	}
	slog.Info("funded", "transactions", len(txs), "took", time.Since(start).Round(time.Millisecond))
	return nil
}

// run drives one load step, checks the ledger and writes the result.
func run(ctx context.Context, args []string) error {
	var (
		t     target
		lc    load.Config
		check bool
		out   string
	)
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	t.register(fs)
	fs.IntVar(&t.cfg.Shards, "shards", 1, "clearing and fee account pairs trades spread over (1 = one hot account)")
	fs.Float64Var(&t.cfg.Zipf, "zipf", 1.1, "user skew: 0 uniform, above 1 skewed")
	fs.Float64Var(&t.cfg.Reads, "reads", 0.1, "fraction of operations that are reads")
	fs.Uint64Var(&t.cfg.Seed, "seed", 1, "operation stream seed")
	fs.IntVar(&lc.Concurrency, "concurrency", 64, "closed loop: concurrent workers")
	fs.Float64Var(&lc.Rate, "rate", 0, "open loop: requests per second (0 = closed loop)")
	fs.DurationVar(&lc.Duration, "duration", 5*time.Minute, "how long to send load")
	fs.IntVar(&lc.Batch, "batch", 1, "writes per request")
	fs.BoolVar(&check, "check", true, "verify balances after the run")
	fs.StringVar(&out, "out", "", "result file (default results/<label>-<time>.json)")
	fs.Parse(args)
	if lc.Batch > 1 {
		t.cfg.Reads = 0
	}
	if err := t.cfg.Validate(); err != nil {
		return err
	}
	l, err := t.ledger()
	if err != nil {
		return err
	}

	var before brokerage.Balances
	if check {
		if before, err = brokerage.Snapshot(ctx, l, t.cfg); err != nil {
			return err
		}
	}
	book := brokerage.NewBook()
	slog.Info("running", "system", t.label(), "rate", lc.Rate, "concurrency", lc.Concurrency, "batch", lc.Batch, "shards", t.cfg.Shards, "duration", lc.Duration)
	res := load.Run(ctx, l, brokerage.NewGen(t.cfg, ledger.NewIDs()), lc, book)
	rep := report.Run{System: t.system, Label: t.label(), Brokerage: t.cfg, Load: lc, Result: res, Checked: check}
	rep.Unresolved = load.Resolve(ctx, l, res.Unknown, book)
	if check {
		after, err := brokerage.Snapshot(ctx, l, t.cfg)
		if err != nil {
			return err
		}
		rep.Problems = brokerage.Check(before, after, book)
	}
	if res.AppliedOverdrafts > 0 {
		rep.Problems = append(rep.Problems, fmt.Sprintf("%d overdraft buys were applied", res.AppliedOverdrafts))
	}
	if rep.Unresolved > 0 {
		rep.Problems = append(rep.Problems, fmt.Sprintf("%d writes have an unknown outcome", rep.Unresolved))
	}
	rep.Summarise()

	if out == "" {
		out = filepath.Join("results", fmt.Sprintf("%s-%s.json", strings.ReplaceAll(t.label(), "/", "-"), time.Now().Format("20060102-150405")))
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return fmt.Errorf("error creating results directory: %w", err)
	}
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return fmt.Errorf("error encoding result: %w", err)
	}
	if err := os.WriteFile(out, b, 0o644); err != nil {
		return fmt.Errorf("error writing result: %w", err)
	}
	slog.Info("done", "out", out, "applied_per_sec", int(rep.AppliedPerSec), "write_p99_ms", rep.WriteP99, "problems", len(rep.Problems))
	for _, p := range rep.Problems[:min(len(rep.Problems), 20)] {
		slog.Warn("check", "problem", p)
	}
	if len(rep.Problems) > 0 {
		return errors.New("correctness check failed")
	}
	return nil
}
