package capture

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sync"
)

// HTTPExchange is one request/response pair recorded for offline replay.
type HTTPExchange struct {
	Method         string      `json:"method"`
	URL            string      `json:"url"`
	RequestBody    []byte      `json:"requestBody,omitempty"`
	StatusCode     int         `json:"statusCode"`
	ResponseHeader http.Header `json:"responseHeader,omitempty"`
	ResponseBody   []byte      `json:"responseBody,omitempty"`
}

// RecordingTransport delegates requests and stores complete request/response
// pairs. It is intended for fixture creation, not assertion execution.
type RecordingTransport struct {
	Base http.RoundTripper
	mu   sync.Mutex
	log  []HTTPExchange
}

// RoundTrip implements http.RoundTripper.
func (t *RecordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if t.Base == nil {
		return nil, fmt.Errorf("recording transport has no base RoundTripper")
	}
	requestBody, err := readAndRestore(request)
	if err != nil {
		return nil, fmt.Errorf("reading request body: %w", err)
	}
	response, err := t.Base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, fmt.Errorf("base RoundTripper returned a nil response")
	}
	responseBody, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("reading response body: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("closing response body: %w", closeErr)
	}
	response.Body = io.NopCloser(bytes.NewReader(responseBody))
	t.mu.Lock()
	t.log = append(t.log, HTTPExchange{
		Method: request.Method, URL: request.URL.String(), RequestBody: requestBody,
		StatusCode: response.StatusCode, ResponseHeader: response.Header.Clone(), ResponseBody: responseBody,
	})
	t.mu.Unlock()
	return response, nil
}

// Exchanges returns a deep copy of the recorded exchanges.
func (t *RecordingTransport) Exchanges() []HTTPExchange {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]HTTPExchange, len(t.log))
	for i, exchange := range t.log {
		out[i] = cloneExchange(exchange)
	}
	return out
}

// ReplayTransport serves only recorded responses and never delegates to a
// network transport. Requests must arrive in capture order and match exactly.
type ReplayTransport struct {
	mu       sync.Mutex
	exchanges []HTTPExchange
	next     int
}

// NewReplayTransport creates an offline transport over recorded exchanges.
func NewReplayTransport(exchanges []HTTPExchange) *ReplayTransport {
	cloned := make([]HTTPExchange, len(exchanges))
	for i, exchange := range exchanges {
		cloned[i] = cloneExchange(exchange)
	}
	return &ReplayTransport{exchanges: cloned}
}

// RoundTrip implements http.RoundTripper and fails on any unrecorded request.
func (t *ReplayTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	body, err := readAndRestore(request)
	if err != nil {
		return nil, fmt.Errorf("reading replay request body: %w", err)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	if t.next >= len(t.exchanges) {
		return nil, fmt.Errorf("offline replay has no response for %s %s", request.Method, request.URL)
	}
	exchange := t.exchanges[t.next]
	if request.Method != exchange.Method || request.URL.String() != exchange.URL || !bytes.Equal(body, exchange.RequestBody) {
		return nil, fmt.Errorf("offline replay request %d mismatch: got %s %s", t.next, request.Method, request.URL)
	}
	t.next++
	return &http.Response{
		Status: fmt.Sprintf("%d %s", exchange.StatusCode, http.StatusText(exchange.StatusCode)),
		StatusCode: exchange.StatusCode, Header: exchange.ResponseHeader.Clone(),
		Body: io.NopCloser(bytes.NewReader(exchange.ResponseBody)), Request: request,
	}, nil
}

// Remaining reports how many captured responses have not been replayed.
func (t *ReplayTransport) Remaining() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.exchanges) - t.next
}

func readAndRestore(request *http.Request) ([]byte, error) {
	if request.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	if err := request.Body.Close(); err != nil {
		return nil, err
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	return body, nil
}

func cloneExchange(exchange HTTPExchange) HTTPExchange {
	exchange.RequestBody = bytes.Clone(exchange.RequestBody)
	exchange.ResponseBody = bytes.Clone(exchange.ResponseBody)
	exchange.ResponseHeader = exchange.ResponseHeader.Clone()
	if exchange.ResponseHeader == nil {
		exchange.ResponseHeader = make(http.Header)
	}
	return exchange
}

var _ http.RoundTripper = (*RecordingTransport)(nil)
var _ http.RoundTripper = (*ReplayTransport)(nil)