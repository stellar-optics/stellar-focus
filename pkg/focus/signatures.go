package focus

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
)

// Signature assertions check which signers attached a signature to a
// transaction, which is the question a multisig test is really asking: not how
// many signatures there are, but whether the key that was supposed to sign is
// among them.
//
// # What "present" means
//
// A Stellar signature is decorated: a four-byte hint plus the signature bytes.
// The hint is all the envelope says about who signed — by convention the last
// four bytes of the signer's public key, of the pre-auth transaction hash, or
// of the hash of the preimage for an X signer. The public key itself is not in
// the envelope, and verifying the signature cryptographically would need the
// network passphrase and the signed payload, so these assertions match hints.
// That is the same attribution rule the protocol applies when it looks for a
// signer's signature, and for the ed25519 case a hint collision across
// unrelated keys is a 1-in-2^32 accident.
//
// Anything that gets a signature into a test is offline data, so this is
// deterministic and makes no network calls.

// AssertSignaturesPresent checks that every expected signer attached a
// signature to the envelope.
//
// expectedPubKeys accepts the signer forms a test holds: a strkey address —
// "G…" for an ed25519 key, and the "T…", "X…" and "P…" forms for pre-auth-tx,
// hash-x and signed-payload signers — an xdr.AccountId, an xdr.MuxedAccount,
// an xdr.SignerKey, or a decoded *lens.Value holding one of those.
//
// For a fee-bump envelope both signature sets count: the fee-bump source
// signs the outer transaction and the inner signers sign the inner one, and a
// test usually wants to check one or the other without having to know which
// set the envelope puts it in. The failure message says which set each
// signature came from.
//
// An M-of-N arrangement is asserted by listing the M signers that are
// required; AssertSignatureThreshold checks a lower bound when the individual
// signers are not fixed in advance.
func AssertSignaturesPresent(t TB, envelope Value, expectedPubKeys ...Value) bool {
	t.Helper()
	const assertion = "AssertSignaturesPresent"
	return assertSignaturesPresent(t, assertion, envelope, len(expectedPubKeys), expectedPubKeys)
}

// AssertSignatureThreshold checks that at least threshold of the expected
// signers attached a signature to the envelope.
//
// It is the M-of-N case where the individual signers are not fixed: a 2-of-3
// multisig account has satisfied its threshold when any two of the three keys
// have signed, whichever they are. AssertSignaturesPresent is the stricter
// all-of form.
//
// expectedPubKeys accepts the same forms as AssertSignaturesPresent, and
// threshold must be between 1 and the number of expected signers.
func AssertSignatureThreshold(t TB, envelope Value, threshold int, expectedPubKeys ...Value) bool {
	t.Helper()
	const assertion = "AssertSignatureThreshold"

	if threshold < 1 {
		t.Errorf("%s: threshold is %d; at least 1 expected signer must sign", assertion, threshold)
		return false
	}
	if threshold > len(expectedPubKeys) {
		t.Errorf("%s: threshold is %d but only %d expected signer(s) were given",
			assertion, threshold, len(expectedPubKeys))
		return false
	}
	return assertSignaturesPresent(t, assertion, envelope, threshold, expectedPubKeys)
}

// AssertSignatureHints checks that the envelope carries a signature for each
// of the wanted hints.
//
// A Stellar signature is decorated: it carries a four-byte hint plus the
// signature bytes. The hint is all the envelope says about who signed — by
// convention the last four bytes of the signer's public key — so a test that
// holds the hint can check that a particular signer signed without holding
// the key itself.
//
// AssertSignedBy is the same check for a signer named by its public key.
func AssertSignatureHints(t TB, envelope Value, wantHints ...[4]byte) bool {
	t.Helper()
	const assertion = "AssertSignatureHints"

	if len(wantHints) == 0 {
		t.Errorf("%s: no signature hints were given; pass at least one hint", assertion)
		return false
	}

	want := make([]expectedSigner, 0, len(wantHints))
	for _, h := range wantHints {
		want = append(want, expectedSigner{
			label: hex.EncodeToString(h[:]),
			hint:  xdr.SignatureHint(h),
		})
	}
	return assertHintsPresent(t, assertion, envelope, want, len(want), "hint")
}

// AssertSignedBy checks that the envelope carries a signature from each of
// the wanted public keys.
//
// wantPublicKeys are strkey addresses — the "G…" form of an ed25519 account,
// and the "T…", "X…" and "P…" forms of the other signer kinds — and each
// signature is recognised by the hint the protocol derives from its key.
//
// AssertSignaturesPresent accepts the same keys along with more forms; this
// one takes strkeys only, so what a test passes is checked by the compiler.
func AssertSignedBy(t TB, envelope Value, wantPublicKeys ...string) bool {
	t.Helper()
	const assertion = "AssertSignedBy"

	if len(wantPublicKeys) == 0 {
		t.Errorf("%s: no public keys were given; pass at least one signer", assertion)
		return false
	}

	want := make([]expectedSigner, 0, len(wantPublicKeys))
	for i, key := range wantPublicKeys {
		signer, err := expectedSignerFrom(key)
		if err != nil {
			t.Errorf("%s: wantPublicKeys[%d] could not be read as a signer: %v", assertion, i, err)
			return false
		}
		want = append(want, signer)
	}
	return assertHintsPresent(t, assertion, envelope, want, len(want), "signer")
}

