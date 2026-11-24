package focus

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// A contract's __constructor runs exactly once, inside the deployment
// transaction that creates the contract. Everything it writes to storage is
// therefore visible in that transaction's meta as contract data: the instance
// entry the protocol creates (whose ScContractInstance carries instance
// storage) and one ContractData entry per persistent or temporary storage
// write. These assertions read that state.
//
// That makes them offline by construction. The meta a test already holds is the
// evidence, so no RPC call, simulator or ledger snapshot is needed, and the
// same meta gives the same answer every run.
//
// This is the property a deployment test usually wants. Asserting that a
// create_contract operation succeeded says the contract exists; asserting on
// the constructor state says it exists *and* was initialized the way the
// contract expects — owner bound, admin set, supply minted, initialized flag
// flipped.

// ConstructorState describes the state a contract's __constructor is expected
// to have established during deployment.
//
// ContractID is required. Everything else is optional: an empty Owner skips the
// owner check, and an empty Entries map checks only that the deployment
// initialized the contract.
//
// Storage keys are matched against the key's symbol or string value, ignoring
// case, because a #[contracttype] enum variant commonly serializes as "Owner"
// while the field it backs reads "owner".
type ConstructorState struct {
	// ContractID is the strkey "C…" address of the deployed contract.
	ContractID string

	// Owner is the strkey account ("G…") or contract ("C…") address the
	// constructor is expected to bind as owner. Empty skips the check.
	Owner string
	// OwnerKey is the storage key holding the owner. Empty means "owner".
	OwnerKey string

	// Entries are additional storage entries the constructor is expected to
	// have written, keyed by storage key name.
	//
	// Values accept an xdr.ScVal, a *lens.Value, base64 or raw XDR, or a Go
	// bool, integer or string. A Go string becomes an address when it is a
	// strkey account or contract address, and a symbol otherwise.
	Entries map[string]any
}

// ownerKey returns the key the owner is expected under.
func (w ConstructorState) ownerKey() string {
	if w.OwnerKey != "" {
		return w.OwnerKey
	}
	return "owner"
}

// AssertConstructorState checks that deploying a contract through its
// __constructor left the expected state behind.
//
// The assertion reads the contract data a transaction meta carries. It fails
// when the contract has no instance entry — the marker that this transaction
// deployed it — when an expected storage entry is missing, or when an entry
// holds a different value. Entries the constructor wrote that the test did not
// mention are not differences: a constructor is allowed to write more than one
// test cares about.
//
// State is read from both places a constructor can put it: the instance
// storage inside the contract's instance entry, and the separate persistent and
// temporary ContractData entries.
//
// Every difference is reported in one failure, with the value wanted, the value
// found, and where it was found. The full state the constructor wrote is listed
// underneath, so a mismatch is diagnosable without a second run.
func AssertConstructorState(t TB, meta Value, want ConstructorState) bool {
	t.Helper()
	const assertion = "AssertConstructorState"

	expected, err := constructorExpectations(want)
	if err != nil {
		t.Errorf("%s: %v", assertion, err)
		return false
	}

	snapshot, ok := readConstructorSnapshot(t, assertion, meta, want.ContractID)
	if !ok {
		return false
	}

	var diffs []constructorDifference
	for _, exp := range expected {
		candidates := snapshot.find(exp.name)
		if len(candidates) == 0 {
			diffs = append(diffs, constructorDifference{
				key:  exp.name,
				want: describeValue(exp.value),
				got:  "(missing)",
			})
			continue
		}
		if anyValueEquals(candidates, exp.valueID) {
			continue
		}
		got := candidates[0]
		diffs = append(diffs, constructorDifference{
			key:  exp.name,
			want: describeValue(exp.value),
			got:  fmt.Sprintf("%s  (%s)", describeValue(got.value), got.location()),
		})
	}

	if len(diffs) == 0 {
		return true
	}

	t.Errorf("%s: contract %s was initialized with different state — %s\n\n%s\n\n%s",
		assertion, shorten(snapshot.contract), pluralDiff(len(diffs)),
		indent(renderConstructorDifferences(diffs)),
		indent(snapshot.describe()))
	return false
}

