package load

import (
	"context"
	"errors"
	"sync"
	"time"

	"torbench/internal/brokerage"
	"torbench/internal/ledger"
)

// Config picks the load mode. A positive Rate runs open loop: requests start on a fixed schedule and
// latency counts from the scheduled start, so queueing shows up. Otherwise Concurrency workers send
// requests back to back. Batch is the number of writes per request; above 1 the run has no reads.
type Config struct {
	Concurrency int
	Rate        float64
	Duration    time.Duration
	Batch       int
}

// Counts tallies the outcomes of one operation kind.
type Counts struct {
	OK       int `json:"ok"`
	Rejected int `json:"rejected"`
	Unknown  int `json:"unknown"`
}

// Result is what one run measured.
type Result struct {
	Elapsed time.Duration              `json:"elapsed_ns"`
	Latency map[string]Latency         `json:"latency"`
	Ops     map[brokerage.Kind]*Counts `json:"ops"`
	// AppliedOverdrafts counts overdraft buys the ledger accepted; it must be 0.
	AppliedOverdrafts int `json:"applied_overdrafts"`
	// Unknown holds writes whose outcome the run could not tell.
	Unknown []ledger.Tx `json:"-"`
}

// runner holds a run's shared state.
type runner struct {
	l    ledger.Ledger
	cfg  Config
	book *brokerage.Book

	genMu sync.Mutex
	gen   *brokerage.Gen

	mu      sync.Mutex
	samples map[string][]time.Duration
	errs    map[string]int
	res     Result
}

// Run drives l for cfg.Duration with operations from gen, recording applied writes in book.
func Run(ctx context.Context, l ledger.Ledger, gen *brokerage.Gen, cfg Config, book *brokerage.Book) Result {
	r := &runner{
		l: l, cfg: cfg, book: book, gen: gen,
		samples: map[string][]time.Duration{}, errs: map[string]int{},
		res: Result{Ops: map[brokerage.Kind]*Counts{}},
	}
	start := time.Now()
	var wg sync.WaitGroup
	if cfg.Rate > 0 {
		interval := time.Duration(float64(time.Second) / cfg.Rate)
		for i := 0; ; i++ {
			at := start.Add(time.Duration(i) * interval)
			if at.Sub(start) >= cfg.Duration {
				break
			}
			time.Sleep(time.Until(at))
			ops := r.take()
			wg.Go(func() { r.do(ctx, ops, at) })
		}
	} else {
		deadline := start.Add(cfg.Duration)
		for range cfg.Concurrency {
			wg.Go(func() {
				for time.Now().Before(deadline) {
					r.do(ctx, r.take(), time.Now())
				}
			})
		}
	}
	wg.Wait()
	r.res.Elapsed = time.Since(start)
	r.res.Latency = map[string]Latency{}
	for k, ds := range r.samples {
		r.res.Latency[k] = summarise(ds, r.errs[k])
	}
	return r.res
}

// take returns the operations for one request.
func (r *runner) take() []brokerage.Op {
	r.genMu.Lock()
	defer r.genMu.Unlock()
	ops := make([]brokerage.Op, max(r.cfg.Batch, 1))
	for i := range ops {
		ops[i] = r.gen.Next(r.cfg.Batch > 1)
	}
	return ops
}

// do sends one request and records it, timing from start.
func (r *runner) do(ctx context.Context, ops []brokerage.Op, start time.Time) {
	label := string(ops[0].Kind)
	if r.cfg.Batch > 1 {
		label = "batch"
	}
	if ops[0].Kind.IsRead() {
		var err error
		if ops[0].Kind == brokerage.Balance {
			_, err = r.l.Balances(ctx, brokerage.Cash(ops[0].User))
		} else {
			err = r.l.Statement(ctx, brokerage.User(ops[0].User))
		}
		r.record(label, time.Since(start), err != nil)
		return
	}
	txs := make([]ledger.Tx, len(ops))
	for i, op := range ops {
		txs[i] = op.Tx
	}
	results, err := r.l.Submit(ctx, txs)
	r.record(label, time.Since(start), err != nil)
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, op := range ops {
		c := r.res.Ops[op.Kind]
		if c == nil {
			c = &Counts{}
			r.res.Ops[op.Kind] = c
		}
		switch {
		case err != nil || (results[i] != nil && !errors.Is(results[i], ledger.ErrRejected)):
			c.Unknown++
			r.res.Unknown = append(r.res.Unknown, op.Tx)
		case results[i] == nil:
			c.OK++
			r.book.Apply(op.Tx)
			if op.Kind == brokerage.Overdraft {
				r.res.AppliedOverdrafts++
			}
		default:
			c.Rejected++
		}
	}
}

// record adds one request's latency.
func (r *runner) record(label string, d time.Duration, failed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.samples[label] = append(r.samples[label], d)
	if failed {
		r.errs[label]++
	}
}

// Resolve resubmits each write of unknown outcome alone with its original ids, up to three times, and
// records the applied ones in book. Idempotency makes a write that was already applied come back as
// applied. It returns how many stayed unknown.
func Resolve(ctx context.Context, l ledger.Ledger, unknown []ledger.Tx, book *brokerage.Book) int {
	left := 0
	for _, tx := range unknown {
		resolved := false
		for range 3 {
			results, err := l.Submit(ctx, []ledger.Tx{tx})
			if err != nil || (results[0] != nil && !errors.Is(results[0], ledger.ErrRejected)) {
				continue
			}
			if results[0] == nil {
				book.Apply(tx)
			}
			resolved = true
			break
		}
		if !resolved {
			left++
		}
	}
	return left
}