// expectedSigner is one wanted signer reduced to the strkey a message prints
// and the hint a signature must carry.
type expectedSigner struct {
	label string
	hint  xdr.SignatureHint
}

// attachedSignature is one DecoratedSignature reduced to what matching and a
// failure message need, so the generated union types are not touched again.
type attachedSignature struct {
	// scope is "inner" or "fee-bump", naming which signature set the
	// signature came from.
	scope string
	// index is the position within that set.
	index int
	hint  xdr.SignatureHint
	size  int
}

// assertSignaturesPresent is the shared implementation behind the all-of and
// M-of-N assertions.
func assertSignaturesPresent(t TB, assertion string, envelope Value, required int, expected []Value) bool {
	t.Helper()

	if len(expected) == 0 {
		t.Errorf("%s: no expected public keys were given; pass at least one signer", assertion)
		return false
	}
	want, ok := resolveExpectedSigners(t, assertion, expected)
	if !ok {
		return false
	}
	return assertHintsPresent(t, assertion, envelope, want, required, "signer")
}

// assertHintsPresent checks that the envelope carries a signature for each of
// the wanted signers, and reports the ones that are missing.
//
// The wanted signers are reduced to hints before they get here, so an
// assertion that names a hint and one that names a public key are checked the
// same way.
//
// word names what the wanted signers are — "signer" or "hint" — so the
// message reads as the assertion that made it.
func assertHintsPresent(t TB, assertion string, envelope Value, want []expectedSigner, required int, word string) bool {
	t.Helper()

	sigs, ok := envelopeSignatures(t, assertion, envelope)
	if !ok {
		return false
	}

	matchedAt := make([]int, len(want))
	present := 0
	for i, w := range want {
		matchedAt[i] = -1
		for j, s := range sigs {
			if s.hint == w.hint {
				matchedAt[i] = j
				present++
				break
			}
		}
	}
	if present >= required {
		return true
	}

	var b strings.Builder
	if required == len(want) {
		fmt.Fprintf(&b, "%s: %d of %d expected %s(s) have no matching signature\n\n",
			assertion, len(want)-present, len(want), word)
	} else {
		fmt.Fprintf(&b, "%s: only %d of %d expected %s(s) signed, want at least %d\n\n",
			assertion, present, len(want), word, required)
	}
	fmt.Fprintf(&b, "%s\n\n%s", describeExpectedSigners(want, matchedAt, sigs, word), describeAttachedSignatures(sigs))
	t.Errorf("%s", b.String())
	return false
}

// resolveExpectedSigners coerces every expected key, reporting a test failure
// naming the argument that could not be read.
func resolveExpectedSigners(t TB, assertion string, keys []Value) ([]expectedSigner, bool) {
	t.Helper()

	out := make([]expectedSigner, 0, len(keys))
	for i, key := range keys {
		var (
			signer expectedSigner
			err    error
		)
		if !guard(t, assertion, func() { signer, err = expectedSignerFrom(key) }) {
			return nil, false
		}
		if err != nil {
			t.Errorf("%s: expectedPubKeys[%d] could not be read as a signer: %v", assertion, i, err)
			return nil, false
		}
		out = append(out, signer)
	}
	return out, true
}

// expectedSignerFrom accepts one expected signer in the forms callers hold it
// in, reducing it to a strkey and the hint its signature will carry.
func expectedSignerFrom(v Value) (expectedSigner, error) {
	addr, err := signerAddress(v)
	if err != nil {
		return expectedSigner{}, err
	}

	var key xdr.SignerKey
	if err := key.SetAddress(addr); err != nil {
		return expectedSigner{}, fmt.Errorf("%q is not a signer address: %w", addr, err)
	}
	hint, err := signerKeyHint(key)
	if err != nil {
		return expectedSigner{}, fmt.Errorf("%q: %w", addr, err)
	}
	return expectedSigner{label: addr, hint: hint}, nil
}

