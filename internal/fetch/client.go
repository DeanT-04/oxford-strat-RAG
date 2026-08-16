// Package fetch provides a polite, retrying HTTP client used for both
// crawling HTML pages and streaming PDF downloads. It enforces a minimum
// interval between requests, a browser-grade User-Agent, per-request
// timeouts, exponential backoff, and a hard cap on in-memory bodies.
package fetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// maxHTMLBytes caps the in-memory size of a crawled HTML page. Pages larger
// than this are treated as an error rather than exhausting memory.
const maxHTMLBytes = 16 << 20 // 16 MiB

// StatusError reports a non-2xx HTTP status and is used for retry decisions.
type StatusError struct {
	Code int
	URL  string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("unexpected status %d for %s", e.Code, e.URL)
}

// Options configures a Client. Zero values fall back to sensible defaults.
type Options struct {
	UserAgent    string
	Timeout      time.Duration
	Retries      int
	Backoff      time.Duration // base backoff between retries
	Politeness   time.Duration // minimum interval between requests
	Transport    http.RoundTripper
	MaxBodyBytes int64 // cap for in-memory Get bodies; 0 uses maxHTMLBytes
}

// Client is a concurrency-safe HTTP client with politeness and retries.
type Client struct {
	http    *http.Client
	ua      string
	timeout time.Duration
	retries int
	backoff time.Duration
	polite  time.Duration
	maxBody int64

	mu     sync.Mutex
	lastAt time.Time
}

// New constructs a Client from opts.
func New(opts Options) *Client {
	c := &Client{
		ua:      opts.UserAgent,
		timeout: opts.Timeout,
		retries: opts.Retries,
		backoff: opts.Backoff,
		polite:  opts.Politeness,
	}
	if c.timeout <= 0 {
		c.timeout = 30 * time.Second
	}
	c.maxBody = opts.MaxBodyBytes
	if c.maxBody <= 0 {
		c.maxBody = maxHTMLBytes
	}
	transport := opts.Transport
	if transport == nil {
		transport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			TLSHandshakeTimeout:   10 * time.Second,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   8,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: c.timeout,
		}
	}
	c.http = &http.Client{
		Transport: transport,
		Timeout:   c.timeout,
	}
	return c
}

// Get fetches a full document body (capped at maxHTMLBytes) with retries.
func (c *Client) Get(ctx context.Context, u string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		if attempt > 0 {
			if err := sleepCtx(ctx, backoffDuration(c.backoff, attempt)); err != nil {
				return nil, err
			}
		}
		b, err := c.getOnce(ctx, u)
		if err == nil {
			return b, nil
		}
		lastErr = err
		if !retryable(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("giving up after %d attempts: %w", c.retries+1, lastErr)
}

// Stream performs a GET with retries and returns the open response body.
// The caller must close resp.Body. The final (post-redirect) URL is
// available as resp.Request.URL.
func (c *Client) Stream(ctx context.Context, u string) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		if attempt > 0 {
			if err := sleepCtx(ctx, backoffDuration(c.backoff, attempt)); err != nil {
				return nil, err
			}
		}
		resp, err := c.do(ctx, u)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !retryable(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("giving up after %d attempts: %w", c.retries+1, lastErr)
}

// getOnce performs one request and reads a bounded body.
func (c *Client) getOnce(ctx context.Context, u string) ([]byte, error) {
	resp, err := c.do(ctx, u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > c.maxBody {
		return nil, fmt.Errorf("body exceeds %d bytes", c.maxBody)
	}
	return body, nil
}

// do performs a single request and returns any non-2xx status as an error.
func (c *Client) do(ctx context.Context, u string) (*http.Response, error) {
	c.throttle()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if c.ua != "" {
		req.Header.Set("User-Agent", c.ua)
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/pdf,*/*;q=0.8")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		resp.Body.Close()
		return nil, &StatusError{Code: resp.StatusCode, URL: u}
	}
	return resp, nil
}

// throttle enforces the minimum interval between requests. It serializes
// the timing bookkeeping only; the network request itself happens outside
// the lock.
func (c *Client) throttle() {
	if c.polite <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if wait := c.polite - time.Since(c.lastAt); wait > 0 {
		time.Sleep(wait)
	}
	c.lastAt = time.Now()
}

// retryable reports whether an error is worth retrying. It never retries on
// explicit cancellation or on non-transient status codes (4xx other than
// 408/429).
func retryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	var se *StatusError
	if errors.As(err, &se) {
		switch se.Code {
		case http.StatusRequestTimeout, http.StatusTooManyRequests:
			return true
		default:
			return se.Code >= 500
		}
	}
	// Network errors and timeouts are transient by nature.
	return true
}

// backoffDuration returns the sleep before attempt n (n >= 1), doubling each
// time. A zero base means no delay.
func backoffDuration(base time.Duration, attempt int) time.Duration {
	if base <= 0 || attempt < 1 {
		return 0
	}
	return base << (attempt - 1)
}

// sleepCtx sleeps for d or until ctx is done, returning the context error.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// IsPDF reports whether data begins with the PDF magic header, tolerating a
// short preamble (BOM or HTTP whitespace) within the first 1 KiB.
func IsPDF(data []byte) bool {
	if len(data) < 5 {
		return false
	}
	limit := len(data)
	if limit > 1024 {
		limit = 1024
	}
	return bytes.Contains(data[:limit], []byte("%PDF-"))
}
