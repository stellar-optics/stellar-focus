package capture

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"testing"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// headerFixture encodes a LedgerHeaderHistoryEntry with the given fields so
// tests run against real XDR without a network.
func headerFixture(t *testing.T, version, baseFee, baseReserve, seq uint32, totalCoins int64) string {
	t.Helper()
	entry := xdr.LedgerHeaderHistoryEntry{
		Hash: xdr.Hash{1, 2, 3},
		Header: xdr.LedgerHeader{
			LedgerVersion: xdr.Uint32(version),
			ScpValue: xdr.StellarValue{
				CloseTime: xdr.TimePoint(1700000000),
			},
			LedgerSeq:   xdr.Uint32(seq),
			TotalCoins:  xdr.Int64(totalCoins),
			BaseFee:     xdr.Uint32(baseFee),
			BaseReserve: xdr.Uint32(baseReserve),
		},
	}
	var buf bytes.Buffer
	if _, err := xdr.Marshal(&buf, entry); err != nil {
		t.Fatalf("marshalling fixture header: %v", err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// fakeHeaderSource is an offline HeaderSource.
type fakeHeaderSource struct {
	network  string
	endpoint string
	resp     *protocol.GetLatestLedgerResponse
	err      error
}

func (f *fakeHeaderSource) Network() string  { return f.network }
func (f *fakeHeaderSource) Endpoint() string { return f.endpoint }
func (f *fakeHeaderSource) LedgerHeader(ctx context.Context) (*protocol.GetLatestLedgerResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func TestCaptureLedgerHeaderRecord(t *testing.T) {
	b64 := headerFixture(t, 20, 100, 5000, 8100, 105000000)
	src := &fakeHeaderSource{
		network:  NetworkTestnet,
		endpoint: DefaultEndpoints[NetworkTestnet],
		resp: &protocol.GetLatestLedgerResponse{
			Hash:            "abc123",
			ProtocolVersion: 20,
			Sequence:        8100,
			LedgerCloseTime: 1700000000,
			LedgerHeader:    b64,
		},
	}

	rec, err := FetchLedgerHeader(context.Background(), src)
	if err != nil {
		t.Fatalf("FetchLedgerHeader: %v", err)
	}

	if rec.Name != "header" || rec.Type != "LedgerHeaderHistoryEntry" {
		t.Errorf("record identity wrong: %+v", rec)
	}
	if rec.XDR != b64 || rec.Ledger != 8100 {
		t.Errorf("provenance wrong: %+v", rec)
	}
	if !contains(rec.Description, "abc123") {
		t.Errorf("description must carry the ledger hash: %q", rec.Description)
	}
}

func TestParseLedgerHeaderFields(t *testing.T) {
	b64 := headerFixture(t, 20, 100, 5000, 8100, 105000000)

	fields, err := ParseLedgerHeader(b64)
	if err != nil {
		t.Fatalf("ParseLedgerHeader: %v", err)
	}

	if fields.Version != 20 {
		t.Errorf("ledger version: want 20, got %d", fields.Version)
	}
	if fields.BaseFee != 100 {
		t.Errorf("base fee (stroops): want 100, got %d", fields.BaseFee)
	}
	if fields.BaseReserve != 5000 {
		t.Errorf("base reserve: want 5000, got %d", fields.BaseReserve)
	}
	if fields.TotalCoins != 105000000 {
		t.Errorf("total coins: want 105000000, got %d", fields.TotalCoins)
	}
	if fields.Sequence != 8100 {
		t.Errorf("ledger sequence: want 8100, got %d", fields.Sequence)
	}
}

func TestParseLedgerHeaderErrors(t *testing.T) {
	t.Run("not base64", func(t *testing.T) {
		_, err := ParseLedgerHeader("!!!not-base64!!!")
		if err == nil || !contains(err.Error(), "decoding ledger header") {
			t.Fatalf("want a decode error, got: %v", err)
		}
	})

	t.Run("wrong XDR type", func(t *testing.T) {
		// A valid XDR value of a different type fails at parsing, not
		// decoding.
		var buf bytes.Buffer
		scval := xdr.ScVal{Type: xdr.ScValTypeScvBool, B: new(bool)}
		if _, mErr := xdr.Marshal(&buf, scval); mErr != nil {
			t.Fatalf("marshalling wrong type: %v", mErr)
		}
		_, err := ParseLedgerHeader(base64.StdEncoding.EncodeToString(buf.Bytes()))
		if err == nil || !contains(err.Error(), "parsing ledger header") {
			t.Fatalf("want a parse error, got: %v", err)
		}
	})
}

func TestCaptureLedgerHeaderErrors(t *testing.T) {
	t.Run("transport failure", func(t *testing.T) {
		_, err := FetchLedgerHeader(context.Background(), &fakeHeaderSource{err: fmt.Errorf("boom")})
		if err == nil || !contains(err.Error(), "getLatestLedger") {
			t.Fatalf("want an RPC error, got: %v", err)
		}
	})

	t.Run("no header XDR", func(t *testing.T) {
		_, err := FetchLedgerHeader(context.Background(), &fakeHeaderSource{
			resp: &protocol.GetLatestLedgerResponse{Sequence: 1},
		})
		if err == nil || !contains(err.Error(), "no header XDR") {
			t.Fatalf("want a no-header error, got: %v", err)
		}
	})
}
