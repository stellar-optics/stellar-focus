package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// StatsReport captures coverage and disk metrics across test fixtures.
type StatsReport struct {
	TotalFixtures    int            `json:"totalFixtures"`
	Formats          map[string]int `json:"formats"`
	TotalBytes       int64          `json:"totalBytes"`
	AverageSizeBytes int64          `json:"averageSizeBytes"`
	Operations       map[string]int `json:"operations"`
	ProtocolVersions map[string]int `json:"protocolVersions"`
}

// NewStatsCmd builds the focus stats command.
func NewStatsCmd(stdout io.Writer) *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "stats [flags] [paths...]",
		Short: "Report fixture coverage across operations, protocol versions, and disk footprint",
		Long: `stats inspects test fixtures recursively and reports coverage metrics:
format breakdown (JSON, YAML, Text), operation type coverage matrix,
protocol version distribution, and total/average disk footprint.`,
		Example: `  focus stats
  focus stats testdata/
  focus stats --format json testdata/ fixtures/`,
		RunE: func(cmd *cobra.Command, args []string) error {
			paths := args
			if len(paths) == 0 {
				paths = []string{"."}
			}

			report, err := CollectStats(paths)
			if err != nil {
				return err
			}

			switch strings.ToLower(format) {
			case "json":
				data, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(stdout, string(data))
				return nil
			case "table", "":
				RenderStatsTable(stdout, report)
				return nil
			default:
				return fmt.Errorf("unsupported format %q, want table or json", format)
			}
		},
	}

	cmd.Flags().StringVar(&format, "format", "table", "output format: table or json")
	return cmd
}

func newStatsCmd(cfg *config, stdout io.Writer) *cobra.Command {
	return NewStatsCmd(stdout)
}

// CollectStats recursively inspects the given paths and aggregates coverage statistics.
func CollectStats(paths []string) (*StatsReport, error) {
	report := &StatsReport{
		Formats:          make(map[string]int),
		Operations:       make(map[string]int),
		ProtocolVersions: make(map[string]int),
	}

	seenFiles := make(map[string]bool)

	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			if isFixtureFile(p) && !seenFiles[p] {
				seenFiles[p] = true
				_ = analyzeFixtureFile(p, info.Size(), report)
			}
			continue
		}

		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				name := d.Name()
				if name == ".git" || name == "vendor" || name == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if isFixtureFile(path) && !seenFiles[path] {
				seenFiles[path] = true
				fileInfo, err := d.Info()
				if err != nil {
					return nil
				}
				_ = analyzeFixtureFile(path, fileInfo.Size(), report)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	if report.TotalFixtures > 0 {
		report.AverageSizeBytes = report.TotalBytes / int64(report.TotalFixtures)
	}

	return report, nil
}

func isFixtureFile(path string) bool {
	base := filepath.Base(path)
	if strings.HasPrefix(base, ".") {
		return false
	}
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".json") ||
		strings.HasSuffix(lower, ".json.gz") ||
		strings.HasSuffix(lower, ".yaml") ||
		strings.HasSuffix(lower, ".yml") ||
		strings.HasSuffix(lower, ".txt")
}

func fixtureFormat(path string) string {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".json") || strings.HasSuffix(lower, ".json.gz"):
		return "JSON"
	case strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml"):
		return "YAML"
	case strings.HasSuffix(lower, ".txt"):
		return "Text"
	default:
		return "Other"
	}
}

func analyzeFixtureFile(path string, size int64, report *StatsReport) error {
	fmtType := fixtureFormat(path)

	// Attempt golden.Load
	fixture, err := golden.Load(path)
	if err != nil {
		// If it's a golden file or inside testdata, still count format & size
		if strings.Contains(path, "testdata") || strings.Contains(path, "golden") || strings.Contains(path, "fixtures") {
			report.TotalFixtures++
			report.TotalBytes += size
			report.Formats[fmtType]++
		}
		return nil
	}

	report.TotalFixtures++
	report.TotalBytes += size
	report.Formats[fmtType]++

	if fixture.Captured != nil && fixture.Captured.Ledger > 0 {
		if fixture.Captured.Ledger >= 47123000 {
			report.ProtocolVersions["Protocol 21"]++
		} else {
			report.ProtocolVersions["Protocol 20"]++
		}
	}

	if fixture.Type == "TransactionEnvelope" && fixture.XDR != "" {
		var env xdr.TransactionEnvelope
		if err := xdr.SafeUnmarshalBase64(fixture.XDR, &env); err == nil {
			var ops []xdr.Operation
			hasSoroban := false
			switch env.Type {
			case xdr.EnvelopeTypeEnvelopeTypeTxV0:
				if env.V0 != nil {
					ops = env.V0.Tx.Operations
				}
			case xdr.EnvelopeTypeEnvelopeTypeTx:
				if env.V1 != nil {
					ops = env.V1.Tx.Operations
					if env.V1.Tx.Ext.V == 1 && env.V1.Tx.Ext.SorobanData != nil {
						hasSoroban = true
					}
				}
			case xdr.EnvelopeTypeEnvelopeTypeTxFeeBump:
				if env.FeeBump != nil && env.FeeBump.Tx.InnerTx.Type == xdr.EnvelopeTypeEnvelopeTypeTx && env.FeeBump.Tx.InnerTx.V1 != nil {
					ops = env.FeeBump.Tx.InnerTx.V1.Tx.Operations
					if env.FeeBump.Tx.InnerTx.V1.Tx.Ext.V == 1 && env.FeeBump.Tx.InnerTx.V1.Tx.Ext.SorobanData != nil {
						hasSoroban = true
					}
				}
			}
			for _, op := range ops {
				name := opStatsTypeName(op.Body.Type)
				report.Operations[name]++
			}
			if hasSoroban && fixture.Captured == nil {
				report.ProtocolVersions["Protocol 20"]++
			}
		}
	} else if fixture.Type == "LedgerCloseMeta" && fixture.XDR != "" {
		var lcm xdr.LedgerCloseMeta
		if err := xdr.SafeUnmarshalBase64(fixture.XDR, &lcm); err == nil {
			var version uint32
			switch lcm.V {
			case 0:
				if lcm.V0 != nil {
					version = uint32(lcm.V0.LedgerHeader.Header.LedgerVersion)
				}
			case 1:
				if lcm.V1 != nil {
					version = uint32(lcm.V1.LedgerHeader.Header.LedgerVersion)
				}
			case 2:
				if lcm.V2 != nil {
					version = uint32(lcm.V2.LedgerHeader.Header.LedgerVersion)
				}
			}
			if version > 0 {
				report.ProtocolVersions[fmt.Sprintf("Protocol %d", version)]++
			}
		}
	}

	return nil
}

