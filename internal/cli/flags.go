// Configuration for the focus command.
//
// A flag, an environment variable and a config-file key are three spellings
// of one setting, and this file is the only place that decides which spelling
// wins. The precedence is the conventional one, highest first:
//
//	explicit flag  >  environment variable  >  config file  >  built-in default
//
// Keeping that order in one place means a new flag cannot accidentally invent
// its own precedence, and it means the values the commands use are resolved
// through Viper rather than read from pflag variables captured at definition
// time.
//
// Configuration is a CLI concern only: nothing in pkg/focus or pkg/golden
// imports this package, so the library's dependency promise is unaffected.
package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/stellar-optics/stellar-focus/pkg/capture"
)

const (
	// envPrefix is prepended to every setting when looking it up in the
	// environment, so --rpc-url becomes FOCUS_RPC_URL.
	envPrefix = "FOCUS"

	// configFlag is the persistent flag naming an explicit config file.
	configFlag = "config"
)

// Configuration keys. They are the flag names, so help text, environment
// variables and config files all use the same vocabulary.
//
// The one exception is keyOutFile: encode writes one file where capture
// writes a directory, so its --out is stored under a key of its own.
const (
	keyConfig      = "config"
	keyNetwork     = "network"
	keyRPCURL      = "rpc-url"
	keySource      = "source"
	keyTimeout     = "timeout"
	keyColor       = "color"
	keyOut         = "out"
	keyPrefix      = "prefix"
	keyForce       = "force"
	keyMask        = "mask-addresses"
	keyBatch       = "batch"
	keyConcurrency = "concurrency"
	// keyVerifyConcurrency is verify's own key. It shares the --concurrency
	// flag name with capture, but not the setting: capture's concurrency is
	// stored under "concurrency", and a command's flag must not read the
	// other's binding through Viper.
	keyVerifyConcurrency = "verify-concurrency"
	keyRate              = "rate"
	keyOutFile           = "out-file"
	keyType              = "type"
	keyDescription       = "description"
	keyPass              = "passphrase"
	keyFileSrc           = "file-source"
	keySanit             = "sanitize"
	keyMemo              = "mask-memo"
	keyData              = "scrub-data"
	keyKeys              = "scrub-keys"
	keyArchive           = "archive-url"
	keySince             = "since"
	keyFilter            = "filter"
	keyDriftedOnly       = "drifted-only"
	keyVerifyCache       = "verify-cache"
)

// The capture sources the command can talk to.
const (
	// sourceRPC captures from a Soroban RPC endpoint.
	sourceRPC = "rpc"
	// sourceHorizon captures from a Horizon REST endpoint.
	sourceHorizon = "horizon"
	// sourceFile replays XDR dumps from a local directory. It is chosen by
	// --file-source rather than --source, since it needs a directory.
	sourceFile = "file"
)

// Defaults, declared once so a flag and a config fallback cannot drift apart.
const (
	defaultNetwork     = capture.NetworkTestnet
	defaultRPCURL      = ""
	defaultSource      = sourceRPC
	defaultTimeout     = 30 * time.Second
	defaultColor       = "auto"
	defaultOutDir      = "testdata"
	defaultPrefix      = ""
	defaultForce       = false
	defaultMask        = false
	defaultPass        = ""
	defaultFileSrc     = ""
	defaultArchive     = ""
	defaultBatch       = ""
	defaultConcurrency = 4
	defaultRate        = 0
	defaultOutFile     = ""
	defaultType        = ""
	defaultDescription = ""
)

// defaultVerifyConcurrency is the number of fixtures verify checks at once
// when told nothing else: every core the machine has.
var defaultVerifyConcurrency = runtime.NumCPU()

// discoveredConfigFiles are checked, in order, when --config is not given.
// An absent file is not an error: most projects have no focus config at all.
var discoveredConfigFiles = []string{
	".focus.yaml",
	".focus.yml",
	".focus.json",
	".focus.toml",
}

