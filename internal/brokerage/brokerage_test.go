package brokerage

import (
	"reflect"
	"testing"

	"torbench/internal/ledger"
)

func TestGenIsRepeatable(t *testing.T) {
	c := Config{Users: 100, Tickers: 3, Shards: 2, Zipf: 1.1, Reads: 0.2, Seed: 7}
	a, b := NewGen(c, &ledger.IDs{}), NewGen(c, &ledger.IDs{})
	for i := range 1000 {
		if x, y := a.Next(false), b.Next(false); !reflect.DeepEqual(x, y) {
			t.Fatalf("op %d differs: %+v vs %+v", i, x, y)
		}
	}
}

func TestGenTxsBalance(t *testing.T) {
	g := NewGen(Config{Users: 10, Tickers: 2, Shards: 1, Seed: 1}, &ledger.IDs{})
	book := NewBook()
	for range 1000 {
		book.Apply(g.Next(true).Tx)
	}
	sums := map[string]int64{}
	for _, m := range book.b {
		for a, v := range m {
			sums[a] += v
		}
	}
	for a, s := range sums {
		if s != 0 {
			t.Errorf("%s sums to %d", a, s)
		}
	}
}

func TestCheck(t *testing.T) {
	tx := ledger.Tx{Postings: []ledger.Posting{move(World, Cash(0), USD, 5)}}
	before := Balances{World: {USD: -10}, Cash(0): {USD: 10}}
	good := Balances{World: {USD: -15}, Cash(0): {USD: 15}}
	book := NewBook()
	book.Apply(tx)
	if p := Check(before, good, book); len(p) != 0 {
		t.Fatalf("good snapshot: %v", p)
	}
	bad := Balances{World: {USD: -15}, Cash(0): {USD: 14}}
	if p := Check(before, bad, book); len(p) != 2 { // the cash mismatch and the USD sum
		t.Fatalf("bad snapshot: want 2 problems, got %v", p)
	}
	neg := Balances{World: {USD: 5}, Cash(0): {USD: -5}}
	if p := Check(neg, neg, NewBook()); len(p) != 1 {
		t.Fatalf("negative cash: want 1 problem, got %v", p)
	}
}

func TestIDString(t *testing.T) {
	if got := (ledger.ID{Hi: 1, Lo: 2}).String(); got != "18446744073709551618" {
		t.Fatalf("got %s", got)
	}
}