// AssertContractOwner checks that a contract's __constructor bound the
// conventional "owner" storage entry to wantOwner.
//
// This is the owner-binding half of AssertConstructorState on its own, for the
// common case where the only property a deployment test wants to pin down is
// who owns the contract. For a different storage key, or to check the owner
// alongside other state, use AssertConstructorState with Owner and OwnerKey.
//
// The owner must be an address: a strkey account ("G…") or contract ("C…"). A
// value that is not an address is reported as such rather than as a value
// mismatch, because it usually means the constructor wrote the wrong kind of
// value rather than the wrong owner.
func AssertContractOwner(t TB, meta Value, contractID, wantOwner string) bool {
	t.Helper()
	const assertion = "AssertContractOwner"

	if strings.TrimSpace(wantOwner) == "" {
		t.Errorf("%s: wantOwner is empty; pass the strkey account or contract address "+
			"the constructor binds as owner", assertion)
		return false
	}
	want, ok := scAddressValue(strings.TrimSpace(wantOwner))
	if !ok {
		t.Errorf("%s: wantOwner %q is not a strkey account (G…) or contract (C…) address",
			assertion, wantOwner)
		return false
	}
	wantID, err := scValID(want)
	if err != nil {
		t.Errorf("%s: encoding the wanted owner: %v", assertion, err)
		return false
	}

	snapshot, ok := readConstructorSnapshot(t, assertion, meta, contractID)
	if !ok {
		return false
	}

	const ownerKey = "owner"
	candidates := snapshot.find(ownerKey)
	if len(candidates) == 0 {
		t.Errorf("%s: contract %s has no owner binding: the deployment wrote no storage entry "+
			"with key %q\n\n%s",
			assertion, shorten(snapshot.contract), ownerKey, indent(snapshot.describe()))
		return false
	}
	if anyValueEquals(candidates, wantID) {
		return true
	}

	got := candidates[0]
	if !isAddressScVal(got.value) {
		t.Errorf("%s: the owner entry on contract %s holds %s, which is not an address; "+
			"an owner binding must be a strkey account (G…) or contract (C…)\n\n%s",
			assertion, shorten(snapshot.contract), describeValue(got.value),
			indent(snapshot.describe()))
		return false
	}

	t.Errorf("%s: contract %s binds owner to %s, want %s\n\n%s",
		assertion, shorten(snapshot.contract), describeValue(got.value), describeValue(want),
		indent(snapshot.describe()))
	return false
}

// constructorExpectation is one storage entry a test expects to find.
type constructorExpectation struct {
	// name is the storage key name as the test wrote it, for the message.
	name string
	// value is the expected value.
	value xdr.ScVal
	// valueID is the canonical encoding of value, compared instead of calling
	// the generated Equals methods so a malformed arm cannot panic.
	valueID string
}

// constructorExpectations validates a ConstructorState and turns it into the
// ordered list of entries to check. Validation happens before the meta is read,
// so a mistake in the expectation is never reported as a missing entry.
func constructorExpectations(want ConstructorState) ([]constructorExpectation, error) {
	var out []constructorExpectation

	if strings.TrimSpace(want.Owner) != "" {
		owner := strings.TrimSpace(want.Owner)
		sv, ok := scAddressValue(owner)
		if !ok {
			return nil, fmt.Errorf("the Owner value %q is not a strkey account (G…) or contract (C…) address", want.Owner)
		}
		id, err := scValID(sv)
		if err != nil {
			return nil, fmt.Errorf("encoding the Owner value: %w", err)
		}
		out = append(out, constructorExpectation{name: want.ownerKey(), value: sv, valueID: id})
	}

	keys := make([]string, 0, len(want.Entries))
	for name := range want.Entries {
		keys = append(keys, name)
	}
	sort.Strings(keys)

	for _, name := range keys {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("the Entries map contains an empty storage key")
		}
		if want.Owner != "" && strings.EqualFold(name, want.ownerKey()) {
			return nil, fmt.Errorf("the Entries map also lists the owner key %q; "+
				"set Owner and leave the key out of Entries, or drop Owner and assert on the entry directly", name)
		}
		sv, err := constructorValue(want.Entries[name])
		if err != nil {
			return nil, fmt.Errorf("the Entries value for %q: %w", name, err)
		}
		id, err := scValID(sv)
		if err != nil {
			return nil, fmt.Errorf("the Entries value for %q could not be encoded: %w", name, err)
		}
		out = append(out, constructorExpectation{name: name, value: sv, valueID: id})
	}

	return out, nil
}

