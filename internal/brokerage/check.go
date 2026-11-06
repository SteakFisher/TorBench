package brokerage

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"golang.org/x/sync/errgroup"

	"torbench/internal/ledger"
)

// Balances holds balances per account address and asset.
type Balances map[string]map[string]int64

// Book sums the changes of applied transactions per account and asset. It is safe for concurrent use.
type Book struct {
	mu sync.Mutex
	b  Balances
}

// NewBook returns an empty Book.
func NewBook() *Book { return &Book{b: Balances{}} }

// Apply records tx: each posting debits its source and credits its destination.
func (k *Book) Apply(tx ledger.Tx) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, p := range tx.Postings {
		k.add(p.Source, p.Asset, -p.Amount)
		k.add(p.Destination, p.Asset, p.Amount)
	}
}

// add changes one balance; the caller holds k.mu.
func (k *Book) add(address, asset string, delta int64) {
	if k.b[address] == nil {
		k.b[address] = map[string]int64{}
	}
	k.b[address][asset] += delta
}

// Snapshot reads the balances of every account in c, 64 at a time.
func Snapshot(ctx context.Context, l ledger.Ledger, c Config) (Balances, error) {
	accounts := c.Accounts()
	out := make([]map[string]int64, len(accounts))
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(64)
	for i, a := range accounts {
		g.Go(func() (err error) {
			out[i], err = l.Balances(ctx, a.Address)
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	b := make(Balances, len(accounts))
	for i, a := range accounts {
		b[a.Address] = out[i]
	}
	return b, nil
}

// Check compares two snapshots against the book. It returns one line per problem: an account whose
// balance moved differently from the book, an account other than world below zero, or an asset whose
// balances do not sum to zero.
func Check(before, after Balances, book *Book) []string {
	book.mu.Lock()
	defer book.mu.Unlock()
	var problems []string
	sums := map[string]int64{}
	for _, addr := range slices.Sorted(maps.Keys(after)) {
		assets := map[string]bool{}
		for a := range after[addr] {
			assets[a] = true
		}
		for a := range before[addr] {
			assets[a] = true
		}
		for a := range book.b[addr] {
			assets[a] = true
		}
		for _, a := range slices.Sorted(maps.Keys(assets)) {
			sums[a] += after[addr][a]
			if addr != World && after[addr][a] < 0 {
				problems = append(problems, fmt.Sprintf("%s %s is negative: %d", addr, a, after[addr][a]))
			}
			if got, want := after[addr][a]-before[addr][a], book.b[addr][a]; got != want {
				problems = append(problems, fmt.Sprintf("%s %s moved %d, want %d", addr, a, got, want))
			}
		}
	}
	for addr := range book.b {
		if _, ok := after[addr]; !ok {
			problems = append(problems, fmt.Sprintf("%s is in the book but was not read", addr))
		}
	}
	for _, a := range slices.Sorted(maps.Keys(sums)) {
		if sums[a] != 0 {
			problems = append(problems, fmt.Sprintf("%s sums to %d, want 0", a, sums[a]))
		}
	}
	return problems
}
