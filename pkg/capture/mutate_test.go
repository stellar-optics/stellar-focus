package capture

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

const samplePayload = "AAAAAAAAAGQAAAAAAAAA+owAAAAAAAAAAGQAAAAAAAAAAGqZAAAAAAAD6jAAAABwAAAAAAAAAAAAAAAAAAAAEBAAAAAAAAALNuO5b7acqW1sVPZ/JTluF8v93g=="

func TestMutateDeterministic(t *testing.T) {
	one, mutOne, err := Mutate(samplePayload, 7, 3)
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	two, mutTwo, err := Mutate(samplePayload, 7, 3)
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}

	if one != two {
		t.Error("same seed and steps must produce identical mutants")
	}
	if len(mutOne) != len(mutTwo) {
		t.Fatalf("mutation reports differ in size: %d vs %d", len(mutOne), len(mutTwo))
	}
	for i := range mutOne {
		if mutOne[i] != mutTwo[i] {
			t.Errorf("mutation %d differs: %+v vs %+v", i, mutOne[i], mutTwo[i])
		}
	}
}

func TestMutateChangesPayload(t *testing.T) {
	mutant, muts, err := Mutate(samplePayload, 42, 2)
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}

	if mutant == samplePayload {
		t.Error("two steps of corruption must change the payload")
	}
	if len(muts) != 2 {
		t.Fatalf("the report must name exactly what was applied: %d entries, %+v", len(muts), muts)
	}

	for _, m := range muts {
		if m.Op == "unknown" {
			t.Errorf("corruption op names must be resolved: %+v", m)
		}
		if m.Offset < 0 {
			t.Errorf("offsets are valid byte indexes: %+v", m)
		}
	}
}

func TestMutateZeroStepsIsNoOp(t *testing.T) {
	mutant, muts, err := Mutate(samplePayload, 1, 0)
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}

	if mutant != samplePayload {
		t.Error("zero steps must return the input unchanged")
	}
	if len(muts) != 0 {
		t.Errorf("zero steps produce no mutation entries, got %+v", muts)
	}
}

func TestMutateDifferentSeedsDiverge(t *testing.T) {
	a, _, err := Mutate(samplePayload, 1, 4)
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	b, _, err := Mutate(samplePayload, 99, 4)
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}

	if a == b {
		t.Error("different seeds should give different mutants")
	}
}

func TestMutateErrors(t *testing.T) {
	t.Run("invalid base64", func(t *testing.T) {
		_, _, err := Mutate("!!!not-base64!!!", 1, 1)
		if err == nil || !contains(err.Error(), "not valid base64") {
			t.Fatalf("want a base64 error, got: %v", err)
		}
	})

	t.Run("empty payload", func(t *testing.T) {
		empty := base64.StdEncoding.EncodeToString(nil)
		_, _, err := Mutate(empty, 1, 1)
		if err == nil || !contains(err.Error(), "nothing to corrupt") {
			t.Fatalf("want an empty-payload error, got: %v", err)
		}
	})

	t.Run("negative steps", func(t *testing.T) {
		_, _, err := Mutate(samplePayload, 1, -1)
		if err == nil || !contains(err.Error(), "must not be negative") {
			t.Fatalf("want a negative-steps error, got: %v", err)
		}
	})
}

func TestMutationsReportOnly(t *testing.T) {
	muts, err := Mutations(samplePayload, 3, 2)
	if err != nil {
		t.Fatalf("Mutations: %v", err)
	}
	if len(muts) != 2 {
		t.Fatalf("want two mutation entries, got %+v", muts)
	}
}

// mutationRaw decodes the shared sample payload into the raw bytes MutateXDR
// works on.
func mutationRaw(t *testing.T) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(samplePayload)
	if err != nil {
		t.Fatalf("decoding sample payload: %v", err)
	}
	return raw
}

func TestMutateXDRIsDeterministicForEveryType(t *testing.T) {
	raw := mutationRaw(t)
	for _, mutationType := range []MutationType{BitFlip, Truncate, RandomizeField, DuplicateOp} {
		t.Run(mutationType.String(), func(t *testing.T) {
			first, firstReport := MutateXDR(raw, 17, mutationType)
			second, secondReport := MutateXDR(raw, 17, mutationType)

			if string(first) != string(second) {
				t.Error("same seed and mutation type must produce byte-identical XDR")
			}
			if firstReport != secondReport {
				t.Errorf("reports differ: %+v vs %+v", firstReport, secondReport)
			}
			if !firstReport.Applied {
				t.Errorf("mutation was not applied: %+v", firstReport)
			}
			if firstReport.Offset < 0 || firstReport.Offset >= len(raw) {
				t.Errorf("report offset %d is outside the payload of %d bytes", firstReport.Offset, len(raw))
			}
		})
	}
}

