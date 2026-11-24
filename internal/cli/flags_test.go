package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stellar-optics/stellar-focus/pkg/capture"
)

// clearFocusEnv removes ambient FOCUS_* settings so a developer's shell
// cannot change what these tests observe. Viper ignores an empty environment
// value unless AllowEmptyEnv is enabled, so "" reads as unset.
func clearFocusEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"FOCUS_CONFIG",
		"FOCUS_NETWORK",
		"FOCUS_RPC_URL",
		"FOCUS_SOURCE",
		"FOCUS_TIMEOUT",
		"FOCUS_COLOR",
		"FOCUS_OUT",
		"FOCUS_PREFIX",
		"FOCUS_FORCE",
	} {
		t.Setenv(key, "")
	}
}

func TestConfigDefaults(t *testing.T) {
	clearFocusEnv(t)

	cfg := newConfig()
	if err := cfg.load(); err != nil {
		t.Fatalf("load() error = %v", err)
	}

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"network", cfg.network(), capture.NetworkTestnet},
		{"rpc URL", cfg.rpcURL(), ""},
		{"timeout", cfg.timeout(), 30 * time.Second},
		{"color", cfg.colorMode(), "auto"},
		{"out", cfg.outDir(), "testdata"},
		{"prefix", cfg.prefix(), ""},
		{"force", cfg.force(), false},
	}
	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}

func TestEnvOverridesDefaults(t *testing.T) {
	clearFocusEnv(t)
	t.Setenv("FOCUS_NETWORK", "mainnet")
	t.Setenv("FOCUS_RPC_URL", "https://rpc.example")
	t.Setenv("FOCUS_TIMEOUT", "45s")
	t.Setenv("FOCUS_COLOR", "never")
	t.Setenv("FOCUS_OUT", "fixtures")
	t.Setenv("FOCUS_PREFIX", "nightly")
	t.Setenv("FOCUS_FORCE", "true")

	cfg := newConfig()
	if err := cfg.load(); err != nil {
		t.Fatalf("load() error = %v", err)
	}

	if got := cfg.network(); got != "mainnet" {
		t.Errorf("network = %q, want mainnet", got)
	}
	if got := cfg.rpcURL(); got != "https://rpc.example" {
		t.Errorf("rpcURL = %q, want the environment value", got)
	}
	if got := cfg.timeout(); got != 45*time.Second {
		t.Errorf("timeout = %s, want 45s", got)
	}
	if got := cfg.colorMode(); got != "never" {
		t.Errorf("color = %q, want never", got)
	}
	if got := cfg.outDir(); got != "fixtures" {
		t.Errorf("out = %q, want fixtures", got)
	}
	if got := cfg.prefix(); got != "nightly" {
		t.Errorf("prefix = %q, want nightly", got)
	}
	if !cfg.force() {
		t.Error("force = false, want true")
	}
}

func TestFlagOverridesEnvironment(t *testing.T) {
	clearFocusEnv(t)
	t.Setenv("FOCUS_NETWORK", "venus")

	// The environment names an invalid network; the explicit flag is valid,
	// so the command must run.
	if _, _, err := run(t, "--network", "testnet", "version"); err != nil {
		t.Fatalf("version with a flag override: %v", err)
	}
}

func TestConfigFileOverridesDefaults(t *testing.T) {
	clearFocusEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "focus.yaml")
	writeFile(t, path, `
network: mainnet
rpc-url: https://rpc.example
timeout: 12s
color: never
out: captured
prefix: from_config
force: true
`)

	cfg := newConfig()
	cfg.v.Set(keyConfig, path)

	if err := cfg.load(); err != nil {
		t.Fatalf("load() error = %v", err)
	}

	if got := cfg.network(); got != "mainnet" {
		t.Errorf("network = %q, want mainnet", got)
	}
	if got := cfg.rpcURL(); got != "https://rpc.example" {
		t.Errorf("rpcURL = %q, want the config value", got)
	}
	if got := cfg.timeout(); got != 12*time.Second {
		t.Errorf("timeout = %s, want 12s", got)
	}
	if got := cfg.colorMode(); got != "never" {
		t.Errorf("color = %q, want never", got)
	}
	if got := cfg.outDir(); got != "captured" {
		t.Errorf("out = %q, want captured", got)
	}
	if got := cfg.prefix(); got != "from_config" {
		t.Errorf("prefix = %q, want from_config", got)
	}
	if !cfg.force() {
		t.Error("force = false, want true")
	}
}

