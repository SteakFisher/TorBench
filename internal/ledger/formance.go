package ledger

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// FormanceFeatures maps a name to the feature set a Formance ledger is created with. "default" is an
// empty set, which Formance fills with its defaults.
var FormanceFeatures = map[string]map[string]string{
	"default": {},
	"minimal": {
		"MOVES_HISTORY": "OFF",
		"MOVES_HISTORY_POST_COMMIT_EFFECTIVE_VOLUMES": "DISABLED",
		"HASH_LOGS":                    "DISABLED",
		"ACCOUNT_METADATA_HISTORY":     "DISABLED",
		"TRANSACTION_METADATA_HISTORY": "DISABLED",
	},
}

// Formance talks to one ledger on a Formance ledger v2 server.
type Formance struct {
	c        client
	features string
	// code maps an asset name to Formance's NAME/precision code
	code map[string]string
}

// NewFormance returns an adapter for the ledger "bench_<features>" on the server at url.
func NewFormance(url, features string, assets []Asset) (*Formance, error) {
	if _, ok := FormanceFeatures[features]; !ok {
		return nil, fmt.Errorf("unknown formance feature set %q", features)
	}
	code := make(map[string]string, len(assets))
	for _, a := range assets {
		code[a.Name] = fmt.Sprintf("%s/%d", a.Name, a.Precision)
	}
	base := strings.TrimRight(url, "/") + "/v2/bench_" + features
	return &Formance{c: client{base: base}, features: features, code: code}, nil
}

// formanceError is Formance's error body.
type formanceError struct {
	ErrorCode    string `json:"errorCode"`
	ErrorMessage string `json:"errorMessage"`
}

// Setup creates the ledger with its feature set. Formance creates accounts on first use, so accounts
// are not registered.
func (f *Formance) Setup(ctx context.Context, _ []Asset, _ []Account) error {
	var out formanceError
	status, err := f.c.do(ctx, "POST", "", nil, map[string]any{"features": FormanceFeatures[f.features]}, &out)
	if err != nil {
		return err
	}
	if status != http.StatusNoContent && out.ErrorCode != "LEDGER_ALREADY_EXISTS" {
		return unexpected("creating ledger", status, out)
	}
	return nil
}

// formancePosting is one posting in a Formance transaction.
type formancePosting struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Asset       string `json:"asset"`
	Amount      int64  `json:"amount"`
}

// body returns tx as a Formance transaction body.
func (f *Formance) body(tx Tx) map[string]any {
	ps := make([]formancePosting, len(tx.Postings))
	for i, p := range tx.Postings {
		ps[i] = formancePosting{p.Source, p.Destination, f.code[p.Asset], p.Amount}
	}
	return map[string]any{"postings": ps, "metadata": map[string]string{}}
}

// Submit sends one transaction to POST /transactions, or several to POST /_bulk, each with its id as
// the idempotency key. The bulk runs sequentially: with parallel=true Formance returns results in
// completion order, and an error result does not say which element it belongs to.
func (f *Formance) Submit(ctx context.Context, txs []Tx) ([]error, error) {
	if len(txs) == 1 {
		var out formanceError
		h := http.Header{"Idempotency-Key": {txs[0].ID.String()}}
		status, err := f.c.do(ctx, "POST", "/transactions", h, f.body(txs[0]), &out)
		if err != nil {
			return nil, err
		}
		switch {
		case status == http.StatusOK:
			return []error{nil}, nil
		case out.ErrorCode == "INSUFFICIENT_FUND":
			return []error{ErrRejected}, nil
		default:
			return nil, unexpected("creating transaction", status, out)
		}
	}
	elems := make([]map[string]any, len(txs))
	for i, tx := range txs {
		elems[i] = map[string]any{"action": "CREATE_TRANSACTION", "ik": tx.ID.String(), "data": f.body(tx)}
	}
	var out struct {
		Data []struct {
			ResponseType string `json:"responseType"`
			ErrorCode    string `json:"errorCode"`
		} `json:"data"`
		formanceError
	}
	status, err := f.c.do(ctx, "POST", "/_bulk?continueOnFailure=true", nil, elems, &out)
	if err != nil {
		return nil, err
	}
	if (status != http.StatusOK && status != http.StatusBadRequest) || len(out.Data) != len(txs) {
		return nil, unexpected("sending bulk", status, out.formanceError)
	}
	results := make([]error, len(txs))
	for i, d := range out.Data {
		switch {
		case d.ResponseType != "ERROR":
		case d.ErrorCode == "INSUFFICIENT_FUND":
			results[i] = ErrRejected
		default:
			results[i] = fmt.Errorf("error creating transaction: %s", d.ErrorCode)
		}
	}
	return results, nil
}

// Balances returns the account's balance per asset. It uses the aggregate endpoint because account
// volumes need the MOVES_HISTORY feature.
func (f *Formance) Balances(ctx context.Context, address string) (map[string]int64, error) {
	var out struct {
		Data map[string]int64 `json:"data"`
		formanceError
	}
	filter := map[string]any{"$match": map[string]string{"address": address}}
	status, err := f.c.do(ctx, "GET", "/aggregate/balances", nil, filter, &out)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, unexpected("reading balances of "+address, status, out.formanceError)
	}
	m := make(map[string]int64, len(out.Data))
	for code, v := range out.Data {
		name, _, _ := strings.Cut(code, "/")
		m[name] = v
	}
	return m, nil
}

// Statement reads the latest 20 transactions touching an account under prefix.
func (f *Formance) Statement(ctx context.Context, prefix string) error {
	var out formanceError
	// an empty last segment matches any segment
	filter := map[string]any{"$match": map[string]string{"account": prefix + ":"}}
	status, err := f.c.do(ctx, "GET", "/transactions?pageSize=20", nil, filter, &out)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return unexpected("reading statement of "+prefix, status, out)
	}
	return nil
}
