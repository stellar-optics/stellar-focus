package focus

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
)

// propertyIterations is the number of generated inputs per property. A fixed
// seed keeps a failure reproducible: the same run order produces the same
// input, so a report can be replayed without a corpus file.
const propertyIterations = 1000

// propertyRunes is the alphabet the string generator draws from: ASCII,
// multi-byte runes, combining marks and the control characters a fixture or a
// command line can carry.
var propertyRunes = []rune{
	'a', 'Z', '0', '-', '_', '=', '/', '+', ' ',
	'\n', '\r', '\t', 0x00, 0x1f,
	'é', 'ß', '世', '🔥', 0x200d, 0xfeff,
}

// TestPropertyCoerceNeverPanicsAndIsDeterministic feeds generated inputs
// through coerce, the single funnel every assertion uses to turn a Value into
// decoded XDR. The contract is twofold: no input panics, and the same input
// always produces the same output, so an assertion cannot pass or fail
// depending on how many times it has run.
func TestPropertyCoerceNeverPanicsAndIsDeterministic(t *testing.T) {
	inputs := propertyInputs(t)

	for _, v := range inputs {
		first, firstErr := mustNotPanic(t, v)
		second, secondErr := mustNotPanic(t, v)

		if (firstErr == nil) != (secondErr == nil) {
			t.Fatalf("coerce(%T) disagreed with itself about success", v)
		}
		if firstErr != nil {
			if firstErr.Error() != secondErr.Error() {
				t.Fatalf("coerce(%T) produced different errors: %q then %q", v, firstErr, secondErr)
			}
			continue
		}
		if !bytes.Equal(first, second) {
			t.Fatalf("coerce(%T) is not deterministic: %s then %s", v, first, second)
		}
	}
}

// TestPropertyAssertionsNeverPanic feeds the same generated inputs through the
// exported assertions. A panic here would destroy the output of every other
// test in the caller's package, which is the failure mode this library exists
// to avoid.
func TestPropertyAssertionsNeverPanic(t *testing.T) {
	for _, v := range propertyInputs(t) {
		checkNoPanic(t, "AssertEqual", func(tb TB) { AssertEqual(tb, v, v) })
		checkNoPanic(t, "AssertEqualAs", func(tb TB) { AssertEqualAs(tb, "TransactionEnvelope", v, v) })
		checkNoPanic(t, "AssertEnvelopeEqual", func(tb TB) { AssertEnvelopeEqual(tb, v, v) })
		checkNoPanic(t, "AssertResultEqual", func(tb TB) { AssertResultEqual(tb, v, v) })
	}
}

// propertyInputs returns values of every shape an assertion accepts, plus the
// hostile ones it has to survive: empty and malformed base64, arbitrary
// unicode, random bytes, and valid XDR with random corruption applied.
func propertyInputs(t *testing.T) []Value {
	t.Helper()

	valid := validEnvelopeData(t)
	r := rand.New(rand.NewSource(1))

	inputs := []Value{
		nil,
		"",
		"    ",
		"not base64!",
		"AAAA",
		strings.Repeat("A", 16),
		[]byte{},
		[]byte{0xff, 0x00, 0x01},
	}

	for i := 0; i < propertyIterations; i++ {
		inputs = append(inputs,
			randomBytes(r),
			randomString(r),
			mutatedEnvelope(r, valid),
			base64.StdEncoding.EncodeToString(randomBytes(r)),
		)
	}
	return inputs
}

// randomBytes returns up to 64 arbitrary bytes.
func randomBytes(r *rand.Rand) []byte {
	b := make([]byte, r.Intn(64))
	for i := range b {
		b[i] = byte(r.Intn(256))
	}
	return b
}

// randomString returns up to 40 runes from the property alphabet. The result
// is not always valid UTF-8, which is deliberate: a caller can hand an
// assertion a Go string built from arbitrary bytes.
func randomString(r *rand.Rand) string {
	var b strings.Builder
	for i := 0; i < r.Intn(40); i++ {
		b.WriteRune(propertyRunes[r.Intn(len(propertyRunes))])
	}
	return b.String()
}

// mutatedEnvelope corrupts a real envelope the way a truncated read or a
// wrong offset would: a flipped byte, a truncation, a splice, or a zeroed run.
func mutatedEnvelope(r *rand.Rand, valid []byte) []byte {
	out := append([]byte(nil), valid...)
	if len(out) == 0 {
		return out
	}

	switch r.Intn(4) {
	case 0:
		for i := 0; i < 1+r.Intn(4); i++ {
			out[r.Intn(len(out))] ^= byte(1 + r.Intn(255))
		}
	case 1:
		out = out[:r.Intn(len(out))]
	case 2:
		at := r.Intn(len(out))
		out = append(out[:at], append(randomBytes(r), out[at:]...)...)
	case 3:
		at := r.Intn(len(out))
		end := at + r.Intn(len(out)-at)
		for i := at; i < end; i++ {
			out[i] = 0
		}
	}
	return out
}

// validEnvelopeData reads the repository's own envelope fixture, so the
// mutated inputs are corruptions of a payload that really decodes.
func validEnvelopeData(t *testing.T) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("..", "fuzz", "testdata", "tx_failed.env.txt"))
	if err != nil {
		t.Fatalf("reading the test envelope: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("decoding the test envelope: %v", err)
	}
	return decoded
}

// mustNotPanic runs coerce on v inside a recover, returning the JSON rendering
// of the result so two calls can be compared.
func mustNotPanic(t *testing.T, v Value) (out []byte, err error) {
	t.Helper()

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("coerce panicked on %T: %v", v, recovered)
		}
	}()

	value, err := coerce(v, "")
	if err != nil {
		return nil, err
	}
	encoded, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		return nil, marshalErr
	}
	return encoded, nil
}

// checkNoPanic runs one assertion on a recording TB, reporting a panic as a
// failure of the property rather than a crash of the test binary.
func checkNoPanic(t *testing.T, name string, assert func(TB)) {
	t.Helper()

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("%s panicked: %v", name, recovered)
		}
	}()
	assert(faketb.New())
}
