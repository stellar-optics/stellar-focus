package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

func TestCaptureRecordsAnExplicitPassphrase(t *testing.T) {
	clearFocusEnv(t)
	withFake(t, sourceWithTx(t))
	dir := t.TempDir()

	const passphrase = "Standalone Network ; February 2017"
	_, _, err := run(t, "capture", "tx", testHash, "--out", dir,
		"--network", "standalone", "--rpc-url", "http://localhost:8000/rpc",
		"--passphrase", passphrase)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	f, err := golden.Load(filepath.Join(dir, "envelope.golden.json"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if f.Captured.NetworkPassphrase != passphrase {
		t.Errorf("networkPassphrase = %q, want %q", f.Captured.NetworkPassphrase, passphrase)
	}
}

func TestCaptureRecordsTheTestnetPassphraseByDefault(t *testing.T) {
	clearFocusEnv(t)
	withFake(t, sourceWithTx(t))
	dir := t.TempDir()

	if _, _, err := run(t, "capture", "tx", testHash, "--out", dir); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	f, err := golden.Load(filepath.Join(dir, "envelope.golden.json"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if f.Captured.NetworkPassphrase != "Test SDF Network ; September 2015" {
		t.Errorf("networkPassphrase = %q, want the testnet passphrase", f.Captured.NetworkPassphrase)
	}
}

func TestCaptureRejectsAPassphraseTheNetworkContradicts(t *testing.T) {
	clearFocusEnv(t)
	src := sourceWithTx(t)
	src.Passphrase = "Test SDF Network ; September 2015"
	withFake(t, src)

	_, _, err := run(t, "capture", "tx", testHash, "--out", t.TempDir(),
		"--passphrase", "Public Global Stellar Network ; September 2015")
	if err == nil || !strings.Contains(err.Error(), "passphrase mismatch") {
		t.Errorf("Execute() error = %v, want a passphrase mismatch", err)
	}
}
