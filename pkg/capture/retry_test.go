package capture_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stellar-optics/stellar-focus/pkg/capture"
)

// newRetryServer starts an HTTP test server that returns statusCode for the
// first `failFirst` requests and 200 thereafter. It also sets Retry-After on
// the failing responses when retryAfterSecs > 0.
func newRetryServer(t *testing.T, statusCode, failFirst int, retryAfterSecs float64) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if int(n) <= failFirst {
			if retryAfterSecs > 0 {
				w.Header().Set("Retry-After", fmt.Sprintf("%.0f", retryAfterSecs))
			}
			w.WriteHeader(statusCode)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// TestRetryTransportRetriesOn429 verifies that the transport retries on
// HTTP 429 Too Many Requests and eventually succeeds.
func TestRetryTransportRetriesOn429(t *testing.T) {
	t.Parallel()

	srv, calls := newRetryServer(t, http.StatusTooManyRequests, 2, 0)

	var slept time.Duration
	cfg := capture.RetryConfig{MaxRetries: 5, BaseInterval: time.Millisecond, MaxInterval: 10 * time.Millisecond}
	rt := capture.NewRetryTransport(nil, cfg)
	rt.SetSleepFn(func(d time.Duration) { slept += d })

	client := &http.Client{Transport: rt}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET failed after retries: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("final status = %d, want 200", resp.StatusCode)
	}
	if atomic.LoadInt32(calls) != 3 {
		t.Errorf("expected 3 calls (2 failures + 1 success), got %d", atomic.LoadInt32(calls))
	}
	_ = slept
}

// TestRetryTransportRetriesOn503 verifies that the transport retries on
// HTTP 503 Service Unavailable.
func TestRetryTransportRetriesOn503(t *testing.T) {
	t.Parallel()

	srv, calls := newRetryServer(t, http.StatusServiceUnavailable, 1, 0)

	cfg := capture.RetryConfig{MaxRetries: 3, BaseInterval: time.Millisecond, MaxInterval: 5 * time.Millisecond}
	rt := capture.NewRetryTransport(nil, cfg)
	rt.SetSleepFn(func(time.Duration) {})

	client := &http.Client{Transport: rt}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("final status = %d, want 200", resp.StatusCode)
	}
	if atomic.LoadInt32(calls) != 2 {
		t.Errorf("expected 2 calls (1 failure + 1 success), got %d", atomic.LoadInt32(calls))
	}
}

// TestRetryTransportHonoursMaxRetries verifies that the transport stops after
// MaxRetries and returns the last (retryable) response.
func TestRetryTransportHonoursMaxRetries(t *testing.T) {
	t.Parallel()

	const maxRetries = 3
	srv, calls := newRetryServer(t, http.StatusTooManyRequests, 100 /* always fail */, 0)

	cfg := capture.RetryConfig{MaxRetries: maxRetries, BaseInterval: time.Millisecond, MaxInterval: 5 * time.Millisecond}
	rt := capture.NewRetryTransport(nil, cfg)
	rt.SetSleepFn(func(time.Duration) {})

	client := &http.Client{Transport: rt}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	// After exhausting retries the last 429 is returned.
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", resp.StatusCode)
	}
	wantCalls := int32(maxRetries + 1)
	if atomic.LoadInt32(calls) != wantCalls {
		t.Errorf("expected %d total calls, got %d", wantCalls, atomic.LoadInt32(calls))
	}
}

// TestRetryTransportRespectsRetryAfter verifies that a Retry-After header is
// honoured: the sleep duration must be at least the value the server sent.
func TestRetryTransportRespectsRetryAfter(t *testing.T) {
	t.Parallel()

	// Server returns 429 for the first request with Retry-After: 1 (second).
	srv, _ := newRetryServer(t, http.StatusTooManyRequests, 1, 1 /* one second */)

	var sleptDurations []time.Duration
	cfg := capture.RetryConfig{MaxRetries: 3, BaseInterval: time.Millisecond, MaxInterval: 5 * time.Millisecond}
	rt := capture.NewRetryTransport(nil, cfg)
	rt.SetSleepFn(func(d time.Duration) { sleptDurations = append(sleptDurations, d) })

	client := &http.Client{Transport: rt}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	resp.Body.Close()

	if len(sleptDurations) == 0 {
		t.Fatal("no sleep recorded; Retry-After was not honoured")
	}
	// The first sleep must be at least 1 second (from Retry-After header).
	if sleptDurations[0] < time.Second {
		t.Errorf("first sleep = %v, want ≥ 1s (from Retry-After)", sleptDurations[0])
	}
}

