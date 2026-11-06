package ledger

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// httpClient is shared by every adapter; it keeps enough idle connections for high concurrency.
var httpClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        4096,
		MaxIdleConnsPerHost: 4096,
		IdleConnTimeout:     90 * time.Second,
	},
}

// client sends JSON requests to one base URL.
type client struct {
	base   string
	header http.Header
}

// do sends body as JSON, decodes the response into out when out is non-nil and the response has a body,
// and returns the status code. An error means no usable response arrived.
func (c client) do(ctx context.Context, method, path string, header http.Header, body, out any) (int, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("error encoding %s %s: %w", method, path, err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return 0, fmt.Errorf("error building %s %s: %w", method, path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range c.header {
		req.Header[k] = v
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("error sending %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, fmt.Errorf("error reading %s %s: %w", method, path, err)
	}
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return resp.StatusCode, fmt.Errorf("error decoding %s %s (%d): %w: %s", method, path, resp.StatusCode, err, b)
		}
	}
	return resp.StatusCode, nil
}

// unexpected returns an error for a response with an unexpected status.
func unexpected(op string, status int, body any) error {
	return fmt.Errorf("error %s: status %d: %v", op, status, body)
}
