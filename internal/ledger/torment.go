package ledger

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"
)

// Torment talks to a Torment server.
type Torment struct{ c client }

// NewTorment returns an adapter for the Torment server at url, authenticating with apiKey.
func NewTorment(url, apiKey string) *Torment {
	return &Torment{client{base: strings.TrimRight(url, "/"), header: http.Header{"Authorization": {"Bearer " + apiKey}}}}
}

// Setup registers each asset as a currency and each account, 32 accounts at a time.
func (t *Torment) Setup(ctx context.Context, assets []Asset, accounts []Account) error {
	for _, a := range assets {
		var out map[string]any
		status, err := t.c.do(ctx, "POST", "/currencies", nil, map[string]string{"code": fmt.Sprintf("%s/%d", a.Name, a.Precision)}, &out)
		if err != nil {
			return err
		}
		if status != http.StatusCreated && status != http.StatusOK {
			return unexpected("registering currency "+a.Name, status, out)
		}
	}
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(32)
	for _, a := range accounts {
		g.Go(func() error {
			var out map[string]any
			status, err := t.c.do(ctx, "POST", "/accounts", nil, map[string]any{
				"address": a.Address, "type": a.Type, "allow_negative": a.AllowNegative, "currencies": nonNil(a.Assets),
			}, &out)
			if err != nil {
				return err
			}
			if status != http.StatusCreated && status != http.StatusOK {
				return unexpected("registering account "+a.Address, status, out)
			}
			return nil
		})
	}
	return g.Wait()
}

// tormentPosting is one posting in a Torment transfer request.
type tormentPosting struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Currency    string `json:"currency"`
	Amount      string `json:"amount"`
}

// Submit sends every transaction as one chain in a single POST /transfers.
func (t *Torment) Submit(ctx context.Context, txs []Tx) ([]error, error) {
	chains := make([][]tormentPosting, len(txs))
	for i, tx := range txs {
		chains[i] = make([]tormentPosting, len(tx.Postings))
		for j, p := range tx.Postings {
			chains[i][j] = tormentPosting{
				ID: tx.ID.Add(uint64(j)).String(), Source: p.Source, Destination: p.Destination,
				Currency: p.Asset, Amount: strconv.FormatInt(p.Amount, 10),
			}
		}
	}
	var out struct {
		Transfers [][]struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"transfers"`
		Error string `json:"error"`
	}
	status, err := t.c.do(ctx, "POST", "/transfers", nil, map[string]any{"postings": chains}, &out)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK || len(out.Transfers) != len(txs) {
		return nil, unexpected("posting transfers", status, out.Error)
	}
	results := make([]error, len(txs))
	for i, chain := range out.Transfers {
		for _, tr := range chain {
			switch tr.Error {
			case "", "chain_failed":
			// id_already_failed: a retry of a posting TigerBeetle already rejected
			case "insufficient_funds", "id_already_failed":
				results[i] = ErrRejected
			default:
				results[i] = fmt.Errorf("error posting transfer: %s", tr.Error)
			}
			if results[i] != nil {
				break
			}
		}
	}
	return results, nil
}

// Balances returns the account's wallet balances.
func (t *Torment) Balances(ctx context.Context, address string) (map[string]int64, error) {
	var out struct {
		Balances []struct {
			Currency string `json:"currency"`
			Amount   string `json:"amount"`
		} `json:"balances"`
		Error string `json:"error"`
	}
	status, err := t.c.do(ctx, "GET", "/accounts/"+address+"/balances", nil, nil, &out)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return map[string]int64{}, nil
	}
	if status != http.StatusOK {
		return nil, unexpected("reading balances of "+address, status, out.Error)
	}
	m := make(map[string]int64, len(out.Balances))
	for _, b := range out.Balances {
		n, err := strconv.ParseInt(b.Amount, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("error parsing balance of %s: %w", address, err)
		}
		m[b.Currency] = n
	}
	return m, nil
}

// Statement reads the latest 20 transfers of the accounts matching prefix:*.
func (t *Torment) Statement(ctx context.Context, prefix string) error {
	var out map[string]any
	status, err := t.c.do(ctx, "GET", "/accounts/"+prefix+":*/transfers?limit=20", nil, nil, &out)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return unexpected("reading statement of "+prefix, status, out)
	}
	return nil
}

// nonNil returns s, or an empty slice when s is nil, so it encodes as [].
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
