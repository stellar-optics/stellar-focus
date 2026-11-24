package capture

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// MutationType names one corruption the byte-level mutator can apply.
//
// The mutator works on raw XDR bytes rather than on a decoded value, because
// the point is to produce the malformed input a parser must survive. The four
// shapes cover the cases that actually break a decoder: a single bit that
// changes a union discriminant, a record cut short, a field overwritten, and
// an operation duplicated where the protocol forbids it.
type MutationType string

const (
	// BitFlip flips one bit at a seeded offset.
	BitFlip MutationType = "bit_flip"
	// Truncate cuts the payload at a seeded offset, giving a short record.
	Truncate MutationType = "truncate"
	// RandomizeField overwrites a short run of bytes at a seeded offset.
	RandomizeField MutationType = "randomize_field"
	// DuplicateOp duplicates one operation of a transaction envelope. When
	// the payload is not a transaction envelope it falls back to duplicating a
	// short run of bytes, so the mutation still applies deterministically.
	DuplicateOp MutationType = "duplicate_op"
)

// String makes a mutation type readable in a fixture name and a report.
func (m MutationType) String() string { return string(m) }

// MutationReport describes exactly what MutateXDR did.
//
// Offset and Length locate the corruption in the original payload. MutatedLength
// differs from OriginalLength when the mutation changed the size — a truncation
// or a duplicated operation — so a caller can tell a same-size edit from a
// structural one without diffing the bytes itself.
type MutationReport struct {
	Type           MutationType `json:"type"`
	Offset         int          `json:"offset"`
	Length         int          `json:"length"`
	OriginalLength int          `json:"originalLength"`
	MutatedLength  int          `json:"mutatedLength"`
	Applied        bool         `json:"applied"`
	Description    string       `json:"description"`
}

// MutateXDR applies one deterministic mutation to a raw XDR payload.
//
// The same raw bytes, seed and mutation type always produce byte-identical
// output, which is what makes a negative fixture reproducible without storing
// the mutant itself. raw is never modified: the returned slice is always a
// fresh copy.
//
// The function has no error return on purpose: a mutator that refuses input is
// less useful than one that reports what it managed to do. An empty payload or
// an unrecognised mutation type leaves the bytes unchanged and is visible in
// the report's Applied and Description fields.
func MutateXDR(raw []byte, seed int64, mutationType MutationType) ([]byte, MutationReport) {
	out := append([]byte(nil), raw...)
	report := MutationReport{
		Type:           mutationType,
		Offset:         -1,
		OriginalLength: len(raw),
		MutatedLength:  len(raw),
	}

	if len(raw) == 0 {
		report.Description = "nothing to mutate: the payload is empty"
		return out, report
	}

	stream := mutationStream(seed, mutationType)
	switch mutationType {
	case BitFlip:
		offset := int(binary.BigEndian.Uint64(stream[0:8]) % uint64(len(out)))
		bit := uint(stream[8] % 8)
		out[offset] ^= 1 << bit
		report.Offset = offset
		report.Length = 1
		report.Applied = true
		report.Description = fmt.Sprintf("flipped bit %d at byte offset %d", bit, offset)

	case Truncate:
		n := int(binary.BigEndian.Uint64(stream[0:8]) % uint64(len(out)))
		out = out[:n]
		report.Offset = n
		report.Length = len(raw) - n
		report.Applied = true
		report.Description = fmt.Sprintf("truncated at byte offset %d (%d bytes removed)", n, len(raw)-n)

	case RandomizeField:
		offset := int(binary.BigEndian.Uint64(stream[0:8]) % uint64(len(out)))
		remaining := len(out) - offset
		maxLength := remaining
		if maxLength > 8 {
			maxLength = 8
		}
		length := 1 + int(uint64(stream[8])%uint64(maxLength))
		for i := 0; i < length; i++ {
			out[offset+i] = randomByte(stream, i)
		}
		report.Offset = offset
		report.Length = length
		report.Applied = true
		report.Description = fmt.Sprintf("randomized %d byte(s) at byte offset %d", length, offset)

	case DuplicateOp:
		if mutated, index, ok := duplicateOperation(out, stream); ok {
			offset := commonPrefix(out, mutated)
			out = mutated
			report.Offset = offset
			report.Applied = true
			report.Description = fmt.Sprintf("duplicated operation %d; payload diverges at byte offset %d", index, offset)
		} else {
			offset, length := duplicateByteRange(&out, stream)
			report.Offset = offset
			report.Length = length
			report.Applied = true
			report.Description = fmt.Sprintf("duplicated %d byte(s) at byte offset %d (payload is not a transaction envelope)", length, offset)
		}

	default:
		report.Description = fmt.Sprintf("unknown mutation type %q; payload left unchanged", mutationType)
		return out, report
	}

	report.MutatedLength = len(out)
	return out, report
}

