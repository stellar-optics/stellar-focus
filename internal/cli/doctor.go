package cli

// The doctor command: check that the local environment can run focus.
//
// A contributor whose first run fails should be told which piece is wrong —
// the toolchain, the endpoint, the cache directory, a missing companion
// binary — rather than left to infer it from a stack trace. The checks are
// read-only apart from creating the cache directory, which is the directory
// the tool would create on its first capture anyway.

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/stellar-optics/stellar-focus/pkg/capture"
)

// minimumGoMinor is the oldest Go release focus supports: 1.22.
const minimumGoMinor = 22

// doctorCheck is the outcome of one check.
type doctorCheck struct {
	// OK is whether the check passed.
	OK bool
	// Label names the check, e.g. "Go Runtime".
	Label string
	// Detail says what was found.
	Detail string
	// Optional marks a check that warns but does not fail the run.
	Optional bool
}

// newDoctorCmd returns the cobra command for `focus doctor`.
func newDoctorCmd(cfg *config, stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Validate the local development environment and CLI dependencies",
		Long: `Check that the local environment can capture and verify fixtures.

The checks are:

  - Go Runtime: this binary was built with Go 1.22 or newer.
  - Soroban RPC: the configured RPC endpoint answers over HTTPS.
  - Horizon: the configured Horizon endpoint answers over HTTPS.
  - Cache Directory: .focus/cache exists and is writable.
  - Companion tools: stellar-xdr-lens and the gh CLI are on PATH. These are
    optional and only improve diffs and issue links.

Exit status is 0 when every required check passes and 1 when one fails.`,
		Example: `  focus doctor
  focus doctor --network mainnet
  focus doctor --rpc-url http://localhost:8000/rpc`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			checks := []doctorCheck{
				checkGoRuntime(),
				checkRPCEndpoint(cfg),
				checkHorizonEndpoint(cfg),
				checkCacheDirectory(),
			}
			checks = append(checks, checkCompanionTools()...)

			var failed int
			for _, check := range checks {
				mark := "[✓]"
				switch {
				case check.OK:
				case check.Optional:
					mark = "[!]"
				default:
					mark = "[✗]"
					failed++
				}
				fmt.Fprintf(stdout, "%s %s: %s\n", mark, check.Label, check.Detail)
			}

			passed := len(checks) - failed
			fmt.Fprintf(stdout, "\n%d checks passed, %d failed\n", passed, failed)
			if failed > 0 {
				return failureErrorf("%d check(s) failed", failed)
			}
			return nil
		},
	}
}

// checkGoRuntime reports the Go version this binary was built with.
//
// The version is read from the runtime rather than by running `go version`,
// so doctor still works when the toolchain is not on PATH — which is one of
// the things it is meant to diagnose.
func checkGoRuntime() doctorCheck {
	version := runtime.Version()
	check := doctorCheck{Label: "Go Runtime", Detail: fmt.Sprintf("%s %s/%s", version, runtime.GOOS, runtime.GOARCH)}

	minor, ok := goMinorVersion(version)
	if !ok {
		check.Detail += " (could not parse the version)"
		return check
	}
	if minor < minimumGoMinor {
		check.Detail += fmt.Sprintf(" (need Go 1.%d or newer)", minimumGoMinor)
		return check
	}
	check.OK = true
	return check
}

// goMinorVersion extracts the minor number from a Go version string such as
// "go1.22.5" or "go1.25.0".
func goMinorVersion(version string) (int, bool) {
	version = strings.TrimPrefix(version, "go")
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return 0, false
	}
	if major, err := strconv.Atoi(parts[0]); err != nil || major < 1 {
		return 0, false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, false
	}
	return minor, true
}

// checkRPCEndpoint reports whether the Soroban RPC endpoint answers.
func checkRPCEndpoint(cfg *config) doctorCheck {
	override := ""
	if cfg.source() == sourceRPC {
		override = cfg.rpcURL()
	}
	endpoint, err := capture.ResolveEndpoint(cfg.network(), override)
	if err != nil {
		return doctorCheck{Label: "Soroban RPC", Detail: err.Error()}
	}
	return probeEndpoint("Soroban RPC", endpoint, cfg.timeout())
}

// checkHorizonEndpoint reports whether the Horizon endpoint answers.
func checkHorizonEndpoint(cfg *config) doctorCheck {
	override := ""
	if cfg.source() == sourceHorizon {
		override = cfg.rpcURL()
	}
	endpoint, err := capture.ResolveHorizonEndpoint(cfg.network(), override)
	if err != nil {
		return doctorCheck{Label: "Horizon", Detail: err.Error()}
	}
	return probeEndpoint("Horizon", endpoint, cfg.timeout())
}

// probeEndpoint performs one HTTPS request and reports the latency.
//
// The request goes through the default transport, so TLS certificate
// verification and proxy settings are exactly the ones a capture and a
// verify would use: doctor must diagnose the environment the tool actually
// runs in, not a simplified one.
func probeEndpoint(label, endpoint string, timeout time.Duration) doctorCheck {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	client := &http.Client{Timeout: timeout}
	start := time.Now()
	resp, err := client.Get(endpoint + "/.well-known/stellar")
	latency := time.Since(start)
	if err != nil {
		return doctorCheck{Label: label, Detail: fmt.Sprintf("%s unreachable: %v", endpoint, err)}
	}
	defer resp.Body.Close()

	detail := fmt.Sprintf("%s (latency: %s)", endpoint, latency.Round(time.Millisecond))
	if resp.StatusCode >= 500 {
		return doctorCheck{Label: label, Detail: detail + fmt.Sprintf(" (HTTP %d)", resp.StatusCode)}
	}
	return doctorCheck{Label: label, Detail: detail, OK: true}
}

// checkCacheDirectory reports whether .focus/cache exists and can be written.
//
// It lives in the working directory, beside the verify cache, so a project
// keeps its local artifacts together rather than scattering them under the
// home directory.
func checkCacheDirectory() doctorCheck {
	cacheDir := filepath.Join(".focus", "cache")

	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return doctorCheck{Label: "Cache Directory", Detail: fmt.Sprintf("%s is not writable: %v", absolutePath(cacheDir), err)}
	}

	probe, err := os.CreateTemp(cacheDir, ".doctor-*")
	if err != nil {
		return doctorCheck{Label: "Cache Directory", Detail: fmt.Sprintf("%s is not writable: %v", absolutePath(cacheDir), err)}
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)

	return doctorCheck{Label: "Cache Directory", Detail: absolutePath(cacheDir) + " (writable)", OK: true}
}

// checkCompanionTools reports the optional tools that improve the CLI.
func checkCompanionTools() []doctorCheck {
	return []doctorCheck{
		optionalTool("stellar-xdr-lens", "enhanced diffs"),
		optionalTool("gh", "pull-request links"),
	}
}

// optionalTool reports whether a companion binary is on PATH.
func optionalTool(name, purpose string) doctorCheck {
	label := "Optional: " + name
	if path, err := exec.LookPath(name); err == nil {
		return doctorCheck{Label: label, Detail: path, OK: true, Optional: true}
	}
	return doctorCheck{
		Label:    label,
		Detail:   fmt.Sprintf("not found on PATH (optional, for %s)", purpose),
		Optional: true,
	}
}

// absolutePath renders a path absolutely for display, falling back to the
// path itself when the working directory cannot be read.
func absolutePath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}
