// Package load drives a ledger with an operation stream and measures it.
package load

import (
	"math"
	"slices"
	"time"
)

// Latency summarises one request kind, in milliseconds.
type Latency struct {
	Requests int     `json:"requests"`
	Errors   int     `json:"errors"`
	P50      float64 `json:"p50_ms"`
	P90      float64 `json:"p90_ms"`
	P99      float64 `json:"p99_ms"`
	P999     float64 `json:"p999_ms"`
	Max      float64 `json:"max_ms"`
}

// summarise returns the percentiles of ds, sorting it in place.
// ponytail: keeps every sample in memory, about 8 MB per million requests
func summarise(ds []time.Duration, errors int) Latency {
	l := Latency{Requests: len(ds), Errors: errors}
	if len(ds) == 0 {
		return l
	}
	slices.Sort(ds)
	at := func(p float64) float64 {
		i := max(int(math.Ceil(p*float64(len(ds))))-1, 0)
		return float64(ds[i].Microseconds()) / 1000
	}
	l.P50, l.P90, l.P99, l.P999, l.Max = at(0.5), at(0.9), at(0.99), at(0.999), at(1)
	return l
}