// constructorValue converts an expected storage value into an ScVal.
//
// An xdr.ScVal, a *lens.Value or base64/raw XDR is used as given. A Go scalar
// is mapped to the arm a test most likely means: bool to sb, integer to i64 or
// the matching width, and a string to an address when it is a valid strkey and
// a symbol otherwise.
//
// The string rule is what makes the common case readable: passing "GA…" to a
// contract state assertion means an address, and passing "admin" means the
// symbol a contract's DataKey normally serializes to.
func constructorValue(v any) (xdr.ScVal, error) {
	switch val := v.(type) {
	case xdr.ScVal:
		return val, nil
	case *xdr.ScVal:
		if val == nil {
			return xdr.ScVal{}, fmt.Errorf("value is a nil *xdr.ScVal")
		}
		return *val, nil

	case bool:
		b := val
		return xdr.ScVal{Type: xdr.ScValTypeScvBool, B: &b}, nil
	case int:
		i := xdr.Int64(val)
		return xdr.ScVal{Type: xdr.ScValTypeScvI64, I64: &i}, nil
	case int32:
		i := xdr.Int32(val)
		return xdr.ScVal{Type: xdr.ScValTypeScvI32, I32: &i}, nil
	case int64:
		i := xdr.Int64(val)
		return xdr.ScVal{Type: xdr.ScValTypeScvI64, I64: &i}, nil
	case uint32:
		u := xdr.Uint32(val)
		return xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &u}, nil
	case uint64:
		u := xdr.Uint64(val)
		return xdr.ScVal{Type: xdr.ScValTypeScvU64, U64: &u}, nil

	case string:
		if sv, ok := scAddressValue(val); ok {
			return sv, nil
		}
		// Base64 XDR is the package's usual way to pass an exact value, so it
		// is tried before falling back to a symbol.
		if decoded, err := coerce(val, "ScVal"); err == nil {
			if sv, ok := decoded.Raw.(*xdr.ScVal); ok {
				return *sv, nil
			}
		}
		sym := xdr.ScSymbol(val)
		return xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}, nil

	default:
		decoded, err := coerce(v, "ScVal")
		if err != nil {
			return xdr.ScVal{}, fmt.Errorf(
				"%w; pass an xdr.ScVal, base64 or raw XDR, or a bool, integer or string", err)
		}
		sv, ok := decoded.Raw.(*xdr.ScVal)
		if !ok {
			return xdr.ScVal{}, fmt.Errorf("decoded %s value does not hold an ScVal", decoded.Type)
		}
		return *sv, nil
	}
}

// constructorEntry is one storage entry a constructor wrote, in the sanitized
// form the assertions read. All generated accessors have already been called by
// the time one of these exists.
type constructorEntry struct {
	key        xdr.ScVal
	value      xdr.ScVal
	durability xdr.ContractDataDurability
	// instance reports that the entry lives in the contract instance's storage
	// map rather than in a ContractData entry of its own.
	instance bool
	// keyID and valueID are canonical XDR encodings, used for matching so the
	// generated Equals methods are never called on an unexpected arm.
	keyID   string
	valueID string
}

// location names where the entry lives, for a failure message.
func (e constructorEntry) location() string {
	if e.instance {
		return "instance storage"
	}
	return durabilityLabel(e.durability) + " storage"
}

// constructorSnapshot is the state a deployment transaction left for one
// contract.
type constructorSnapshot struct {
	contract string
	// instance reports whether the contract's instance entry is present, which
	// is the marker that this transaction deployed it.
	instance bool
	entries  []constructorEntry
}

// find returns the entries whose key name matches name, ignoring case.
func (s constructorSnapshot) find(name string) []constructorEntry {
	var out []constructorEntry
	for _, e := range s.entries {
		if got, ok := storageKeyName(e.key); ok && strings.EqualFold(got, name) {
			out = append(out, e)
		}
	}
	return out
}

// describe renders the state the constructor wrote, so a failure shows what is
// there as well as what was wanted.
func (s constructorSnapshot) describe() string {
	if len(s.entries) == 0 {
		if s.instance {
			return "the constructor wrote no storage entries"
		}
		return "the deployment wrote no contract data for " + shorten(s.contract)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "state written by the constructor (%d entries):\n", len(s.entries))
	for _, e := range s.entries {
		fmt.Fprintf(&b, "  %-16s %-22s %s\n",
			e.location(), describeStorageKey(e.key), describeValue(e.value))
	}
	return strings.TrimRight(b.String(), "\n")
}

// constructorDifference is one mismatched storage entry.
type constructorDifference struct {
	key  string
	want string
	got  string
}

// renderConstructorDifferences prints each difference with the value wanted and
// the value found, so a failure answers both questions without a second run.
func renderConstructorDifferences(diffs []constructorDifference) string {
	var b strings.Builder
	fmt.Fprintf(&b, "differences (%d):\n", len(diffs))
	for _, d := range diffs {
		fmt.Fprintf(&b, "  ~ %s\n", d.key)
		fmt.Fprintf(&b, "      want  %s\n", d.want)
		fmt.Fprintf(&b, "      got   %s\n", d.got)
	}
	return strings.TrimRight(b.String(), "\n")
}

