// Package capture — HTTP retry middleware with exponential backoff and jitter.
//
// When running focus capture tx --batch against public Soroban RPC endpoints
// or Horizon instances, requests frequently encounter rate limiting (HTTP 429)
// or temporary network errors (HTTP 502/503/504). RetryTransport wraps any
// http.RoundTripper and transparently retries on those status codes, applying
// exponential backoff with full jitter between attempts.
package capture

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// retryableStatusCodes is the set of HTTP status codes that warrant a retry.
// These are transient conditions: rate limiting, bad gateway, and temporary
// service unavailability.
var retryableStatusCodes = map[int]bool{
	http.StatusTooManyRequests:    true, // 429
	http.StatusBadGateway:         true, // 502
	http.StatusServiceUnavailable: true, // 503
	http.StatusGatewayTimeout:     true, // 504
}

// RetryConfig holds the parameters for the retry policy.
type RetryConfig struct {
	// MaxRetries is how many times to retry after the first failure.
	// A value of 0 means no retries (one attempt total).
	MaxRetries int
	// BaseInterval is the base backoff duration before the first retry.
	BaseInterval time.Duration
	// MaxInterval is the ceiling for the exponential backoff before jitter.
	MaxInterval time.Duration
}

// DefaultRetryConfig is the retry policy applied when no explicit config is
// given. It matches the requirements: 5 retries starting at 500 ms.
var DefaultRetryConfig = RetryConfig{
	MaxRetries:   5,
	BaseInterval: 500 * time.Millisecond,
	MaxInterval:  30 * time.Second,
}

// RetryTransport is an http.RoundTripper that retries on transient HTTP errors
// using exponential backoff with full jitter.
//
// Wrap a plain http.Client's Transport to add retry behaviour:
//
//	client := &http.Client{
//	    Transport: NewRetryTransport(nil, DefaultRetryConfig),
//	}
type RetryTransport struct {
	base   http.RoundTripper
	config RetryConfig
	// sleep is injectable for tests; nil uses time.Sleep.
	sleep func(time.Duration)
}

// NewRetryTransport returns a RetryTransport that delegates to base.
// When base is nil, http.DefaultTransport is used.
func NewRetryTransport(base http.RoundTripper, cfg RetryConfig) *RetryTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &RetryTransport{base: base, config: cfg}
}

// SetSleepFn replaces the sleep implementation used between retries. It is
// intended for unit tests that want to control or observe backoff timing
// without actually sleeping.
func (t *RetryTransport) SetSleepFn(fn func(time.Duration)) { t.sleep = fn }

// RoundTrip implements http.RoundTripper.
//
// It retries the request on retryable status codes (429, 502, 503, 504) and
// on transient connection-reset errors. If the response includes a Retry-After
// header it honours that duration instead of the computed backoff for that
// attempt.
func (t *RetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cfg := t.config
	sleepFn := t.sleep
	if sleepFn == nil {
		sleepFn = time.Sleep
	}

	var bodyBytes []byte
	if req.Body != nil && req.GetBody == nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}

	var (
		resp    *http.Response
		lastErr error
	)

	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			wait := t.backoff(attempt, resp, cfg)
			sleepFn(wait)
			// Drain and close the previous response body before retrying.
			if resp != nil {
				//nolint:errcheck // Draining the body is best-effort before retry; read errors can be safely ignored.
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		}

		// Clone the request so context is preserved, and re-arm the body for retry.
		cloned := req.Clone(req.Context())
		if req.GetBody != nil {
			var err error
			cloned.Body, err = req.GetBody()
			if err != nil {
				return nil, err
			}
		} else if bodyBytes != nil {
			cloned.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		resp, lastErr = t.base.RoundTrip(cloned)

		if lastErr != nil {
			// Retry on transient connection errors.
			if isTransientNetError(lastErr) {
				resp = nil
				continue
			}
			return nil, lastErr
		}

		if !retryableStatusCodes[resp.StatusCode] {
			return resp, nil
		}
		// Keep resp so backoff can read Retry-After, then retry.
	}

	if lastErr != nil {
		return nil, lastErr
	}
	// Return the last response (a retryable one) if we exhausted retries.
	return resp, nil
}

// backoff computes the duration to sleep before attempt number n (1-based).
//
// If the response carries a Retry-After header its value is used directly.
// Otherwise exponential backoff with full jitter is applied:
//
//	sleep = random(0, min(maxInterval, baseInterval * 2^(attempt-1)))
func (t *RetryTransport) backoff(attempt int, resp *http.Response, cfg RetryConfig) time.Duration {
	// Respect Retry-After if the server sent one.
	if resp != nil {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			ra = strings.TrimSpace(ra)
			if secs, err := strconv.ParseFloat(ra, 64); err == nil && secs > 0 {
				return time.Duration(secs * float64(time.Second))
			}
			if tm, err := http.ParseTime(ra); err == nil {
				if d := time.Until(tm); d > 0 {
					return d
				}
				return 0
			}
		}
	}

	// Exponential ceiling.
	exp := cfg.BaseInterval
	for i := 1; i < attempt; i++ {
		exp *= 2
		if exp > cfg.MaxInterval {
			exp = cfg.MaxInterval
			break
		}
	}
	if exp > cfg.MaxInterval {
		exp = cfg.MaxInterval
	}

	// Full jitter: uniform random in [0, exp].
	if exp <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(exp) + 1))
}

// isTransientNetError reports whether err looks like a transient connection
// problem that is worth retrying.
func isTransientNetError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "eof") ||
		strings.Contains(msg, "i/o timeout")
}

// RetryHTTPClient returns an *http.Client whose transport automatically retries
// transient HTTP errors according to cfg, with the given per-request timeout.
func RetryHTTPClient(timeout time.Duration, cfg RetryConfig) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: NewRetryTransport(nil, cfg),
	}
}

// ErrMaxRetriesExceeded is returned when all retry attempts are exhausted.
// It is wrapped around the last error so callers can distinguish it.
var ErrMaxRetriesExceeded = fmt.Errorf("max retries exceeded")
