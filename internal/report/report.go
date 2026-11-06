// Package report stores one run's result and renders many as an HTML report.
package report

import (
	"cmp"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"os"
	"slices"

	"torbench/internal/brokerage"
	"torbench/internal/load"
)

// Run is one load step's configuration, measurements and check outcome.
type Run struct {
	System    string           `json:"system"`
	Label     string           `json:"label"`
	Brokerage brokerage.Config `json:"brokerage"`
	Load      load.Config      `json:"load"`
	load.Result
	Checked    bool     `json:"checked"`
	Unresolved int      `json:"unresolved"`
	Problems   []string `json:"problems"`

	// AppliedPerSec counts applied writes; DonePerSec also counts rejected ones. RequestsPerSec counts
	// every request, reads included.
	AppliedPerSec  float64 `json:"applied_per_sec"`
	DonePerSec     float64 `json:"done_per_sec"`
	RequestsPerSec float64 `json:"requests_per_sec"`
	// WriteP50 and WriteP99 are the buy latency, or the batch latency for batched runs.
	WriteP50 float64 `json:"write_p50_ms"`
	WriteP99 float64 `json:"write_p99_ms"`
}

// Summarise fills the derived fields from the result.
func (r *Run) Summarise() {
	var applied, done int
	for kind, c := range r.Ops {
		if kind.IsRead() {
			continue
		}
		applied += c.OK
		done += c.OK + c.Rejected
	}
	requests := 0
	for _, l := range r.Latency {
		requests += l.Requests
	}
	secs := r.Elapsed.Seconds()
	r.AppliedPerSec, r.DonePerSec, r.RequestsPerSec = float64(applied)/secs, float64(done)/secs, float64(requests)/secs
	w := r.Latency[string(brokerage.Buy)]
	if r.Load.Batch > 1 {
		w = r.Latency["batch"]
	}
	r.WriteP50, r.WriteP99 = w.P50, w.P99
}

// Mode returns "open" for a fixed-rate run and "closed" otherwise.
func (r Run) Mode() string {
	if r.Load.Rate > 0 {
		return "open"
	}
	return "closed"
}

// RequestErrors returns how many requests failed as a whole.
func (r Run) RequestErrors() int {
	n := 0
	for _, l := range r.Latency {
		n += l.Errors
	}
	return n
}

// Series names the curve a run belongs to: everything but the load level.
func (r Run) Series() string {
	return fmt.Sprintf("%s shards=%d zipf=%g batch=%d", r.Label, r.Brokerage.Shards, r.Brokerage.Zipf, r.Load.Batch)
}

//go:embed report.html
var page string

var tmpl = template.Must(template.New("report").Parse(page))

// point is one chart point.
type point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Write renders the runs stored in files as an HTML report.
func Write(w io.Writer, files []string) error {
	runs := make([]Run, 0, len(files))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("error reading %s: %w", f, err)
		}
		var r Run
		if err := json.Unmarshal(b, &r); err != nil {
			return fmt.Errorf("error decoding %s: %w", f, err)
		}
		runs = append(runs, r)
	}
	slices.SortFunc(runs, func(a, b Run) int {
		return cmp.Or(cmp.Compare(a.Mode(), b.Mode()), cmp.Compare(a.Series(), b.Series()),
			cmp.Compare(a.Load.Rate, b.Load.Rate), cmp.Compare(a.Load.Concurrency, b.Load.Concurrency))
	})
	// closed loop: applied writes per second against concurrency; open loop: write p99 against rate
	throughput, latency := map[string][]point{}, map[string][]point{}
	for _, r := range runs {
		if r.Mode() == "open" {
			latency[r.Series()] = append(latency[r.Series()], point{r.Load.Rate, r.WriteP99})
		} else {
			throughput[r.Series()] = append(throughput[r.Series()], point{float64(r.Load.Concurrency), r.AppliedPerSec})
		}
	}
	return tmpl.Execute(w, map[string]any{"Runs": runs, "Throughput": throughput, "Latency": latency})
}