// mutationStream derives a deterministic byte stream from a seed and a
// mutation type. Including the type means a BitFlip and a Truncate from the
// same seed do not land on the same offset by accident.
func mutationStream(seed int64, mutationType MutationType) [32]byte {
	h := sha256.New()
	seedBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(seedBytes, uint64(seed))
	_, _ = h.Write(seedBytes)
	_, _ = h.Write([]byte(mutationType))
	sum := h.Sum(nil)
	var stream [32]byte
	copy(stream[:], sum)
	return stream
}

// randomByte derives the i-th replacement byte without consuming shared
// state, so a mutation's bytes depend only on the seed, the type and the
// position within the field.
func randomByte(stream [32]byte, i int) byte {
	h := sha256.New()
	_, _ = h.Write(stream[:])
	_, _ = h.Write([]byte{byte(i)})
	return h.Sum(nil)[0]
}

// duplicateOperation duplicates one operation of a transaction envelope and
// re-encodes it. It reports the duplicated index so the report can name what
// changed.
func duplicateOperation(raw []byte, stream [32]byte) ([]byte, int, bool) {
	var envelope xdr.TransactionEnvelope
	if err := envelope.UnmarshalBinary(raw); err != nil {
		return nil, 0, false
	}

	var operations *[]xdr.Operation
	switch envelope.Type {
	case xdr.EnvelopeTypeEnvelopeTypeTx:
		if envelope.V1 == nil {
			return nil, 0, false
		}
		operations = &envelope.V1.Tx.Operations
	case xdr.EnvelopeTypeEnvelopeTypeTxV0:
		if envelope.V0 == nil {
			return nil, 0, false
		}
		operations = &envelope.V0.Tx.Operations
	case xdr.EnvelopeTypeEnvelopeTypeTxFeeBump:
		if envelope.FeeBump == nil || envelope.FeeBump.Tx.InnerTx.V1 == nil {
			return nil, 0, false
		}
		operations = &envelope.FeeBump.Tx.InnerTx.V1.Tx.Operations
	default:
		return nil, 0, false
	}

	ops := *operations
	if len(ops) == 0 {
		return nil, 0, false
	}
	index := int(binary.BigEndian.Uint64(stream[0:8]) % uint64(len(ops)))

	duplicated := make([]xdr.Operation, 0, len(ops)+1)
	duplicated = append(duplicated, ops[:index+1]...)
	duplicated = append(duplicated, ops[index])
	duplicated = append(duplicated, ops[index+1:]...)
	*operations = duplicated

	mutated, err := envelope.MarshalBinary()
	if err != nil {
		return nil, 0, false
	}
	return mutated, index, true
}

// duplicateByteRange duplicates a seeded run of bytes, the fallback shape of
// DuplicateOp for payloads that are not transaction envelopes.
func duplicateByteRange(data *[]byte, stream [32]byte) (offset, length int) {
	payload := *data
	offset = int(binary.BigEndian.Uint64(stream[0:8]) % uint64(len(payload)))
	remaining := len(payload) - offset
	maxLength := remaining
	if maxLength > 8 {
		maxLength = 8
	}
	length = 1 + int(uint64(stream[8])%uint64(maxLength))

	run := append([]byte(nil), payload[offset:offset+length]...)
	out := make([]byte, 0, len(payload)+length)
	out = append(out, payload[:offset+length]...)
	out = append(out, run...)
	out = append(out, payload[offset+length:]...)
	*data = out
	return offset + length, length
}