func opStatsTypeName(t xdr.OperationType) string {
	switch t {
	case xdr.OperationTypePayment:
		return "Payment"
	case xdr.OperationTypeInvokeHostFunction:
		return "InvokeHostFunction"
	case xdr.OperationTypeManageData:
		return "ManageData"
	case xdr.OperationTypeCreateAccount:
		return "CreateAccount"
	case xdr.OperationTypeChangeTrust:
		return "ChangeTrust"
	case xdr.OperationTypeSetOptions:
		return "SetOptions"
	case xdr.OperationTypeBumpSequence:
		return "BumpSequence"
	case xdr.OperationTypeManageSellOffer:
		return "ManageSellOffer"
	case xdr.OperationTypeCreatePassiveSellOffer:
		return "CreatePassiveSellOffer"
	case xdr.OperationTypeManageBuyOffer:
		return "ManageBuyOffer"
	case xdr.OperationTypePathPaymentStrictReceive:
		return "PathPaymentStrictReceive"
	case xdr.OperationTypePathPaymentStrictSend:
		return "PathPaymentStrictSend"
	case xdr.OperationTypeCreateClaimableBalance:
		return "CreateClaimableBalance"
	case xdr.OperationTypeClaimClaimableBalance:
		return "ClaimClaimableBalance"
	case xdr.OperationTypeBeginSponsoringFutureReserves:
		return "BeginSponsoringFutureReserves"
	case xdr.OperationTypeEndSponsoringFutureReserves:
		return "EndSponsoringFutureReserves"
	case xdr.OperationTypeRevokeSponsorship:
		return "RevokeSponsorship"
	case xdr.OperationTypeClawback:
		return "Clawback"
	case xdr.OperationTypeClawbackClaimableBalance:
		return "ClawbackClaimableBalance"
	case xdr.OperationTypeSetTrustLineFlags:
		return "SetTrustLineFlags"
	case xdr.OperationTypeLiquidityPoolDeposit:
		return "LiquidityPoolDeposit"
	case xdr.OperationTypeLiquidityPoolWithdraw:
		return "LiquidityPoolWithdraw"
	case xdr.OperationTypeExtendFootprintTtl:
		return "ExtendFootprintTtl"
	case xdr.OperationTypeRestoreFootprint:
		return "RestoreFootprint"
	case xdr.OperationTypeInflation:
		return "Inflation"
	case xdr.OperationTypeAccountMerge:
		return "AccountMerge"
	case xdr.OperationTypeAllowTrust:
		return "AllowTrust"
	default:
		return strings.TrimPrefix(t.String(), "OperationType")
	}
}

// RenderStatsTable writes a formatted terminal table of the report.
func RenderStatsTable(w io.Writer, r *StatsReport) {
	fmt.Fprintf(w, "=== Fixture Coverage & Statistics ===\n\n")
	fmt.Fprintf(w, "Total Fixtures Analyzed: %d\n", r.TotalFixtures)
	fmt.Fprintf(w, "Total Disk Footprint:    %s (%d bytes)\n", formatByteSize(r.TotalBytes), r.TotalBytes)
	fmt.Fprintf(w, "Average Fixture Size:    %s (%d bytes)\n\n", formatByteSize(r.AverageSizeBytes), r.AverageSizeBytes)

	fmt.Fprintln(w, "Formats Breakdown:")
	if len(r.Formats) == 0 {
		fmt.Fprintln(w, "  (none)")
	} else {
		for _, name := range sortedMapKeys(r.Formats) {
			fmt.Fprintf(w, "  %-10s %d\n", name+":", r.Formats[name])
		}
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "Protocol Version Distribution:")
	if len(r.ProtocolVersions) == 0 {
		fmt.Fprintln(w, "  (none recorded)")
	} else {
		for _, proto := range sortedMapKeys(r.ProtocolVersions) {
			fmt.Fprintf(w, "  %-15s %d\n", proto+":", r.ProtocolVersions[proto])
		}
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "Operation Type Coverage Matrix:")
	if len(r.Operations) == 0 {
		fmt.Fprintln(w, "  (no operations found)")
	} else {
		for _, op := range sortedMapKeys(r.Operations) {
			fmt.Fprintf(w, "  %-30s %d\n", op+":", r.Operations[op])
		}
	}
}

func formatByteSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func sortedMapKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
