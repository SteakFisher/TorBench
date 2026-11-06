// Package ledger drives Torment and Formance through one interface.
package ledger

import (
	"context"
	"errors"
	"math/big"
	"sync/atomic"
	"time"
)

// ErrRejected marks a transaction the ledger refused for insufficient funds. Nothing in it was applied.
var ErrRejected = errors.New("rejected: insufficient funds")

// Ledger is one system under test.
type Ledger interface {
	// Setup registers assets and accounts. It is idempotent.
	Setup(ctx context.Context, assets []Asset, accounts []Account) error
	// Submit applies each transaction atomically and independently of the others, in one request. The
	// result for txs[i] is nil, ErrRejected or another error; err is set when the request as a whole
	// failed and the outcome of every transaction is unknown.
	Submit(ctx context.Context, txs []Tx) (results []error, err error)
	// Balances returns the account's balance per asset name. An unknown account has none.
	Balances(ctx context.Context, address string) (map[string]int64, error)
	// Statement reads the latest transfers of every account under prefix, such as "user:12".
	Statement(ctx context.Context, prefix string) error
}

// Asset is a currency or ticker, with Precision decimal places in its minor unit.
type Asset struct {
	Name      string
	Precision int
}

// Account is an account to register. Type is a Torment account type; Assets lists wallets created up
// front.
type Account struct {
	Address       string
	Type          string
	AllowNegative bool
	Assets        []string
}

// Posting moves Amount minor units of Asset from Source to Destination.
type Posting struct {
	Source, Destination string
	Asset               string
	Amount              int64
}

// Tx is an atomic set of postings. Posting i has transfer id ID+i, and ID is the idempotency key.
type Tx struct {
	ID       ID
	Postings []Posting
}

// ID is a 128-bit id.
type ID struct{ Hi, Lo uint64 }

// Add returns id+n. Lo does not overflow within a run.
func (id ID) Add(n uint64) ID { return ID{id.Hi, id.Lo + n} }

// String returns id in base 10.
func (id ID) String() string {
	n := new(big.Int).SetUint64(id.Hi)
	n.Lsh(n, 64)
	return n.Or(n, new(big.Int).SetUint64(id.Lo)).String()
}

// IDs hands out strictly increasing ids: Hi is the creation time in milliseconds and Lo a counter, so
// ids from a later process are larger.
type IDs struct {
	hi uint64
	lo atomic.Uint64
}

// NewIDs returns an id source starting at the current time.
func NewIDs() *IDs { return &IDs{hi: uint64(time.Now().UnixMilli())} }

// Next reserves n consecutive ids and returns the first.
func (s *IDs) Next(n int) ID { return ID{s.hi, s.lo.Add(uint64(n)) - uint64(n) + 1} }