// commonPrefix returns the length of the longest shared prefix, which is the
// first byte a structural mutation changed.
func commonPrefix(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// Mutation is one corruption applied to captured bytes. The four shapes are
// the ones a fixture parser meets most: a bit flip somewhere, a byte zeroed,
// a byte set to 0xFF everywhere, and a truncated record. Every shape is
// deterministic for a given input and step number, so a mutant can be
// regenerated and its expected parse behaviour asserted in CI without
// committing the mutant itself.
type Mutation struct {
	// Op names the corruption. It is part of the deterministic choice, not
	// a knob the caller turns.
	Op string `json:"op"`
	// Offset is the byte index the corruption touched, counted from the
	// start of the decoded XDR.
	Offset int `json:"offset"`
}

// Mutate returns a corrupted variation of the base64 XDR in b64.
//
// seed starts the corruption stream and steps counts how many corruptions to
// apply; the same inputs always give the same mutant, and reporting what was
// touched — see Mutations — keeps a resilience test's name meaningful. The
// result is still base64; whether it still parses is the question a
// resilience test asks, which is why nothing here attempts to stay or skip
// field boundaries on the caller's behalf.
//
// A zero-length payload and an invalid base64 string are both errors rather
// than a no-op: a mutator that quietly passes its input through would make a
// resilience suite appear to exercise parsers it never reached.
func Mutate(b64 string, seed uint64, steps int) (mutant string, mutations []Mutation, err error) {
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", nil, fmt.Errorf("mutating: input is not valid base64 XDR: %w", err)
	}
	if len(data) == 0 {
		return "", nil, fmt.Errorf("mutating: nothing to corrupt (payload is empty)")
	}
	if steps < 0 {
		return "", nil, fmt.Errorf("mutating: steps must not be negative")
	}
	if steps == 0 {
		return base64.StdEncoding.EncodeToString(data), nil, nil
	}

	for step := 0; step < steps; step++ {
		off, op := pick(data, seed, step)
		mut := Mutation{Op: opName(op), Offset: off}
		switch op {
		case 0:
			data[off] ^= 0xFF
		case 1:
			data[off] = 0x00
		case 2:
			data[off] = 0xFF
		case 3:
			// Truncate after the chosen byte. Repeated truncation moves
			// the record shorter, so mutants from the same seed with more
			// steps read less and less like the original — the shape a
			// parser meets on cut-off input.
			if off+1 < len(data) {
				data = data[:off+1]
			}
		}
		mutations = append(mutations, mut)
	}

	return base64.StdEncoding.EncodeToString(data), mutations, nil
}

// Mutations reports what Mutate touched, so a resilience test can name its
// mutants exactly rather than diffing whole payloads.
func Mutations(b64 string, seed uint64, steps int) ([]Mutation, error) {
	_, mutations, err := Mutate(b64, seed, steps)
	if err != nil {
		return nil, err
	}
	return mutations, nil
}

// pick chooses one corruption for one step from the seed stream.
func pick(data []byte, seed uint64, step int) (int, int) {
	h := sha256.New()
	seedBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(seedBytes, seed)
	stepBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(stepBytes, uint32(step))
	_, _ = h.Write(seedBytes)
	_, _ = h.Write(stepBytes)
	sum := h.Sum(nil)

	op := int(sum[0] % 4)
	if len(data) < 2 {
		// A one-byte payload only supports in-place edits; truncating it
		// would give an empty record, which Mutate already refuses.
		return 0, op % 3
	}
	off := int(binary.BigEndian.Uint32(sum[1:5]) % uint32(len(data)))
	return off, op
}

// opName makes a mutation's op readable in a fixture name.
func opName(op int) string {
	switch op {
	case 0:
		return "bitflip"
	case 1:
		return "zero"
	case 2:
		return "ones"
	case 3:
		return "truncate"
	}
	return "unknown"
}
