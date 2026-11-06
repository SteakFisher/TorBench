package brokerage

import (
	"math/rand/v2"

	"torbench/internal/ledger"
)

// Kind is an operation type.
type Kind string

const (
	Deposit   Kind = "deposit"
	Withdraw  Kind = "withdraw"
	Buy       Kind = "buy"
	Sell      Kind = "sell"
	Overdraft Kind = "overdraft" // a buy far beyond the user's cash, which must be rejected
	Dividend  Kind = "dividend"
	Balance   Kind = "read_balance"
	Statement Kind = "read_statement"
)

// IsRead reports whether k reads instead of writing.
func (k Kind) IsRead() bool { return k == Balance || k == Statement }

// writeMix is the share of each write kind, in percent.
var writeMix = []struct {
	kind Kind
	pct  int
}{{Deposit, 10}, {Withdraw, 5}, {Buy, 43}, {Sell, 35}, {Overdraft, 5}, {Dividend, 2}}

// dividendPayees is how many users one dividend transaction pays.
const dividendPayees = 50

// Op is one operation. Tx is set for writes; User is the user a read is about.
type Op struct {
	Kind Kind
	Tx   ledger.Tx
	User int
}

// Gen produces a repeatable operation stream from Config.Seed. It is not safe for concurrent use.
type Gen struct {
	c    Config
	r    *rand.Rand
	zipf *rand.Zipf
	ids  *ledger.IDs
}

// NewGen returns a generator for c that takes transaction ids from ids.
func NewGen(c Config, ids *ledger.IDs) *Gen {
	r := rand.New(rand.NewPCG(c.Seed, c.Seed^0x9e3779b97f4a7c15))
	g := &Gen{c: c, r: r, ids: ids}
	if c.Zipf > 1 {
		g.zipf = rand.NewZipf(r, c.Zipf, 1, uint64(c.Users-1))
	}
	return g
}

// Next returns the next operation; with writesOnly it never returns a read.
func (g *Gen) Next(writesOnly bool) Op {
	if !writesOnly && g.r.Float64() < g.c.Reads {
		kind := Balance
		if g.r.IntN(2) == 0 {
			kind = Statement
		}
		return Op{Kind: kind, User: g.user()}
	}
	n := g.r.IntN(100)
	for _, m := range writeMix {
		if n < m.pct {
			return g.write(m.kind)
		}
		n -= m.pct
	}
	panic("writeMix does not sum to 100")
}

// user picks a user, skewed by Zipf when set.
func (g *Gen) user() int {
	if g.zipf != nil {
		return int(g.zipf.Uint64())
	}
	return g.r.IntN(g.c.Users)
}

// between returns a random value in [lo, hi].
func (g *Gen) between(lo, hi int64) int64 { return lo + g.r.Int64N(hi-lo+1) }

// write builds a write operation of kind k.
func (g *Gen) write(k Kind) Op {
	u, shard, ticker := g.user(), g.r.IntN(g.c.Shards), Ticker(g.r.IntN(g.c.Tickers))
	var ps []ledger.Posting
	switch k {
	case Deposit:
		ps = []ledger.Posting{move(Bank(shard), Cash(u), USD, g.between(100, 5_000)*cents)}
	case Withdraw:
		ps = []ledger.Posting{move(Cash(u), Bank(shard), USD, g.between(1, 500)*cents)}
	case Buy, Overdraft:
		qty, price := g.between(1, 20), g.between(10, 500)*cents
		if k == Overdraft {
			qty = 1_000_000_000
		}
		cost := qty * price
		ps = []ledger.Posting{
			move(Cash(u), Clearing(shard), USD, cost),
			move(Clearing(shard), Shares(u), ticker, qty*shareUnits),
			move(Cash(u), Fees(shard), USD, fee(cost)),
		}
	case Sell:
		qty, price := g.between(1, 5), g.between(10, 500)*cents
		proceeds := qty * price
		ps = []ledger.Posting{
			move(Shares(u), Clearing(shard), ticker, qty*shareUnits),
			move(Clearing(shard), Cash(u), USD, proceeds),
			move(Cash(u), Fees(shard), USD, fee(proceeds)),
		}
	case Dividend:
		perShare := g.between(1, 100)
		for range dividendPayees {
			ps = append(ps, move(Bank(shard), Cash(g.user()), USD, perShare*g.between(1, 50)))
		}
	}
	return Op{Kind: k, User: u, Tx: ledger.Tx{ID: g.ids.Next(len(ps)), Postings: ps}}
}

// fee is 0.1% of amount, at least one dollar.
func fee(amount int64) int64 { return max(amount/1000, cents) }

// move returns a posting of amount of asset from src to dst.
func move(src, dst, asset string, amount int64) ledger.Posting {
	return ledger.Posting{Source: src, Destination: dst, Asset: asset, Amount: amount}
}