// TestRetryTransportDoesNotRetryOn404 verifies that non-retryable status codes
// (like 404 Not Found) are returned immediately without retrying.
func TestRetryTransportDoesNotRetryOn404(t *testing.T) {
	t.Parallel()

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	cfg := capture.RetryConfig{MaxRetries: 5, BaseInterval: time.Millisecond, MaxInterval: 5 * time.Millisecond}
	rt := capture.NewRetryTransport(nil, cfg)
	rt.SetSleepFn(func(time.Duration) {})

	client := &http.Client{Transport: rt}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("expected exactly 1 call for 404, got %d", atomic.LoadInt32(&calls))
	}
}

// TestRetryHTTPClientConfig verifies that RetryHTTPClient returns a client
// with the right timeout and a RetryTransport.
func TestRetryHTTPClientConfig(t *testing.T) {
	t.Parallel()

	client := capture.RetryHTTPClient(5*time.Second, capture.DefaultRetryConfig)
	if client.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", client.Timeout)
	}
	if _, ok := client.Transport.(*capture.RetryTransport); !ok {
		t.Errorf("Transport is %T, want *capture.RetryTransport", client.Transport)
	}
}

// TestRetryTransportPreservesBodyOnRetry verifies that request bodies on POST
// requests are re-sent intact on retry attempts.
func TestRetryTransportPreservesBodyOnRetry(t *testing.T) {
	t.Parallel()

	const expectedBody = `{"jsonrpc":"2.0","method":"getLatestLedger"}`
	var calls int32
	var receivedBodies []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedBodies = append(receivedBodies, string(b))
		n := atomic.AddInt32(&calls, 1)
		if n <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "success")
	}))
	t.Cleanup(srv.Close)

	cfg := capture.RetryConfig{MaxRetries: 3, BaseInterval: time.Millisecond, MaxInterval: 5 * time.Millisecond}
	rt := capture.NewRetryTransport(nil, cfg)
	rt.SetSleepFn(func(time.Duration) {})

	client := &http.Client{Transport: rt}
	resp, err := client.Post(srv.URL, "application/json", bytes.NewReader([]byte(expectedBody)))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("final status = %d, want 200", resp.StatusCode)
	}
	if len(receivedBodies) != 3 {
		t.Fatalf("expected 3 requests, got %d", len(receivedBodies))
	}
	for i, b := range receivedBodies {
		if b != expectedBody {
			t.Errorf("attempt %d: body = %q, want %q", i+1, b, expectedBody)
		}
	}
}

// TestRetryTransportRespectsRetryAfterHTTPDate verifies that HTTP-date format
// in Retry-After header is parsed and respected.
func TestRetryTransportRespectsRetryAfterHTTPDate(t *testing.T) {
	t.Parallel()

	targetTime := time.Now().Add(2 * time.Second)
	var calls int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.Header().Set("Retry-After", targetTime.UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	var sleptDurations []time.Duration
	cfg := capture.RetryConfig{MaxRetries: 3, BaseInterval: time.Millisecond, MaxInterval: 5 * time.Millisecond}
	rt := capture.NewRetryTransport(nil, cfg)
	rt.SetSleepFn(func(d time.Duration) { sleptDurations = append(sleptDurations, d) })

	client := &http.Client{Transport: rt}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	resp.Body.Close()

	if len(sleptDurations) == 0 {
		t.Fatal("no sleep recorded for HTTP-date Retry-After")
	}
	if sleptDurations[0] < time.Second {
		t.Errorf("sleep duration = %v, want at least 1s for HTTP-date Retry-After", sleptDurations[0])
	}
}
