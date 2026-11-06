// Package brokerage models a stock brokerage on a ledger: its accounts, its funding, and a seeded stream
// of operations.
package brokerage

import (
	"fmt"

	"torbench/internal/ledger"
)

// MaxShards is how many bank, clearing and fee account sets are registered. A run uses the first
// Config.Shards.
const MaxShards = 256

// Every account is credit-normal on Torment, so a balance means credits minus debits, the same as
// Formance's input minus output.
const (
	World      = "world"
	USD        = "USD"
	cents      = 100
	shareUnits = 1_000_000 // tickers have precision 6, for fractional shares

	userCash      = 10_000 * cents          // each user's opening cash
	clearingCash  = 1_000_000_000 * cents   // each clearing shard's opening cash
	clearingStock = 1_000_000 * shareUnits  // each clearing shard's opening stock per ticker
	bankCash      = 100_000_000_000 * cents // each bank shard's opening cash
)

// Config sizes the brokerage and shapes its traffic.
type Config struct {
	Users   int
	Tickers int
	// Shards is how many bank, clearing and fee account sets writes spread over; 1 means every write
	// shares the same bank, clearing and fee accounts.
	Shards int
	// Zipf skews which users act: 0 is uniform, larger values concentrate activity on fewer users. It
	// must be 0 or above 1.
	Zipf float64
	// Reads is the fraction of operations that are reads.
	Reads float64
	Seed  uint64
}

// Validate reports a Config the generator cannot use.
func (c Config) Validate() error {
	switch {
	case c.Users < 1 || c.Tickers < 1:
		return fmt.Errorf("users and tickers must be positive")
	case c.Shards < 1 || c.Shards > MaxShards:
		return fmt.Errorf("shards must be between 1 and %d", MaxShards)
	case c.Zipf != 0 && c.Zipf <= 1:
		return fmt.Errorf("zipf must be 0 or above 1")
	case c.Reads < 0 || c.Reads > 1:
		return fmt.Errorf("reads must be between 0 and 1")
	}
	return nil
}

// Ticker returns the name of ticker i.
func Ticker(i int) string { return fmt.Sprintf("TK%02d", i) }

// Cash, Shares, User, Bank, Clearing and Fees return account addresses.
func Cash(user int) string      { return fmt.Sprintf("user:%d:cash", user) }
func Shares(user int) string    { return fmt.Sprintf("user:%d:shares", user) }
func User(user int) string      { return fmt.Sprintf("user:%d", user) }
func Bank(shard int) string     { return fmt.Sprintf("bank:%d", shard) }
func Clearing(shard int) string { return fmt.Sprintf("broker:clearing:%d", shard) }
func Fees(shard int) string     { return fmt.Sprintf("broker:fees:%d", shard) }

// Assets returns USD and every ticker.
func (c Config) Assets() []ledger.Asset {
	assets := []ledger.Asset{{Name: USD, Precision: 2}}
	for i := range c.Tickers {
		assets = append(assets, ledger.Asset{Name: Ticker(i), Precision: 6})
	}
	return assets
}

// Accounts returns every account. Share accounts start with no wallets; transfers create them.
func (c Config) Accounts() []ledger.Account {
	all := make([]string, 0, c.Tickers+1)
	for _, a := range c.Assets() {
		all = append(all, a.Name)
	}
	accounts := []ledger.Account{{Address: World, Type: "equity", AllowNegative: true, Assets: all}}
	for s := range MaxShards {
		accounts = append(accounts,
			ledger.Account{Address: Bank(s), Type: "liability", Assets: []string{USD}},
			ledger.Account{Address: Clearing(s), Type: "liability", Assets: all},
			ledger.Account{Address: Fees(s), Type: "revenue", Assets: []string{USD}},
		)
	}
	for u := range c.Users {
		accounts = append(accounts,
			ledger.Account{Address: Cash(u), Type: "liability", Assets: []string{USD}},
			ledger.Account{Address: Shares(u), Type: "liability"},
		)
	}
	return accounts
}

// Funding returns the transactions that give each user cash, each bank shard cash, and each clearing
// shard cash and stock. They are the only writes that touch world.
func (c Config) Funding(ids *ledger.IDs) []ledger.Tx {
	var txs []ledger.Tx
	one := func(dst, asset string, amount int64) {
		txs = append(txs, ledger.Tx{ID: ids.Next(1), Postings: []ledger.Posting{move(World, dst, asset, amount)}})
	}
	for s := range MaxShards {
		one(Bank(s), USD, bankCash)
		one(Clearing(s), USD, clearingCash)
		for t := range c.Tickers {
			one(Clearing(s), Ticker(t), clearingStock)
		}
	}
	for u := range c.Users {
		one(Cash(u), USD, userCash)
	}
	return txs
}