func TestMutateXDRLeavesTheInputUntouched(t *testing.T) {
	raw := mutationRaw(t)
	original := string(raw)

	if _, report := MutateXDR(raw, 5, BitFlip); !report.Applied {
		t.Fatalf("bit flip was not applied: %+v", report)
	}
	if string(raw) != original {
		t.Error("MutateXDR modified its input slice")
	}
}

func TestMutateXDRReportsTheExactChange(t *testing.T) {
	raw := mutationRaw(t)

	t.Run("bit flip", func(t *testing.T) {
		got, report := MutateXDR(raw, 11, BitFlip)
		if got[report.Offset] == raw[report.Offset] {
			t.Errorf("byte %d did not change", report.Offset)
		}
		if !strings.Contains(report.Description, "flipped bit") {
			t.Errorf("description does not name the mutation: %q", report.Description)
		}
	})

	t.Run("truncate", func(t *testing.T) {
		got, report := MutateXDR(raw, 11, Truncate)
		if len(got) != report.Offset {
			t.Errorf("payload is %d bytes, report says it was truncated at %d", len(got), report.Offset)
		}
		if report.Length != len(raw)-len(got) {
			t.Errorf("report length %d does not match %d removed bytes", report.Length, len(raw)-len(got))
		}
	})

	t.Run("randomize field", func(t *testing.T) {
		got, report := MutateXDR(raw, 11, RandomizeField)
		changed := false
		for i := report.Offset; i < report.Offset+report.Length; i++ {
			if got[i] != raw[i] {
				changed = true
				break
			}
		}
		if !changed {
			t.Errorf("no byte in [%d,%d) changed", report.Offset, report.Offset+report.Length)
		}
	})
}

func TestMutateXDRDuplicateOperation(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "pkg", "fuzz", "testdata", "tx_failed.env.txt"))
	if err != nil {
		t.Fatalf("reading envelope fixture: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("decoding envelope fixture: %v", err)
	}

	mutated, report := MutateXDR(raw, 3, DuplicateOp)
	if !report.Applied {
		t.Fatalf("DuplicateOp was not applied: %+v", report)
	}
	if !strings.Contains(report.Description, "duplicated operation") {
		t.Fatalf("DuplicateOp fell back to bytes: %q", report.Description)
	}

	var before, after xdr.TransactionEnvelope
	if err := before.UnmarshalBinary(raw); err != nil {
		t.Fatalf("decoding original envelope: %v", err)
	}
	if err := after.UnmarshalBinary(mutated); err != nil {
		t.Fatalf("decoding mutated envelope: %v", err)
	}
	if got, want := after.OperationsCount(), before.OperationsCount()+1; got != want {
		t.Errorf("mutated envelope has %d operations, want %d", got, want)
	}
}

func TestMutateXDRReportsUnusableInput(t *testing.T) {
	unchanged, report := MutateXDR(nil, 1, BitFlip)
	if report.Applied {
		t.Errorf("empty input reported as applied: %+v", report)
	}
	if len(unchanged) != 0 {
		t.Errorf("empty input grew to %d bytes", len(unchanged))
	}

	raw, _ := base64.StdEncoding.DecodeString(samplePayload)
	unchanged, report = MutateXDR(raw, 1, MutationType("not-a-mutation"))
	if report.Applied {
		t.Errorf("unknown mutation type reported as applied: %+v", report)
	}
	if string(unchanged) != string(raw) {
		t.Error("unknown mutation type changed the payload")
	}
}

func TestMutateRecordExposesTheGenerator(t *testing.T) {
	record := Record{Name: "payment", Type: "TransactionEnvelope", XDR: samplePayload}

	mutant, report, err := MutateRecord(record, 9, BitFlip)
	if err != nil {
		t.Fatalf("MutateRecord: %v", err)
	}
	if mutant.XDR == record.XDR {
		t.Error("mutant payload is unchanged")
	}
	if report.Offset < 0 {
		t.Errorf("report has no offset: %+v", report)
	}
	if mutant.Name == record.Name {
		t.Error("mutant name should identify the mutation")
	}

	if _, _, err := MutateRecord(Record{Name: "broken", XDR: "!!"}, 1, BitFlip); err == nil {
		t.Fatal("invalid base64 was accepted")
	}
}