// config resolves flags, environment variables and a config file into the
// values a command uses.
type config struct {
	v *viper.Viper
}

// newConfig returns a configuration with defaults and environment binding
// installed. It creates its own Viper instance rather than using the global
// one, so tests can run independently and a library importing this package
// could never share state with another command.
func newConfig() *config {
	v := viper.New()
	v.SetEnvPrefix(envPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()

	v.SetDefault(keyNetwork, defaultNetwork)
	v.SetDefault(keyRPCURL, defaultRPCURL)
	v.SetDefault(keySource, defaultSource)
	v.SetDefault(keyTimeout, defaultTimeout)
	v.SetDefault(keyColor, defaultColor)
	v.SetDefault(keyOut, defaultOutDir)
	v.SetDefault(keyPrefix, defaultPrefix)
	v.SetDefault(keyForce, defaultForce)
	v.SetDefault(keyConcurrency, defaultConcurrency)
	v.SetDefault(keyVerifyConcurrency, defaultVerifyConcurrency)
	v.SetDefault(keyRate, defaultRate)
	v.SetDefault(keyPass, defaultPass)
	v.SetDefault(keyFileSrc, defaultFileSrc)
	v.SetDefault(keyArchive, defaultArchive)

	return &config{v: v}
}

// bindRootFlags declares the flags every command accepts and binds them to
// their configuration keys.
func (c *config) bindRootFlags(root *cobra.Command) {
	flags := root.PersistentFlags()

	flags.String(configFlag, "",
		fmt.Sprintf("read settings from this file instead of %s", discoveredConfigFiles[0]))
	flags.String(keyNetwork, defaultNetwork,
		"network to talk to: testnet or mainnet")
	flags.String(keyRPCURL, defaultRPCURL,
		"endpoint override, for the source in use")
	flags.String(keySource, defaultSource,
		"where to capture from: rpc or horizon")
	flags.String(keyFileSrc, defaultFileSrc,
		"read XDR dumps from this directory instead of the network")
	flags.String(keyArchive, defaultArchive,
		"history archive to fall back to for transactions older than the source keeps")
	flags.Duration(keyTimeout, defaultTimeout,
		"timeout for a single RPC call")
	flags.String(keyColor, defaultColor,
		`when to colourise output: "auto", "always" or "never"`)
	flags.String(keyPass, defaultPass,
		"network passphrase, for a network focus cannot name or a source that does not report one")

	c.bind(flags, configFlag)
	c.bind(flags, keyNetwork)
	c.bind(flags, keyRPCURL)
	c.bind(flags, keySource)
	c.bind(flags, keyFileSrc)
	c.bind(flags, keyArchive)
	c.bind(flags, keyTimeout)
	c.bind(flags, keyColor)
	c.bind(flags, keyPass)
}

// bindCaptureFlags declares the flags shared by both capture subcommands.
// They live on the capture parent so --out, --prefix and --force mean the
// same thing, and are bound once, whichever subcommand runs.
func (c *config) bindCaptureFlags(cmd *cobra.Command) {
	flags := cmd.PersistentFlags()

	flags.StringP(keyOut, "o", defaultOutDir,
		"directory to write fixture files into")
	flags.String(keyPrefix, defaultPrefix,
		"filename prefix; defaults to the record name")
	flags.Bool(keyForce, defaultForce,
		"overwrite fixture files that already exist")
	flags.Bool(keyMask, defaultMask,
		"replace public account addresses with deterministic mock accounts")
	flags.Bool(keySanit, false,
		"mask sensitive data: the same as --mask-memo --scrub-data --scrub-keys")
	flags.Bool(keyMemo, false,
		"replace text memos with "+capture.RedactedMemo)
	flags.Bool(keyData, false,
		"replace data entry values with "+capture.RedactedData)
	flags.Bool(keyKeys, false,
		"replace anything shaped like a secret key (S...) with "+capture.RedactedSecretKey)

	c.bind(flags, keyOut)
	c.bind(flags, keyPrefix)
	c.bind(flags, keyForce)
	c.bind(flags, keyMask)
	c.bind(flags, keySanit)
	c.bind(flags, keyMemo)
	c.bind(flags, keyData)
	c.bind(flags, keyKeys)
}

// bindVerifyFlags declares the flags that narrow what a verify run checks.
//
// They are the reason a repository with hundreds of fixtures can still be
// verified on every commit: most of them are old, and the ones that matter
// are the ones that drifted.
func (c *config) bindVerifyFlags(cmd *cobra.Command) {
	flags := cmd.Flags()

	flags.String(keySince, "",
		"only check fixtures captured after this duration (48h) or timestamp (2026-10-01T00:00:00Z)")
	flags.String(keyFilter, "",
		"only check fixtures whose filename matches this glob, or whose metadata matches key:value")
	flags.Bool(keyDriftedOnly, false,
		"only re-check fixtures that previously drifted or could not be checked")
	flags.IntP(keyConcurrency, "j", defaultVerifyConcurrency,
		"how many fixtures may be checked at once")

	c.bind(flags, keySince)
	c.bind(flags, keyFilter)
	c.bind(flags, keyDriftedOnly)
	c.bindAs(flags, keyVerifyConcurrency, keyConcurrency)
}

// bindBatchFlags declares the flags a batch capture accepts.
//
// They live on the transaction subcommand rather than on the capture parent,
// because only a transaction can be captured in bulk.
func (c *config) bindBatchFlags(cmd *cobra.Command) {
	flags := cmd.Flags()

	flags.String(keyBatch, defaultBatch,
		"capture every transaction hash listed in this file instead of one hash")
	flags.Int(keyConcurrency, defaultConcurrency,
		"how many captures may be in flight at once")
	flags.Duration(keyRate, defaultRate,
		"minimum time between two captures, for endpoints that rate-limit")

	c.bind(flags, keyBatch)
	c.bind(flags, keyConcurrency)
	c.bind(flags, keyRate)
}

// bindEncodeFlags declares the flags the encode command accepts.
//
// Its output flag is called --out, like capture's, but it names one file
// rather than a directory, so it is bound to a key of its own: "out" means
// the directory capture writes into, and "out-file" the file encode writes.
func (c *config) bindEncodeFlags(cmd *cobra.Command) {
	flags := cmd.Flags()

	flags.String(keyOut, defaultOutFile,
		"file to write the fixture to; standard output when not given")
	flags.String(keyType, defaultType,
		"XDR type of the payload; detected when not given")
	flags.String(keyDescription, defaultDescription,
		"note recorded in the fixture")

	c.bindAs(flags, keyOutFile, keyOut)
	c.bind(flags, keyType)
	c.bind(flags, keyDescription)
}

// bindAs attaches a declared flag to Viper under a key of its own.
//
// Two commands can spell a setting the same way and mean different things
// by it, so the key a flag is stored under may differ from its name.
func (c *config) bindAs(flags *pflag.FlagSet, key, name string) {
	if err := c.v.BindPFlag(key, flags.Lookup(name)); err != nil {
		panic(fmt.Sprintf("focus: binding --%s: %v", name, err))
	}
}

// bind attaches a declared flag to Viper. A missing flag is a programming
// error in this package, not user input, so it fails fast at start-up.
func (c *config) bind(flags *pflag.FlagSet, key string) {
	if err := c.v.BindPFlag(key, flags.Lookup(key)); err != nil {
		panic(fmt.Sprintf("focus: binding --%s: %v", key, err))
	}
}

// load reads the config file and validates the resolved values. It runs as a
// persistent pre-run hook, after cobra has parsed the flags.
func (c *config) load() error {
	if err := c.readConfig(); err != nil {
		return err
	}
	return c.validate()
}

// readConfig loads an explicit config file, or the first conventional one in
// the working directory. A missing conventional file is not an error.
func (c *config) readConfig() error {
	path := strings.TrimSpace(c.v.GetString(keyConfig))
	if path == "" {
		found, err := c.findConfigFile()
		if err != nil {
			return err
		}
		if found == "" {
			return nil
		}
		path = found
	}

	c.v.SetConfigFile(path)
	if err := c.v.ReadInConfig(); err != nil {
		return usageErrorf("reading configuration %s: %v", path, err)
	}
	return nil
}

// findConfigFile returns the first conventional config file that exists, or
// an empty path when there is none.
func (c *config) findConfigFile() (string, error) {
	for _, name := range discoveredConfigFiles {
		info, err := os.Stat(name)
		switch {
		case err == nil && info.IsDir():
			return "", usageErrorf("configuration path %s is a directory, not a file", name)
		case err == nil:
			return name, nil
		case errors.Is(err, fs.ErrNotExist):
			continue
		default:
			return "", usageErrorf("reading configuration %s: %v", name, err)
		}
	}
	return "", nil
}

// validate rejects configuration that would fail later, before any command
// has done work or contacted a network.
func (c *config) validate() error {
	if c.network() == "" {
		return usageErrorf("network must not be empty")
	}
	switch c.source() {
	case sourceRPC, sourceHorizon:
	case sourceFile:
		dir := c.fileSource()
		if dir == "" {
			return usageErrorf("the file source needs a directory; give it with --file-source <dir>")
		}
		info, err := os.Stat(dir)
		if err != nil {
			return usageErrorf("file source: %v", err)
		}
		if !info.IsDir() {
			return usageErrorf("file source %s is not a directory", dir)
		}
	default:
		return usageErrorf(`source must be %q or %q, got %q`, sourceRPC, sourceHorizon, c.source())
	}
	if _, err := c.endpoint(); err != nil {
		return usageErrorf("%v", err)
	}
	if c.timeout() <= 0 {
		return usageErrorf("timeout must be positive, got %s", c.timeout())
	}
	switch c.colorMode() {
	case "auto", "always", "never":
	default:
		return usageErrorf(`color must be "auto", "always" or "never", got %q`, c.colorMode())
	}
	if c.outDir() == "" {
		return usageErrorf("out must not be empty")
	}
	if c.concurrency() < 1 {
		return usageErrorf("concurrency must be at least 1, got %d", c.concurrency())
	}
	if c.verifyConcurrency() < 1 {
		return usageErrorf("concurrency must be at least 1, got %d", c.verifyConcurrency())
	}
	if c.rate() < 0 {
		return usageErrorf("rate must not be negative, got %s", c.rate())
	}
	return nil
}

// network is the network name, from flag, environment or config.
func (c *config) network() string {
	return strings.TrimSpace(c.v.GetString(keyNetwork))
}

// source is the capture source: rpc or horizon, or file when --file-source
// names a directory of dumps.
func (c *config) source() string {
	if c.fileSource() != "" {
		return sourceFile
	}
	return strings.TrimSpace(c.v.GetString(keySource))
}

// fileSource is the directory of XDR dumps to read instead of the network.
func (c *config) fileSource() string {
	return strings.TrimSpace(c.v.GetString(keyFileSrc))
}

// endpoint is the address the capture source talks to.
//
// The default depends on the source: a Soroban RPC endpoint and a Horizon
// endpoint are different services, and pointing one at the other fails in a
// way that is not obvious from the error.
func (c *config) endpoint() (string, error) {
	switch c.source() {
	case sourceHorizon:
		return capture.ResolveHorizonEndpoint(c.network(), c.rpcURL())
	case sourceRPC:
		return capture.ResolveEndpoint(c.network(), c.rpcURL())
	case sourceFile:
		return c.fileSource(), nil
	default:
		return "", fmt.Errorf("unknown source %q", c.source())
	}
}

// rpcURL is the endpoint override. The snake_case spelling is accepted so a
// config file written in Go-ish YAML does not silently lose the setting.
func (c *config) rpcURL() string {
	if url := strings.TrimSpace(c.v.GetString(keyRPCURL)); url != "" {
		return url
	}
	return strings.TrimSpace(c.v.GetString("rpc_url"))
}

// passphrase is the explicit network passphrase, empty when the source or
// the network name is to decide it.
func (c *config) passphrase() string {
	return strings.TrimSpace(c.v.GetString(keyPass))
}

// archiveURL is the history archive to fall back to, empty for none.
func (c *config) archiveURL() string {
	return strings.TrimSpace(c.v.GetString(keyArchive))
}

// timeout bounds a single RPC call.
func (c *config) timeout() time.Duration {
	return c.v.GetDuration(keyTimeout)
}

// colorMode is the colour policy passed to lens's formatter.
func (c *config) colorMode() string {
	return c.v.GetString(keyColor)
}

// outDir is where capture writes fixtures.
func (c *config) outDir() string {
	return c.v.GetString(keyOut)
}

// prefix is prepended to captured fixture filenames.
func (c *config) prefix() string {
	return c.v.GetString(keyPrefix)
}

// force permits capture to overwrite existing fixtures.
func (c *config) force() bool {
	return c.v.GetBool(keyForce)
}

// since is the threshold a fixture's capture time must be newer than.
//
// It is either a duration counted back from now, or an RFC3339 timestamp.
// An empty string means no threshold.
func (c *config) since() string {
	return strings.TrimSpace(c.v.GetString(keySince))
}

// filter narrows a verify run to the fixtures it should check.
//
// It is either a glob over the filename, or a metadata selector of the form
// "key:value".
func (c *config) filter() string {
	return strings.TrimSpace(c.v.GetString(keyFilter))
}

// driftedOnly reports whether a verify run should re-check only the fixtures
// that previously drifted or could not be checked.
func (c *config) driftedOnly() bool {
	return c.v.GetBool(keyDriftedOnly)
}

// verifyCachePath is where the verify command keeps what its last run found.
//
// It sits beside the capture cache, under .focus, so one directory name
// covers every local artifact the tool writes.
func (c *config) verifyCachePath() string {
	if path := strings.TrimSpace(c.v.GetString(keyVerifyCache)); path != "" {
		return path
	}
	return defaultVerifyCachePath
}

// maskAddresses replaces real account addresses in captured fixtures with
// deterministic mock accounts.
func (c *config) maskAddresses() bool {
	return c.v.GetBool(keyMask)
}

// batch is the file listing the transaction hashes to capture, or "-" for
// standard input. An empty value means no batch.
func (c *config) batch() string {
	return strings.TrimSpace(c.v.GetString(keyBatch))
}

// concurrency is how many captures may be in flight at once.
func (c *config) concurrency() int {
	return c.v.GetInt(keyConcurrency)
}

// verifyConcurrency is how many fixtures a verify run checks at once.
func (c *config) verifyConcurrency() int {
	return c.v.GetInt(keyVerifyConcurrency)
}

// rate is the minimum time between two captures.
func (c *config) rate() time.Duration {
	return c.v.GetDuration(keyRate)
}

// outFile is the file the encode command writes its fixture to. An empty
// value means standard output.
func (c *config) outFile() string {
	return strings.TrimSpace(c.v.GetString(keyOutFile))
}

// encodeType is the XDR type of the payload being encoded. An empty value
// means the type is detected.
func (c *config) encodeType() string {
	return strings.TrimSpace(c.v.GetString(keyType))
}

// encodeDescription is the note recorded in the fixture being written.
func (c *config) encodeDescription() string {
	return c.v.GetString(keyDescription)
}

// sanitizer is the masking capture applies before writing fixtures.
// --sanitize turns every mask on; the individual flags pick some of them.
func (c *config) sanitizer() capture.Sanitizer {
	all := c.v.GetBool(keySanit)
	return capture.Sanitizer{
		MaskMemo:  all || c.v.GetBool(keyMemo),
		ScrubData: all || c.v.GetBool(keyData),
		ScrubKeys: all || c.v.GetBool(keyKeys),
	}
}
