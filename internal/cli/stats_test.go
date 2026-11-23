package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/cli"
)

func TestStatsCommand(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	// 1. Create a transaction envelope fixture in JSON
	aid := xdr.MustAddress("GAPB7YMUQ6MPQ23PTFDNBZP7HGGTZPI2DT5JJURUOIXNPLGHW2O3WYXZ")
	op := xdr.Operation{
		Body: xdr.OperationBody{
			Type: xdr.OperationTypePayment,
			PaymentOp: &xdr.PaymentOp{
				Destination: aid.ToMuxedAccount(),
				Asset:       xdr.Asset{Type: xdr.AssetTypeAssetTypeNative},
				Amount:      100,
			},
		},
	}
	env := xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{
			Tx: xdr.Transaction{
				SourceAccount: aid.ToMuxedAccount(),
				Operations:    []xdr.Operation{op},
			},
		},
	}
	b64Env, err := xdr.MarshalBase64(env)
	if err != nil {
		t.Fatalf("marshaling envelope: %v", err)
	}

	fixtureJSON := `{
  "format": "focus/v1",
  "type": "TransactionEnvelope",
  "xdr": "` + b64Env + `",
  "captured": {
    "network": "testnet",
    "ledger": 100000
  }
}`
	if err := os.WriteFile(filepath.Join(tmpDir, "tx.golden.json"), []byte(fixtureJSON), 0o644); err != nil {
		t.Fatalf("writing json fixture: %v", err)
	}

	// 2. Create another fixture with YAML format
	fixtureYAML := `format: focus/v1
type: TransactionEnvelope
xdr: ` + b64Env + `
`
	if err := os.WriteFile(filepath.Join(tmpDir, "tx.golden.yaml"), []byte(fixtureYAML), 0o644); err != nil {
		t.Fatalf("writing yaml fixture: %v", err)
	}

	// 3. Create a text fixture
	if err := os.WriteFile(filepath.Join(tmpDir, "raw.golden.txt"), []byte("sample fixture content"), 0o644); err != nil {
		t.Fatalf("writing text fixture: %v", err)
	}

	t.Run("CollectStats counts metrics accurately", func(t *testing.T) {
		report, err := cli.CollectStats([]string{tmpDir})
		if err != nil {
			t.Fatalf("CollectStats failed: %v", err)
		}

		if report.TotalFixtures != 3 {
			t.Errorf("TotalFixtures = %d, want 3", report.TotalFixtures)
		}
		if report.Formats["JSON"] != 1 {
			t.Errorf("Formats[JSON] = %d, want 1", report.Formats["JSON"])
		}
		if report.Formats["YAML"] != 1 {
			t.Errorf("Formats[YAML] = %d, want 1", report.Formats["YAML"])
		}
		if report.Formats["Text"] != 1 {
			t.Errorf("Formats[Text] = %d, want 1", report.Formats["Text"])
		}
		if report.Operations["Payment"] < 1 {
			t.Errorf("Operations[Payment] = %d, want >= 1", report.Operations["Payment"])
		}
		if report.TotalBytes <= 0 {
			t.Errorf("TotalBytes = %d, want > 0", report.TotalBytes)
		}
		if report.AverageSizeBytes <= 0 {
			t.Errorf("AverageSizeBytes = %d, want > 0", report.AverageSizeBytes)
		}
	})

	t.Run("CLI output table format", func(t *testing.T) {
		var out bytes.Buffer
		cmd := cli.NewStatsCmd(&out)
		cmd.SetArgs([]string{"--format", "table", tmpDir})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute table stats: %v", err)
		}

		output := out.String()
		for _, want := range []string{
			"Fixture Coverage & Statistics",
			"Total Fixtures Analyzed: 3",
			"Formats Breakdown:",
			"JSON:",
			"YAML:",
			"Text:",
			"Operation Type Coverage Matrix:",
			"Payment:",
		} {
			if !strings.Contains(output, want) {
				t.Errorf("table output missing %q:\n%s", want, output)
			}
		}
	})

	t.Run("CLI output json format", func(t *testing.T) {
		var out bytes.Buffer
		cmd := cli.NewStatsCmd(&out)
		cmd.SetArgs([]string{"--format", "json", tmpDir})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute json stats: %v", err)
		}

		var report cli.StatsReport
		if err := json.Unmarshal(out.Bytes(), &report); err != nil {
			t.Fatalf("unmarshaling json stats: %v\nOutput:\n%s", err, out.String())
		}
		if report.TotalFixtures != 3 {
			t.Errorf("JSON report TotalFixtures = %d, want 3", report.TotalFixtures)
		}
		if report.Formats["JSON"] != 1 {
			t.Errorf("JSON report Formats[JSON] = %d, want 1", report.Formats["JSON"])
		}
	})
}