func TestFlagOverridesConfigFile(t *testing.T) {
	clearFocusEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "focus.yaml")
	writeFile(t, path, "network: venus\n")

	// The config file is invalid on its own; the flag must win.
	if _, _, err := run(t, "--config", path, "--network", "testnet", "version"); err != nil {
		t.Fatalf("version with a config file and flag override: %v", err)
	}
}

func TestEnvironmentOverridesConfigFile(t *testing.T) {
	clearFocusEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "focus.yaml")
	writeFile(t, path, "network: venus\n")
	t.Setenv("FOCUS_CONFIG", path)
	t.Setenv("FOCUS_NETWORK", "testnet")

	if _, _, err := run(t, "version"); err != nil {
		t.Fatalf("version with an environment override: %v", err)
	}
}

func TestConfigFileSuppliesCaptureSettings(t *testing.T) {
	clearFocusEnv(t)
	src := sourceWithTx(t)
	withFake(t, src)

	out := filepath.Join(t.TempDir(), "captured")
	path := filepath.Join(t.TempDir(), "focus.yaml")
	writeFile(t, path, "out: "+out+"\nprefix: config\n")

	if _, _, err := run(t, "--config", path, "capture", "tx", testHash); err != nil {
		t.Fatalf("capture with config-supplied settings: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "config_envelope.golden.json")); err != nil {
		t.Errorf("config did not supply out and prefix: %v", err)
	}
}

func TestAutoDiscoveredConfigFile(t *testing.T) {
	clearFocusEnv(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".focus.yaml"), "network: venus\n")
	t.Chdir(dir)

	_, _, err := run(t, "version")
	if err == nil {
		t.Fatal("the discovered config file was ignored")
	}
	if !IsUsage(err) {
		t.Errorf("error does not classify as usage: %v", err)
	}

	writeFile(t, filepath.Join(dir, ".focus.yaml"), "network: testnet\n")
	if _, _, err := run(t, "version"); err != nil {
		t.Fatalf("valid discovered config: %v", err)
	}
}

func TestSnakeCaseRPCURLInConfig(t *testing.T) {
	clearFocusEnv(t)
	cfg := newConfig()
	cfg.v.Set(keyNetwork, "venus")
	cfg.v.Set("rpc_url", "https://rpc.example")
	cfg.v.Set(keyConfig, filepath.Join(t.TempDir(), "missing.yaml"))

	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() rejected a snake_case rpc_url: %v", err)
	}
	if got := cfg.rpcURL(); got != "https://rpc.example" {
		t.Errorf("rpcURL = %q, want the snake_case value", got)
	}
}