// pluralDiff renders a difference count for the headline.
func pluralDiff(n int) string {
	if n == 1 {
		return "1 difference"
	}
	return fmt.Sprintf("%d differences", n)
}

// readConstructorSnapshot decodes a transaction meta and collects the state the
// deployment wrote for contractID.
//
// It reports why there is nothing to check — a wrong contract id, a meta from
// something other than a deployment, a missing instance entry — rather than
// silently passing an assertion that was never able to make its check.
func readConstructorSnapshot(t TB, assertion string, meta Value, contractID string) (constructorSnapshot, bool) {
	t.Helper()

	contractID = strings.TrimSpace(contractID)
	if contractID == "" {
		t.Errorf("%s: contractID is empty; pass the strkey C… address of the deployed contract",
			assertion)
		return constructorSnapshot{}, false
	}
	if !strings.HasPrefix(contractID, "C") {
		t.Errorf("%s: contractID %q is not a contract address; it must be the strkey C… "+
			"address of the deployed contract", assertion, contractID)
		return constructorSnapshot{}, false
	}
	if _, err := strkey.Decode(strkey.VersionByteContract, contractID); err != nil {
		t.Errorf("%s: contractID %q is not a valid strkey contract address: %v",
			assertion, contractID, err)
		return constructorSnapshot{}, false
	}

	changes, ok := ledgerChanges(t, assertion, meta)
	if !ok {
		return constructorSnapshot{}, false
	}

	var (
		snapshot constructorSnapshot
		present  []string
	)
	if !guard(t, assertion, func() {
		snapshot, present = scanContractData(changes, contractID)
	}) {
		return constructorSnapshot{}, false
	}

	if !snapshot.instance {
		if len(snapshot.entries) == 0 {
			detail := "the transaction meta carries no contract data at all"
			if len(present) > 0 {
				detail = fmt.Sprintf("contract data present (%d):\n%s",
					len(present), strings.Join(present, "\n"))
			}
			t.Errorf("%s: no contract data for contract %s was written by this transaction\n\n%s\n\n"+
				"A __constructor runs inside the deployment transaction, so the state it writes "+
				"appears in that transaction's meta. Check the contract id, and that this is the "+
				"deployment transaction rather than a later invocation.",
				assertion, shorten(contractID), indent(detail))
			return constructorSnapshot{}, false
		}
		t.Errorf("%s: contract %s has no contract instance entry in this transaction, so the "+
			"deployment did not create it\n\n%s",
			assertion, shorten(contractID), indent(snapshot.describe()))
		return constructorSnapshot{}, false
	}

	return snapshot, true
}

// scanContractData walks the ledger changes and collects the contract data
// belonging to contractID. It calls generated accessors, so it must run behind
// guard.
//
// The returned present lines list every contract data change the meta carries,
// for the diagnosis a wrong contract id needs.
func scanContractData(changes []ledgerChange, contractID string) (constructorSnapshot, []string) {
	snapshot := constructorSnapshot{contract: contractID}
	positions := make(map[string]int)
	var present []string

	for _, change := range changes {
		if change.entryType != xdr.LedgerEntryTypeContractData || change.entry == nil {
			continue
		}
		// A "state" change is the value before an update, not something the
		// transaction wrote. Listing it would duplicate every modified entry.
		switch change.kind {
		case xdr.LedgerEntryChangeTypeLedgerEntryCreated,
			xdr.LedgerEntryChangeTypeLedgerEntryUpdated,
			xdr.LedgerEntryChangeTypeLedgerEntryRestored:
		default:
			continue
		}

		data, ok := change.entry.Data.GetContractData()
		if !ok {
			continue
		}
		address, err := data.Contract.String()
		if err != nil {
			continue
		}

		keyID, err := scValID(data.Key)
		if err != nil {
			continue
		}
		valueID, err := scValID(data.Val)
		if err != nil {
			continue
		}

		present = append(present, fmt.Sprintf("%s  %-10s  %s → %s",
			shorten(address), durabilityLabel(data.Durability),
			describeStorageKey(data.Key), describeScVal(data.Val)))

		if address != contractID {
			continue
		}

		if data.Key.Type == xdr.ScValTypeScvLedgerKeyContractInstance {
			snapshot.instance = true
			instance, hasInstance := data.Val.GetInstance()
			if !hasInstance || instance.Storage == nil {
				continue
			}
			for _, entry := range *instance.Storage {
				entryKeyID, err := scValID(entry.Key)
				if err != nil {
					continue
				}
				entryValueID, err := scValID(entry.Val)
				if err != nil {
					continue
				}
				addConstructorEntry(&snapshot, positions, constructorEntry{
					key:        entry.Key,
					value:      entry.Val,
					durability: data.Durability,
					instance:   true,
					keyID:      entryKeyID,
					valueID:    entryValueID,
				})
			}
			continue
		}

		addConstructorEntry(&snapshot, positions, constructorEntry{
			key:        data.Key,
			value:      data.Val,
			durability: data.Durability,
			keyID:      keyID,
			valueID:    valueID,
		})
	}

	return snapshot, present
}

