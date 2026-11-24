// Package fakearchive serves a Stellar history archive from memory, for
// tests of capture.ArchivalSource.
//
// The archive is an httptest server on the loopback interface, so the tests
// that use it stay offline.
package fakearchive

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// Archive is an in-memory history archive.
type Archive struct {
	// Passphrase is recorded in the history archive state.
	Passphrase string

	mu           sync.Mutex
	transactions map[uint32][]xdr.TransactionHistoryEntry
	results      map[uint32][]xdr.TransactionHistoryResultEntry
	headers      map[uint32][]xdr.LedgerHeaderHistoryEntry
	files        map[string][]byte
	requests     []string
}

// New returns an empty archive for a network with the given passphrase.
func New(passphrase string) *Archive {
	return &Archive{
		Passphrase:   passphrase,
		transactions: map[uint32][]xdr.TransactionHistoryEntry{},
		results:      map[uint32][]xdr.TransactionHistoryResultEntry{},
		headers:      map[uint32][]xdr.LedgerHeaderHistoryEntry{},
		files:        map[string][]byte{},
	}
}

// checkpoint returns the checkpoint holding a ledger.
func checkpoint(ledger uint32) uint32 { return ledger | 63 }

// AddLedger records a ledger's transaction set, results and header.
func (a *Archive) AddLedger(
	tx xdr.TransactionHistoryEntry,
	results xdr.TransactionHistoryResultEntry,
	header xdr.LedgerHeaderHistoryEntry,
) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := checkpoint(uint32(tx.LedgerSeq))
	a.transactions[c] = append(a.transactions[c], tx)
	a.results[c] = append(a.results[c], results)
	a.headers[c] = append(a.headers[c], header)
}

// SetFile serves raw bytes at a path, overriding what the archive would
// generate there.
func (a *Archive) SetFile(path string, data []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.files[path] = data
}

// Requests returns the paths requested so far.
func (a *Archive) Requests() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.requests...)
}

// Serve starts the archive and returns its URL; it stops with the test.
func (a *Archive) Serve(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(a.handle))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (a *Archive) handle(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = append(a.requests, r.URL.Path)

	if data, ok := a.files[r.URL.Path]; ok {
		write(w, data)
		return
	}
	if r.URL.Path == "/.well-known/stellar-history.json" {
		state, err := json.Marshal(map[string]any{
			"version":           2,
			"networkPassphrase": a.Passphrase,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		write(w, state)
		return
	}

	for c := range a.transactions {
		switch r.URL.Path {
		case Path("transactions", c):
			writeStream(w, a.transactions[c])
			return
		case Path("results", c):
			writeStream(w, a.results[c])
			return
		case Path("ledger", c):
			writeStream(w, a.headers[c])
			return
		}
	}
	http.NotFound(w, r)
}

// Path is the archive path of a category's file for the checkpoint holding
// ledger, as the archive layout specifies it.
func Path(category string, ledger uint32) string {
	name := fmt.Sprintf("%08x", checkpoint(ledger))
	return fmt.Sprintf("/%s/%s/%s/%s/%s-%s.xdr.gz", category, name[0:2], name[2:4], name[4:6], category, name)
}

// Gzip returns a gzipped stream of record-marked XDR values, as an archive
// stores them.
func Gzip[T any](entries []T) []byte {
	var raw bytes.Buffer
	for _, e := range entries {
		var record bytes.Buffer
		if _, err := xdr.Marshal(&record, e); err != nil {
			panic(err)
		}
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], uint32(record.Len())|1<<31)
		raw.Write(header[:])
		raw.Write(record.Bytes())
	}
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	if _, err := zw.Write(raw.Bytes()); err != nil {
		panic(err)
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return out.Bytes()
}

func writeStream[T any](w http.ResponseWriter, entries []T) {
	write(w, Gzip(entries))
}

// write sends a response body. A client that hung up mid-response is the
// test's problem to report, not the server's, so a failed write is dropped.
func write(w http.ResponseWriter, data []byte) {
	if _, err := w.Write(data); err != nil {
		return
	}
}