func TestExplicitConfigFileErrors(t *testing.T) {
	clearFocusEnv(t)

	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "missing",
			path: filepath.Join(t.TempDir(), "nope.yaml"),
			want: "reading configuration",
		},
		{
			name: "directory",
			path: t.TempDir(),
			want: "reading configuration",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := run(t, "--config", tc.path, "version")
			if err == nil {
				t.Fatal("an unusable --config path was accepted")
			}
			if !IsUsage(err) {
				t.Errorf("error does not classify as usage: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

func TestMalformedConfigFileIsRejected(t *testing.T) {
	clearFocusEnv(t)
	path := filepath.Join(t.TempDir(), "focus.yaml")
	writeFile(t, path, "network: [unterminated\n")

	_, _, err := run(t, "--config", path, "version")
	if err == nil {
		t.Fatal("malformed configuration was accepted")
	}
	if !IsUsage(err) {
		t.Errorf("error does not classify as usage: %v", err)
	}
}

func TestConfigValidation(t *testing.T) {
	clearFocusEnv(t)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "unknown network",
			args: []string{"--network", "venus", "version"},
			want: "unknown network",
		},
		{
			name: "empty network",
			args: []string{"--network", "", "version"},
			want: "network must not be empty",
		},
		{
			name: "non-positive timeout",
			args: []string{"--timeout", "0s", "version"},
			want: "timeout must be positive",
		},
		{
			name: "negative timeout",
			args: []string{"--timeout", "-1s", "version"},
			want: "timeout must be positive",
		},
		{
			name: "unknown colour",
			args: []string{"--color", "rainbow", "version"},
			want: "color must be",
		},
		{
			name: "empty out",
			args: []string{"capture", "tx", "hash", "--out", "", "--network", "testnet"},
			want: "out must not be empty",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := run(t, tc.args...)
			if err == nil {
				t.Fatalf("%v was accepted", tc.args)
			}
			if !IsUsage(err) {
				t.Errorf("error does not classify as usage: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

func TestExplicitRPCURLAllowsCustomNetwork(t *testing.T) {
	clearFocusEnv(t)

	// An explicit endpoint is how a local or private network is named.
	if _, _, err := run(t, "--network", "custom", "--rpc-url", "http://localhost:8000", "version"); err != nil {
		t.Fatalf("explicit rpc-url for a custom network: %v", err)
	}
}

func TestConfigFlagsAreAdvertised(t *testing.T) {
	clearFocusEnv(t)

	stdout, _, err := run(t, "--help")
	if err != nil {
		t.Fatalf("--help: %v", err)
	}
	for _, want := range []string{"--config", "--network", "--rpc-url", "--source", "--timeout", "--color"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help does not mention %s:\n%s", want, stdout)
		}
	}
}

// TestSourceSelectsTheEndpoint checks that the source decides which service
// the command talks to, since an RPC endpoint and a Horizon endpoint are
// different services.
func TestSourceSelectsTheEndpoint(t *testing.T) {
	clearFocusEnv(t)

	cfg := newConfig()
	if err := cfg.load(); err != nil {
		t.Fatalf("load() error = %v", err)
	}
	if got := cfg.source(); got != sourceRPC {
		t.Errorf("source = %q, want %q", got, sourceRPC)
	}
	endpoint, err := cfg.endpoint()
	if err != nil {
		t.Fatalf("endpoint() error = %v", err)
	}
	if endpoint != capture.DefaultEndpoints[capture.NetworkTestnet] {
		t.Errorf("endpoint = %q, want the RPC endpoint", endpoint)
	}

	cfg = newConfig()
	cfg.v.Set(keySource, sourceHorizon)
	if err := cfg.load(); err != nil {
		t.Fatalf("load() error = %v", err)
	}
	endpoint, err = cfg.endpoint()
	if err != nil {
		t.Fatalf("endpoint() error = %v", err)
	}
	if endpoint != capture.DefaultHorizonEndpoints[capture.NetworkTestnet] {
		t.Errorf("endpoint = %q, want the Horizon endpoint", endpoint)
	}
}

func TestUnknownSourceIsRejected(t *testing.T) {
	clearFocusEnv(t)

	_, _, err := run(t, "--source", "carrier-pigeon", "version")
	if err == nil {
		t.Fatal("an unknown source was accepted")
	}
	if !IsUsage(err) {
		t.Errorf("error does not classify as usage: %v", err)
	}
	if !strings.Contains(err.Error(), "source must be") {
		t.Errorf("error %q does not explain the mistake", err)
	}
}

// writeFile writes a test fixture and fails the test if it cannot.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