// addConstructorEntry appends an entry, or replaces an earlier value for the
// same key and durability so the state after the transaction is what remains.
func addConstructorEntry(snapshot *constructorSnapshot, positions map[string]int, entry constructorEntry) {
	id := entry.keyID + "|" + fmt.Sprintf("%d|%t", entry.durability, entry.instance)
	if pos, ok := positions[id]; ok {
		snapshot.entries[pos] = entry
		return
	}
	positions[id] = len(snapshot.entries)
	snapshot.entries = append(snapshot.entries, entry)
}

// anyValueEquals reports whether any candidate holds the wanted value.
func anyValueEquals(candidates []constructorEntry, wantID string) bool {
	for _, candidate := range candidates {
		if candidate.valueID == wantID {
			return true
		}
	}
	return false
}

// storageKeyName returns the text of a storage key: its symbol or string value.
// Storage keys that are neither are not addressable by the key-name form these
// assertions use, so they are ignored by find.
func storageKeyName(v xdr.ScVal) (string, bool) {
	if sym, ok := v.GetSym(); ok {
		return string(sym), true
	}
	if s, ok := v.GetStr(); ok {
		return string(s), true
	}
	return "", false
}

// describeStorageKey renders a storage key the way a contract's DataKey reads:
// as a bare quoted name for symbol and string keys, and as the general ScVal
// form otherwise.
func describeStorageKey(v xdr.ScVal) string {
	if name, ok := storageKeyName(v); ok {
		return fmt.Sprintf("%q", name)
	}
	return describeScVal(v)
}

// scValID returns a canonical encoding of an ScVal, so two values are equal
// exactly when the encodings are. MarshalBinary reports a malformed union
// instead of panicking the way the generated Equals methods can.
func scValID(v xdr.ScVal) (string, error) {
	raw, err := v.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("encoding an ScVal: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// scAddressValue builds the ScvAddress for a strkey account or contract
// address, and reports whether addr was one.
func scAddressValue(addr string) (xdr.ScVal, bool) {
	switch {
	case strings.HasPrefix(addr, "C"):
		raw, err := strkey.Decode(strkey.VersionByteContract, addr)
		if err != nil {
			return xdr.ScVal{}, false
		}
		var id xdr.ContractId
		copy(id[:], raw)
		return addressScVal(xdr.ScAddressTypeScAddressTypeContract, id)

	case strings.HasPrefix(addr, "G"):
		account, err := xdr.AddressToAccountId(addr)
		if err != nil {
			return xdr.ScVal{}, false
		}
		return addressScVal(xdr.ScAddressTypeScAddressTypeAccount, account)

	default:
		return xdr.ScVal{}, false
	}
}

// addressScVal wraps an address arm in the ScVal union.
func addressScVal(addressType xdr.ScAddressType, value any) (xdr.ScVal, bool) {
	address, err := xdr.NewScAddress(addressType, value)
	if err != nil {
		return xdr.ScVal{}, false
	}
	return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &address}, true
}

// isAddressScVal reports whether a value is an address.
func isAddressScVal(v xdr.ScVal) bool {
	_, ok := v.GetAddress()
	return ok
}

// describeValue renders a storage value for a failure message. An address is
// named as an address, because "want GAPB7…WYXZ" otherwise reads like any other
// string, and an address is the most common constructor state a test asserts
// on.
func describeValue(v xdr.ScVal) string {
	if address, ok := v.GetAddress(); ok {
		if s, err := address.String(); err == nil {
			return "address " + shorten(s)
		}
	}
	return describeScVal(v)
}

// durabilityLabel names a contract data durability for a failure listing.
func durabilityLabel(d xdr.ContractDataDurability) string {
	switch d {
	case xdr.ContractDataDurabilityPersistent:
		return "persistent"
	case xdr.ContractDataDurabilityTemporary:
		return "temporary"
	default:
		return fmt.Sprintf("durability_%d", d)
	}
}