// signerAddress reduces a signer argument to the strkey it is identified by.
func signerAddress(v Value) (string, error) {
	switch val := v.(type) {
	case string:
		trimmed := strings.TrimSpace(val)
		if trimmed == "" {
			return "", fmt.Errorf("address is empty")
		}
		return trimmed, nil
	case xdr.SignerKey:
		key := val
		return key.GetAddress()
	case *xdr.SignerKey:
		if val == nil {
			return "", fmt.Errorf("value is a nil *xdr.SignerKey")
		}
		return val.GetAddress()
	case xdr.AccountId:
		aid := val
		return aid.GetAddress()
	case *xdr.AccountId:
		if val == nil {
			return "", fmt.Errorf("value is a nil *xdr.AccountId")
		}
		return val.GetAddress()
	case xdr.MuxedAccount:
		aid := val.ToAccountId()
		return aid.GetAddress()
	case *xdr.MuxedAccount:
		if val == nil {
			return "", fmt.Errorf("value is a nil *xdr.MuxedAccount")
		}
		aid := val.ToAccountId()
		return aid.GetAddress()
	case *lens.Value:
		if val == nil {
			return "", fmt.Errorf("value is a nil *lens.Value")
		}
		return signerAddress(val.Raw)
	default:
		return "", fmt.Errorf(
			"unsupported type %T; expected a strkey address, an xdr.AccountId, "+
				"an xdr.MuxedAccount, an xdr.SignerKey or a decoded *lens.Value", v)
	}
}

// signerKeyHint extracts the four-byte signature hint the protocol derives
// from a signer key: the end of the ed25519 public key, of a pre-auth
// transaction hash, of a hash-x preimage hash, or of the public key inside a
// signed payload.
func signerKeyHint(key xdr.SignerKey) (xdr.SignatureHint, error) {
	var hint xdr.SignatureHint

	switch key.Type {
	case xdr.SignerKeyTypeSignerKeyTypeEd25519:
		ed25519, ok := key.GetEd25519()
		if !ok {
			return hint, fmt.Errorf("ed25519 signer key has no key")
		}
		copy(hint[:], ed25519[28:32])

	case xdr.SignerKeyTypeSignerKeyTypePreAuthTx:
		hash, ok := key.GetPreAuthTx()
		if !ok {
			return hint, fmt.Errorf("pre-auth-tx signer key has no hash")
		}
		copy(hint[:], hash[28:32])

	case xdr.SignerKeyTypeSignerKeyTypeHashX:
		hash, ok := key.GetHashX()
		if !ok {
			return hint, fmt.Errorf("hash-x signer key has no hash")
		}
		copy(hint[:], hash[28:32])

	case xdr.SignerKeyTypeSignerKeyTypeEd25519SignedPayload:
		payload, ok := key.GetEd25519SignedPayload()
		if !ok {
			return hint, fmt.Errorf("signed-payload signer key has no payload")
		}
		copy(hint[:], payload.Ed25519[28:32])

	default:
		return hint, fmt.Errorf("unknown signer key type %d", key.Type)
	}
	return hint, nil
}

// envelopeSignatures collects the signatures an envelope carries, tagging each
// with the set it came from.
//
// A non-fee-bump envelope has only the inner set. A fee-bump envelope has two:
// the fee-bump source's signature over the outer transaction, and the inner
// transaction's signatures.
func envelopeSignatures(t TB, assertion string, envelope Value) ([]attachedSignature, bool) {
	t.Helper()

	env, _, ok := envelopeFrom(t, assertion, envelope)
	if !ok {
		return nil, false
	}

	var out []attachedSignature
	if !guard(t, assertion, func() {
		for i, sig := range env.Signatures() {
			out = append(out, attachedSignature{
				scope: "inner", index: i, hint: sig.Hint, size: len(sig.Signature),
			})
		}
		if env.IsFeeBump() {
			for i, sig := range env.FeeBumpSignatures() {
				out = append(out, attachedSignature{
					scope: "fee-bump", index: i, hint: sig.Hint, size: len(sig.Signature),
				})
			}
		}
	}) {
		return nil, false
	}
	return out, true
}

// describeExpectedSigners renders every expected signer and whether a
// signature matched it, which tells the reader who did sign as well as who did
// not.
func describeExpectedSigners(want []expectedSigner, matchedAt []int, sigs []attachedSignature, word string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "expected %ss (%d):\n", word, len(want))
	for i, w := range want {
		status := "no signature"
		if j := matchedAt[i]; j >= 0 {
			status = fmt.Sprintf("signed (%s [%d])", sigs[j].scope, sigs[j].index)
		}
		fmt.Fprintf(&b, "  [%d] %s (hint %s) — %s\n",
			i, shorten(w.label), hex.EncodeToString(w.hint[:]), status)
	}
	return strings.TrimRight(b.String(), "\n")
}

// describeAttachedSignatures renders the signatures actually carried, so a
// hint that matches no expected signer is visible rather than silently
// ignored.
func describeAttachedSignatures(sigs []attachedSignature) string {
	if len(sigs) == 0 {
		return "signatures carried: none"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "signatures carried (%d):\n", len(sigs))
	for _, s := range sigs {
		fmt.Fprintf(&b, "  [%d] %-8s hint %s, %d byte(s)\n",
			s.index, s.scope, hex.EncodeToString(s.hint[:]), s.size)
	}
	return strings.TrimRight(b.String(), "\n")
}
