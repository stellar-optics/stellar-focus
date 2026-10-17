package capture_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-focus/pkg/capture"
)

func TestRecordingAndReplayTransports(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(append([]byte("reply:"), body...))
	}))
	defer server.Close()

	recorder := &capture.RecordingTransport{Base: http.DefaultTransport}
	client := &http.Client{Transport: recorder}
	response, err := client.Post(server.URL+"/rpc", "application/json", strings.NewReader(`{"id":1}`))
	if err != nil {
		t.Fatalf("recorded request failed: %v", err)
	}
	recordedBody, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("reading recorded response: %v", err)
	}

	replay := capture.NewReplayTransport(recorder.Exchanges())
	replayClient := &http.Client{Transport: replay}
	replayed, err := replayClient.Post(server.URL+"/rpc", "application/json", strings.NewReader(`{"id":1}`))
	if err != nil {
		t.Fatalf("replayed request failed: %v", err)
	}
	replayedBody, err := io.ReadAll(replayed.Body)
	_ = replayed.Body.Close()
	if err != nil {
		t.Fatalf("reading replayed response: %v", err)
	}
	if string(replayedBody) != string(recordedBody) {
		t.Errorf("replayed body = %q, want %q", replayedBody, recordedBody)
	}
	if replay.Remaining() != 0 {
		t.Errorf("Remaining() = %d, want 0", replay.Remaining())
	}
}

func TestReplayTransportRejectsUnrecordedRequests(t *testing.T) {
	t.Parallel()

	transport := capture.NewReplayTransport(nil)
	request := httptest.NewRequest(http.MethodGet, "https://example.invalid/", nil)
	if _, err := transport.RoundTrip(request); err == nil || !strings.Contains(err.Error(), "no response") {
		t.Fatalf("RoundTrip() error = %v, want an offline replay miss", err)
	}
}