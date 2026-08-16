package fetch

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewDefaults(t *testing.T) {
	c := New(Options{})
	if c.timeout <= 0 {
		t.Fatal("timeout must default to > 0")
	}
	if c.maxBody != maxHTMLBytes {
		t.Fatalf("maxBody default = %d, want %d", c.maxBody, maxHTMLBytes)
	}
}

func TestGetSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello"))
	}))
	defer srv.Close()
	c := New(Options{Retries: 0})
	b, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "hello" {
		t.Fatalf("got %q", b)
	}
}

func TestGetNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := New(Options{Retries: 0})
	_, err := c.Get(context.Background(), srv.URL)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusNotFound {
		t.Fatalf("want StatusError 404, got %v", err)
	}
}

func TestGetRetriesThenSucceeds(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	c := New(Options{Retries: 2, Backoff: time.Millisecond})
	b, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "ok" {
		t.Fatalf("got %q", b)
	}
	if atomic.LoadInt32(&n) != 2 {
		t.Fatalf("attempts = %d, want 2", n)
	}
}

func TestGetGivesUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway) // 502: retryable
	}))
	defer srv.Close()
	c := New(Options{Retries: 2, Backoff: time.Millisecond})
	_, err := c.Get(context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "giving up") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := New(Options{Retries: 3, Backoff: time.Millisecond})
	_, err := c.Get(ctx, srv.URL)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestGetOversize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("x", 100)))
	}))
	defer srv.Close()
	c := New(Options{Retries: 0, MaxBodyBytes: 10})
	_, err := c.Get(context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected oversize error, got %v", err)
	}
}

func TestStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("%PDF-1.4"))
	}))
	defer srv.Close()
	c := New(Options{Retries: 0})
	resp, err := c.Stream(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "%PDF-1.4" {
		t.Fatalf("got %q", b)
	}
	if resp.Request == nil {
		t.Fatal("resp.Request must not be nil")
	}
}

func TestThrottleSpacing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	c := New(Options{Retries: 0, Politeness: 10 * time.Millisecond})
	start := time.Now()
	if _, err := c.Get(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 10*time.Millisecond {
		t.Fatalf("expected politeness spacing, elapsed %v", elapsed)
	}
}

func TestRetryable(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{context.Canceled, false},
		{&StatusError{Code: http.StatusNotFound}, false},
		{&StatusError{Code: http.StatusUnauthorized}, false},
		{&StatusError{Code: http.StatusRequestTimeout}, true},
		{&StatusError{Code: http.StatusTooManyRequests}, true},
		{&StatusError{Code: http.StatusInternalServerError}, true},
		{&StatusError{Code: http.StatusServiceUnavailable}, true},
		{errors.New("connection reset"), true},
	}
	for _, tc := range cases {
		if got := retryable(tc.err); got != tc.want {
			t.Errorf("retryable(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestBackoffDuration(t *testing.T) {
	if got := backoffDuration(0, 3); got != 0 {
		t.Errorf("zero base: %v", got)
	}
	if got := backoffDuration(time.Second, 0); got != 0 {
		t.Errorf("attempt 0: %v", got)
	}
	if got := backoffDuration(time.Second, 1); got != time.Second {
		t.Errorf("attempt 1: %v", got)
	}
	if got := backoffDuration(time.Second, 2); got != 2*time.Second {
		t.Errorf("attempt 2: %v", got)
	}
	if got := backoffDuration(time.Second, 3); got != 4*time.Second {
		t.Errorf("attempt 3: %v", got)
	}
}

func TestSleepCtx(t *testing.T) {
	if err := sleepCtx(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepCtx(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("want canceled, got %v", err)
	}
}

func TestIsPDF(t *testing.T) {
	cases := []struct {
		in   []byte
		want bool
	}{
		{[]byte("%PDF-1.4\ncontent"), true},
		{[]byte("hello world"), false},
		{[]byte("%PD"), false},
		{append([]byte("\xef\xbb\xbf"), []byte("%PDF-1.7")...), true},
	}
	for _, tc := range cases {
		if got := IsPDF(tc.in); got != tc.want {
			t.Errorf("IsPDF(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestStreamRetriesThenSucceeds(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	c := New(Options{Retries: 2, Backoff: time.Millisecond})
	resp, err := c.Stream(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "ok" {
		t.Fatalf("got %q", b)
	}
	if atomic.LoadInt32(&n) != 2 {
		t.Fatalf("attempts = %d", n)
	}
}

func TestStreamGivesUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	c := New(Options{Retries: 2, Backoff: time.Millisecond})
	_, err := c.Stream(context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "giving up") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStreamContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := New(Options{Retries: 3, Backoff: time.Millisecond})
	_, err := c.Stream(ctx, srv.URL)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want canceled, got %v", err)
	}
}

func TestGetContextDeadlineDuringBackoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	c := New(Options{Retries: 2, Backoff: time.Second})
	_, err := c.Get(ctx, srv.URL)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
}

func TestGetInvalidURL(t *testing.T) {
	c := New(Options{Retries: 0})
	if _, err := c.Get(context.Background(), "://bad"); err == nil {
		t.Fatal("expected error")
	}
}

func TestIsPDFLarge(t *testing.T) {
	if IsPDF(make([]byte, 2048)) {
		t.Fatal("expected false for large non-PDF body")
	}
}

type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

type failingRoundTripper struct{}

func (failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(failReader{})}, nil
}

func TestGetEmptyUserAgent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	c := New(Options{Retries: 0, UserAgent: ""})
	if _, err := c.Get(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
}

func TestGetReadError(t *testing.T) {
	c := New(Options{Retries: 0, Transport: failingRoundTripper{}})
	_, err := c.Get(context.Background(), "https://anything")
	if err == nil || !strings.Contains(err.Error(), "read failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStreamDeadlineDuringBackoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	c := New(Options{Retries: 2, Backoff: time.Second})
	_, err := c.Stream(ctx, srv.URL)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
}

func TestCheckSafeURL(t *testing.T) {
	ok := []string{
		"https://oxfordstrat.com/x.pdf",
		"http://www.cmegroup.com/x.pdf",
		"https://papers.ssrn.com/delivery.php",
	}
	for _, u := range ok {
		parsed, _ := url.Parse(u)
		if err := checkSafeURL(parsed); err != nil {
			t.Errorf("checkSafeURL(%q) should be safe, got %v", u, err)
		}
	}
	bad := []string{
		"http://127.0.0.1:8080/",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1/",
		"http://192.168.1.1/",
		"http://[::1]/",
		"file:///etc/passwd",
	}
	for _, u := range bad {
		parsed, _ := url.Parse(u)
		if err := checkSafeURL(parsed); err == nil {
			t.Errorf("checkSafeURL(%q) should be rejected", u)
		}
	}
}
