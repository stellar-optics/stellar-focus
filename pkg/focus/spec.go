package focus

import (
	"encoding/binary"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// Contract specifications: the names the contract's author chose.
//
// A Soroban contract's interface — its structs, unions, enums, events and
// functions — is described by a list of ScSpecEntry values. Without that list
// the only description of an event payload is the one the wire gives: a map
// of symbol keys to values, discriminated by ScVal types. A test written
// that way names XDR discriminants, and the person reading the failure has
// to hold the contract's specification in their head to make sense of it.
//
// With a specification, a test says what the contract's author would say:
//
//	focus.AssertEventEmitted(t, meta, focus.Event{
//	    Topics: []any{"transfer"},
//	    Data:   map[string]any{"from": from, "amount": 100},
//	    Spec:   spec,
//	})
//
// and a failure names the field that differs:
//
//	data.amount: want u128(100), got u128(200)
//
// The specification is optional everywhere it appears. Without one, matching
// and rendering behave exactly as they did before, because a test that
// cannot supply a contract's interface should still work.

// specShape says how the parts of a value are arranged.
type specShape int

const (
	// shapeValue is a value with no named parts: a number, a string, an
	// address.
	shapeValue specShape = iota
	// shapeFields is a map-shaped value whose keys are field names.
	shapeFields
	// shapeSeq is a sequence-shaped value whose elements are in order.
	shapeSeq
)

// specType is a value's type, resolved against a contract's specification.
type specType struct {
	// shape says how the value is arranged.
	shape specShape
	// def is the type definition of the value, or of a sequence's elements.
	def *xdr.ScSpecTypeDef
	// fields names the parts of a map-shaped value.
	fields []specField
	// names names the elements of a sequence-shaped value, in order.
	names []string
}

// specField is one named part of a value.
type specField struct {
	// name is the field's name, as the contract's author wrote it.
	name string
	// def is the field's type.
	def *xdr.ScSpecTypeDef
}

// specIndex is a contract's specification, indexed by name.
//
// It is built once per assertion, so a spec that is a few hundred entries
// long costs one pass rather than one per event.
type specIndex struct {
	events    map[string]*xdr.ScSpecEventV0
	structs   map[string]*xdr.ScSpecUdtStructV0
	unions    map[string]*xdr.ScSpecUdtUnionV0
	enums     map[string]*xdr.ScSpecUdtEnumV0
	errEnums  map[string]*xdr.ScSpecUdtErrorEnumV0
	functions map[string]*xdr.ScSpecFunctionV0
}

// newSpecIndex indexes a contract's specification entries.
//
// An entry whose union arm is missing is skipped rather than fatal: a
// specification is an input, and a broken one should cost the names it
// carries, not the whole assertion.
func newSpecIndex(entries []xdr.ScSpecEntry) *specIndex {
	idx := &specIndex{
		events:    map[string]*xdr.ScSpecEventV0{},
		structs:   map[string]*xdr.ScSpecUdtStructV0{},
		unions:    map[string]*xdr.ScSpecUdtUnionV0{},
		enums:     map[string]*xdr.ScSpecUdtEnumV0{},
		errEnums:  map[string]*xdr.ScSpecUdtErrorEnumV0{},
		functions: map[string]*xdr.ScSpecFunctionV0{},
	}
	for i := range entries {
		entry := entries[i]
		switch entry.Kind {
		case xdr.ScSpecEntryKindScSpecEntryEventV0:
			if entry.EventV0 != nil {
				idx.events[string(entry.EventV0.Name)] = entry.EventV0
			}
		case xdr.ScSpecEntryKindScSpecEntryUdtStructV0:
			if entry.UdtStructV0 != nil {
				idx.structs[entry.UdtStructV0.Name] = entry.UdtStructV0
			}
		case xdr.ScSpecEntryKindScSpecEntryUdtUnionV0:
			if entry.UdtUnionV0 != nil {
				idx.unions[entry.UdtUnionV0.Name] = entry.UdtUnionV0
			}
		case xdr.ScSpecEntryKindScSpecEntryUdtEnumV0:
			if entry.UdtEnumV0 != nil {
				idx.enums[entry.UdtEnumV0.Name] = entry.UdtEnumV0
			}
		case xdr.ScSpecEntryKindScSpecEntryUdtErrorEnumV0:
			if entry.UdtErrorEnumV0 != nil {
				idx.errEnums[entry.UdtErrorEnumV0.Name] = entry.UdtErrorEnumV0
			}
		case xdr.ScSpecEntryKindScSpecEntryFunctionV0:
			if entry.FunctionV0 != nil {
				idx.functions[string(entry.FunctionV0.Name)] = entry.FunctionV0
			}
		}
	}
	return idx
}

// empty reports whether the specification names nothing at all, in which case
// the caller is better off rendering values the way it always has.
func (s *specIndex) empty() bool {
	return s == nil ||
		(len(s.events) == 0 && len(s.structs) == 0 && len(s.unions) == 0 &&
			len(s.enums) == 0 && len(s.errEnums) == 0 && len(s.functions) == 0)
}

// event returns the specification entry for an event, by name.
func (s *specIndex) event(name string) *xdr.ScSpecEventV0 {
	if s == nil {
		return nil
	}
	return s.events[name]
}

// findEvent returns the event a set of topics belongs to.
//
// The event's name is one of its topics, and which one depends on whether
// the event has prefix topics, so every symbol topic is tried until one
// names an event the specification knows.
func (s *specIndex) findEvent(topics []xdr.ScVal) *xdr.ScSpecEventV0 {
	if s == nil {
		return nil
	}
	for _, topic := range topics {
		sym, ok := topic.GetSym()
		if !ok {
			continue
		}
		if event, ok := s.events[string(sym)]; ok {
			return event
		}
	}
	return nil
}

// functionOutput returns the type of a function's return value.
func (s *specIndex) functionOutput(name string) (xdr.ScSpecTypeDef, bool) {
	if s == nil {
		return xdr.ScSpecTypeDef{}, false
	}
	fn, ok := s.functions[name]
	if !ok || len(fn.Outputs) == 0 {
		return xdr.ScSpecTypeDef{}, false
	}
	return fn.Outputs[0], true
}

// onlyFunction returns the single function a specification declares.
//
// It lets a caller leave the function name out when there is no ambiguity
// about which one is meant.
func (s *specIndex) onlyFunction() (string, bool) {
	if s == nil || len(s.functions) != 1 {
		return "", false
	}
	for name := range s.functions {
		return name, true
	}
	return "", false
}

// resolveType resolves a type definition into the shape of the value it
// describes.
//
// A struct and a union are both maps on the wire — a struct keyed by its
// field names, a union keyed by its case name — so both resolve to named
// fields.
func (s *specIndex) resolveType(def *xdr.ScSpecTypeDef) specType {
	if def == nil {
		return specType{shape: shapeValue}
	}

	switch def.Type {
	case xdr.ScSpecTypeScSpecTypeUdt:
		if def.Udt == nil {
			break
		}
		name := def.Udt.Name
		if st, ok := s.structs[name]; ok {
			fields := make([]specField, 0, len(st.Fields))
			for _, f := range st.Fields {
				fields = append(fields, specField{name: f.Name, def: &f.Type})
			}
			return specType{shape: shapeFields, fields: fields, def: def}
		}
		if union, ok := s.unions[name]; ok {
			fields := make([]specField, 0, len(union.Cases))
			for _, c := range union.Cases {
				fields = append(fields, specField{name: unionCaseName(c), def: unionCaseType(c)})
			}
			return specType{shape: shapeFields, fields: fields, def: def}
		}

	case xdr.ScSpecTypeScSpecTypeVec:
		if def.Vec != nil {
			return specType{shape: shapeSeq, def: &def.Vec.ElementType}
		}
	}

	return specType{shape: shapeValue, def: def}
}

// unionCaseName returns the name of a union's case.
func unionCaseName(c xdr.ScSpecUdtUnionCaseV0) string {
	switch c.Kind {
	case xdr.ScSpecUdtUnionCaseV0KindScSpecUdtUnionCaseVoidV0:
		if c.VoidCase != nil {
			return c.VoidCase.Name
		}
	case xdr.ScSpecUdtUnionCaseV0KindScSpecUdtUnionCaseTupleV0:
		if c.TupleCase != nil {
			return c.TupleCase.Name
		}
	}
	return ""
}

// unionCaseType returns the type of a union's case, which is a void case for
// a case that carries nothing and a tuple otherwise.
func unionCaseType(c xdr.ScSpecUdtUnionCaseV0) *xdr.ScSpecTypeDef {
	switch c.Kind {
	case xdr.ScSpecUdtUnionCaseV0KindScSpecUdtUnionCaseVoidV0:
		return &xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeVoid}
	case xdr.ScSpecUdtUnionCaseV0KindScSpecUdtUnionCaseTupleV0:
		if c.TupleCase != nil && len(c.TupleCase.Type) > 0 {
			return &c.TupleCase.Type[0]
		}
	}
	return &xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeVoid}
}

// eventDataType resolves the type of an event's data payload.
//
// The specification says how the data is arranged: a single value, a vector
// of the data parameters in order, or a map keyed by their names.
func (s *specIndex) eventDataType(event *xdr.ScSpecEventV0) specType {
	if event == nil {
		return specType{shape: shapeValue}
	}

	var data []xdr.ScSpecEventParamV0
	for _, p := range event.Params {
		if p.Location == xdr.ScSpecEventParamLocationV0ScSpecEventParamLocationData {
			data = append(data, p)
		}
	}

	switch event.DataFormat {
	case xdr.ScSpecEventDataFormatScSpecEventDataFormatMap:
		fields := make([]specField, 0, len(data))
		names := make([]string, 0, len(data))
		for _, p := range data {
			fields = append(fields, specField{name: p.Name, def: &p.Type})
			names = append(names, p.Name)
		}
		return specType{shape: shapeFields, fields: fields, names: names}

	case xdr.ScSpecEventDataFormatScSpecEventDataFormatVec:
		if len(data) == 1 {
			names := []string{data[0].Name}
			return specType{shape: shapeSeq, def: &data[0].Type, names: names}
		}
		return specType{shape: shapeSeq}

	case xdr.ScSpecEventDataFormatScSpecEventDataFormatSingleValue:
		if len(data) == 1 {
			resolved := s.resolveType(&data[0].Type)
			if resolved.shape == shapeValue {
				resolved.names = []string{data[0].Name}
			}
			return resolved
		}
		if len(data) > 1 {
			fields := make([]specField, 0, len(data))
			for _, p := range data {
				fields = append(fields, specField{name: p.Name, def: &p.Type})
			}
			return specType{shape: shapeFields, fields: fields}
		}
	}

	return specType{shape: shapeValue}
}

// eventTopicNames returns the names of an event's topics, in order.
//
// The event's own name is one of its topics, and the parameters that follow
// it are the ones the specification names. A layout the specification does
// not describe is left unnamed rather than guessed at: a wrong name is
// worse than none.
func (s *specIndex) eventTopicNames(event *xdr.ScSpecEventV0, topics []xdr.ScVal) []string {
	names := make([]string, len(topics))
	if event == nil {
		return names
	}

	nameIndex := -1
	for i, topic := range topics {
		sym, ok := topic.GetSym()
		if ok && string(sym) == string(event.Name) {
			nameIndex = i
			break
		}
	}
	if nameIndex < 0 {
		return names
	}

	params := 0
	for _, p := range event.Params {
		if p.Location == xdr.ScSpecEventParamLocationV0ScSpecEventParamLocationTopicList {
			params++
		}
	}
	if nameIndex+1+params != len(topics) {
		return names
	}

	index := nameIndex + 1
	for _, p := range event.Params {
		if p.Location != xdr.ScSpecEventParamLocationV0ScSpecEventParamLocationTopicList {
			continue
		}
		names[index] = p.Name
		index++
	}
	return names
}

// describeSpecValue renders a value using the names the specification gives
// it.
//
// The XDR type is kept alongside the names, so a reader can still see the
// wire shape the names came from.
func (s *specIndex) describeSpecValue(t specType, v xdr.ScVal) string {
	switch t.shape {
	case shapeFields:
		return s.describeSpecFields(t, v)
	case shapeSeq:
		return s.describeSpecSeq(t, v)
	default:
		return s.describeSpecLeaf(t, v)
	}
}

// describeSpecFields renders a map-shaped value by field name.
func (s *specIndex) describeSpecFields(t specType, v xdr.ScVal) string {
	m, ok := v.GetMap()
	if !ok || m == nil {
		return describeScVal(v)
	}

	entries := map[string]xdr.ScVal{}
	for _, entry := range *m {
		if sym, ok := entry.Key.GetSym(); ok {
			entries[string(sym)] = entry.Val
		}
	}

	parts := make([]string, 0, len(t.fields))
	used := map[string]bool{}
	for _, field := range t.fields {
		got, ok := entries[field.name]
		if !ok {
			parts = append(parts, fmt.Sprintf("%s: <missing>", field.name))
			continue
		}
		used[field.name] = true
		parts = append(parts, fmt.Sprintf("%s: %s",
			field.name, s.describeSpecValue(s.resolveType(field.def), got)))
	}

	// A key the specification does not name is still shown, because it is
	// part of what the contract produced.
	extra := make([]string, 0)
	for _, entry := range *m {
		sym, ok := entry.Key.GetSym()
		if !ok || used[string(sym)] {
			continue
		}
		extra = append(extra, fmt.Sprintf("%s: %s", string(sym), describeScVal(entry.Val)))
	}
	sort.Strings(extra)
	parts = append(parts, extra...)

	return "{" + strings.Join(parts, ", ") + "}"
}

// describeSpecSeq renders a sequence-shaped value, naming its elements when
// the specification does.
func (s *specIndex) describeSpecSeq(t specType, v xdr.ScVal) string {
	vec, ok := v.GetVec()
	if !ok || vec == nil {
		return describeScVal(v)
	}

	parts := make([]string, 0, len(*vec))
	for i, element := range *vec {
		rendered := s.describeSpecValue(s.resolveType(t.def), element)
		if i < len(t.names) && t.names[i] != "" {
			rendered = t.names[i] + "=" + rendered
		}
		parts = append(parts, rendered)
	}
	return "vec[" + strings.Join(parts, ", ") + "]"
}

// describeSpecLeaf renders a value with no named parts.
func (s *specIndex) describeSpecLeaf(t specType, v xdr.ScVal) string {
	if t.def != nil && t.def.Type == xdr.ScSpecTypeScSpecTypeUdt && t.def.Udt != nil {
		if name, ok := s.enumCaseName(t.def.Udt.Name, v); ok {
			return name
		}
	}
	return describeScVal(v)
}

// enumCaseName returns the name of the enum case a value carries.
func (s *specIndex) enumCaseName(enum string, v xdr.ScVal) (string, bool) {
	if s == nil {
		return "", false
	}
	e, ok := s.enums[enum]
	if !ok {
		return "", false
	}
	n, ok := v.GetU32()
	if !ok {
		return "", false
	}
	for _, c := range e.Cases {
		if c.Value == n {
			return c.Name, true
		}
	}
	return "", false
}

// dataMatches reports whether an event's payload is the one the matcher
// wants.
//
// A payload the specification describes — a map of field names, say — is
// matched against the fields the contract declares. Anything else is
// compared as it is, so a specification never changes what matches.
func (s *specIndex) dataMatches(event *xdr.ScSpecEventV0, got xdr.ScVal, want any) bool {
	if s == nil || s.empty() {
		wv, ok := scVal(want)
		return ok && got.Equals(wv)
	}

	if _, named := want.(map[string]any); !named {
		wv, ok := scVal(want)
		return ok && got.Equals(wv)
	}

	if event == nil {
		// The specification does not describe this event, so there are no
		// field names to match against.
		return false
	}

	return len(s.specDiff(s.eventDataType(event), want, got)) == 0
}

// describeWantedValue renders a wanted value the way the contract's
// specification describes it.
func (s *specIndex) describeWantedValue(t specType, want any) string {
	if s == nil || s.empty() {
		if val, ok := scVal(want); ok {
			return describeScVal(val)
		}
		return fmt.Sprintf("%v", want)
	}

	switch t.shape {
	case shapeFields:
		wanted, ok := want.(map[string]any)
		if !ok {
			return fmt.Sprintf("%v", want)
		}
		types := map[string]*xdr.ScSpecTypeDef{}
		for _, field := range t.fields {
			types[field.name] = field.def
		}
		names := make([]string, 0, len(wanted))
		for name := range wanted {
			names = append(names, name)
		}
		sort.Strings(names)

		parts := make([]string, 0, len(names))
		for _, name := range names {
			def, ok := types[name]
			if !ok {
				parts = append(parts, fmt.Sprintf("%s: %v", name, wanted[name]))
				continue
			}
			value, err := s.specScVal(s.resolveType(def), wanted[name])
			if err != nil {
				parts = append(parts, fmt.Sprintf("%s: %v", name, wanted[name]))
				continue
			}
			parts = append(parts, fmt.Sprintf("%s: %s",
				name, s.describeSpecValue(s.resolveType(def), value)))
		}
		return "{" + strings.Join(parts, ", ") + "}"

	case shapeSeq:
		wanted, ok := want.([]any)
		if !ok {
			return fmt.Sprintf("%v", want)
		}
		parts := make([]string, 0, len(wanted))
		for i, element := range wanted {
			value, err := s.specScVal(s.resolveType(t.def), element)
			if err != nil {
				parts = append(parts, fmt.Sprintf("%v", element))
				continue
			}
			rendered := s.describeSpecValue(s.resolveType(t.def), value)
			if i < len(t.names) && t.names[i] != "" {
				rendered = t.names[i] + "=" + rendered
			}
			parts = append(parts, rendered)
		}
		return "vec[" + strings.Join(parts, ", ") + "]"

	default:
		value, err := s.specScVal(t, want)
		if err != nil {
			return fmt.Sprintf("%v", want)
		}
		return s.describeSpecValue(t, value)
	}
}

// specDiff compares a wanted value with what the contract produced, and
// returns a line per difference.
//
// A wanted value that is not shaped the way the specification describes —
// a plain ScVal, say — falls back to comparing the two values as they are,
// so a specification improves the message without changing what matches.
func (s *specIndex) specDiff(t specType, want any, got xdr.ScVal) []string {
	switch t.shape {
	case shapeFields:
		return s.diffSpecFields(t, want, got)
	case shapeSeq:
		return s.diffSpecSeq(t, want, got)
	default:
		return s.diffSpecLeaf(t, want, got)
	}
}

// diffSpecFields compares the named parts of a map-shaped value.
func (s *specIndex) diffSpecFields(t specType, want any, got xdr.ScVal) []string {
	wanted, ok := want.(map[string]any)
	if !ok {
		return s.diffSpecLeaf(t, want, got)
	}

	m, ok := got.GetMap()
	if !ok || m == nil {
		return []string{fmt.Sprintf("want %s, got %s",
			s.describeSpecValue(t, got), describeScVal(got))}
	}

	actual := map[string]xdr.ScVal{}
	for _, entry := range *m {
		if sym, ok := entry.Key.GetSym(); ok {
			actual[string(sym)] = entry.Val
		}
	}

	types := map[string]*xdr.ScSpecTypeDef{}
	for _, field := range t.fields {
		types[field.name] = field.def
	}

	var diffs []string
	names := make([]string, 0, len(wanted))
	for name := range wanted {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		value, ok := actual[name]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("%s: <missing>", name))
			continue
		}
		fieldType := s.resolveType(types[name])
		for _, diff := range s.specDiff(fieldType, wanted[name], value) {
			diffs = append(diffs, name+": "+diff)
		}
	}

	for name := range actual {
		if _, ok := wanted[name]; !ok {
			diffs = append(diffs, fmt.Sprintf("%s: <unexpected>", name))
		}
	}
	sort.Strings(diffs)
	return diffs
}

// diffSpecSeq compares the elements of a sequence-shaped value.
func (s *specIndex) diffSpecSeq(t specType, want any, got xdr.ScVal) []string {
	wanted, ok := want.([]any)
	if !ok {
		return s.diffSpecLeaf(t, want, got)
	}
	vec, ok := got.GetVec()
	if !ok || vec == nil {
		return []string{fmt.Sprintf("want %s, got %s",
			s.describeSpecValue(t, got), describeScVal(got))}
	}
	if len(*vec) != len(wanted) {
		return []string{fmt.Sprintf("want %d element(s), got %d",
			len(wanted), len(*vec))}
	}

	var diffs []string
	for i, element := range *vec {
		fieldType := s.resolveType(t.def)
		for _, diff := range s.specDiff(fieldType, wanted[i], element) {
			if i < len(t.names) && t.names[i] != "" {
				diffs = append(diffs, t.names[i]+": "+diff)
				continue
			}
			diffs = append(diffs, diff)
		}
	}
	return diffs
}

// diffSpecLeaf compares a value with no named parts.
func (s *specIndex) diffSpecLeaf(t specType, want any, got xdr.ScVal) []string {
	expected, err := s.specScVal(t, want)
	if err != nil {
		return []string{fmt.Sprintf("want %v, got %s", want, s.describeSpecValue(t, got))}
	}
	if expected.Equals(got) {
		return nil
	}
	return []string{fmt.Sprintf("want %s, got %s",
		s.describeSpecValue(t, expected), s.describeSpecValue(t, got))}
}

// specScVal converts a wanted value into an ScVal, using the type the
// specification gives it.
//
// The conversions a test needs are the ones a contract author would write:
// a strkey address for an address, an integer for a number, a string for a
// string or a symbol, a slice for a vector.
func (s *specIndex) specScVal(t specType, want any) (xdr.ScVal, error) {
	if t.shape == shapeFields {
		wanted, ok := want.(map[string]any)
		if !ok {
			return xdr.ScVal{}, fmt.Errorf("want a map of field names, got %T", want)
		}
		types := map[string]*xdr.ScSpecTypeDef{}
		for _, field := range t.fields {
			types[field.name] = field.def
		}
		entries := make([]xdr.ScMapEntry, 0, len(wanted))
		names := make([]string, 0, len(wanted))
		for name := range wanted {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			def, ok := types[name]
			if !ok {
				return xdr.ScVal{}, fmt.Errorf("field %q is not in the contract specification", name)
			}
			value, err := s.specScVal(s.resolveType(def), wanted[name])
			if err != nil {
				return xdr.ScVal{}, fmt.Errorf("field %q: %w", name, err)
			}
			sym := xdr.ScSymbol(name)
			entries = append(entries, xdr.ScMapEntry{
				Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym},
				Val: value,
			})
		}
		return scMapValue(entries), nil
	}

	if t.shape == shapeSeq {
		wanted, ok := want.([]any)
		if !ok {
			return xdr.ScVal{}, fmt.Errorf("want a list, got %T", want)
		}
		elements := make([]xdr.ScVal, 0, len(wanted))
		for i, element := range wanted {
			value, err := s.specScVal(s.resolveType(t.def), element)
			if err != nil {
				return xdr.ScVal{}, fmt.Errorf("element %d: %w", i, err)
			}
			elements = append(elements, value)
		}
		return scVecValue(elements), nil
	}

	return s.specScValDef(t.def, want)
}

// specScValDef converts a wanted value into an ScVal for a type definition.
func (s *specIndex) specScValDef(def *xdr.ScSpecTypeDef, want any) (xdr.ScVal, error) {
	// An ScVal is already the value; it is used as it is.
	if val, ok := want.(xdr.ScVal); ok {
		return val, nil
	}
	if val, ok := want.(*xdr.ScVal); ok && val != nil {
		return *val, nil
	}
	if def == nil {
		return xdr.ScVal{}, fmt.Errorf("want an ScVal, got %T", want)
	}

	switch def.Type {
	case xdr.ScSpecTypeScSpecTypeVal:
		return xdr.ScVal{}, fmt.Errorf("want an ScVal, got %T", want)

	case xdr.ScSpecTypeScSpecTypeVoid:
		return xdr.ScVal{Type: xdr.ScValTypeScvVoid}, nil

	case xdr.ScSpecTypeScSpecTypeBool:
		b, ok := want.(bool)
		if !ok {
			return xdr.ScVal{}, fmt.Errorf("want a bool, got %T", want)
		}
		return xdr.ScVal{Type: xdr.ScValTypeScvBool, B: &b}, nil

	case xdr.ScSpecTypeScSpecTypeU32:
		n, err := specUint(want, math32)
		if err != nil {
			return xdr.ScVal{}, err
		}
		v := xdr.Uint32(n)
		return xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &v}, nil

	case xdr.ScSpecTypeScSpecTypeI32:
		n, err := specInt(want, math32)
		if err != nil {
			return xdr.ScVal{}, err
		}
		v := xdr.Int32(n)
		return xdr.ScVal{Type: xdr.ScValTypeScvI32, I32: &v}, nil

	case xdr.ScSpecTypeScSpecTypeU64:
		n, err := specUint(want, math64)
		if err != nil {
			return xdr.ScVal{}, err
		}
		v := xdr.Uint64(n)
		return xdr.ScVal{Type: xdr.ScValTypeScvU64, U64: &v}, nil

	case xdr.ScSpecTypeScSpecTypeI64:
		n, err := specInt(want, math64)
		if err != nil {
			return xdr.ScVal{}, err
		}
		v := xdr.Int64(n)
		return xdr.ScVal{Type: xdr.ScValTypeScvI64, I64: &v}, nil

	case xdr.ScSpecTypeScSpecTypeU128:
		n, err := specBigInt(want)
		if err != nil {
			return xdr.ScVal{}, err
		}
		if n.Sign() < 0 {
			return xdr.ScVal{}, fmt.Errorf("want a non-negative number, got %s", n)
		}
		parts := bigToUInt128(n)
		return xdr.ScVal{Type: xdr.ScValTypeScvU128, U128: &parts}, nil

	case xdr.ScSpecTypeScSpecTypeI128:
		n, err := specBigInt(want)
		if err != nil {
			return xdr.ScVal{}, err
		}
		parts := bigToInt128(n)
		return xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &parts}, nil

	case xdr.ScSpecTypeScSpecTypeTimepoint:
		n, err := specInt(want, math64)
		if err != nil {
			return xdr.ScVal{}, err
		}
		v := xdr.TimePoint(n)
		return xdr.ScVal{Type: xdr.ScValTypeScvTimepoint, Timepoint: &v}, nil

	case xdr.ScSpecTypeScSpecTypeDuration:
		n, err := specUint(want, math64)
		if err != nil {
			return xdr.ScVal{}, err
		}
		v := xdr.Duration(n)
		return xdr.ScVal{Type: xdr.ScValTypeScvDuration, Duration: &v}, nil

	case xdr.ScSpecTypeScSpecTypeString:
		str, ok := want.(string)
		if !ok {
			return xdr.ScVal{}, fmt.Errorf("want a string, got %T", want)
		}
		v := xdr.ScString(str)
		return xdr.ScVal{Type: xdr.ScValTypeScvString, Str: &v}, nil

	case xdr.ScSpecTypeScSpecTypeSymbol:
		str, ok := want.(string)
		if !ok {
			return xdr.ScVal{}, fmt.Errorf("want a symbol, got %T", want)
		}
		v := xdr.ScSymbol(str)
		return xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &v}, nil

	case xdr.ScSpecTypeScSpecTypeBytes, xdr.ScSpecTypeScSpecTypeBytesN:
		raw, ok := want.([]byte)
		if !ok {
			return xdr.ScVal{}, fmt.Errorf("want bytes, got %T", want)
		}
		v := xdr.ScBytes(raw)
		return xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: &v}, nil

	case xdr.ScSpecTypeScSpecTypeAddress:
		return specAddress(want)

	case xdr.ScSpecTypeScSpecTypeOption:
		if want == nil {
			return xdr.ScVal{Type: xdr.ScValTypeScvVoid}, nil
		}
		if def.Option == nil {
			return xdr.ScVal{}, fmt.Errorf("want an option value, got %T", want)
		}
		return s.specScValDef(&def.Option.ValueType, want)

	case xdr.ScSpecTypeScSpecTypeVec:
		if def.Vec == nil {
			return xdr.ScVal{}, fmt.Errorf("want a list, got %T", want)
		}
		return s.specScVal(specType{shape: shapeSeq, def: &def.Vec.ElementType}, want)

	case xdr.ScSpecTypeScSpecTypeTuple:
		if def.Tuple == nil {
			return xdr.ScVal{}, fmt.Errorf("want a list, got %T", want)
		}
		elements, ok := want.([]any)
		if !ok {
			return xdr.ScVal{}, fmt.Errorf("want a list, got %T", want)
		}
		if len(elements) != len(def.Tuple.ValueTypes) {
			return xdr.ScVal{}, fmt.Errorf("want %d element(s), got %d",
				len(def.Tuple.ValueTypes), len(elements))
		}
		values := make([]xdr.ScVal, 0, len(elements))
		for i, element := range elements {
			value, err := s.specScValDef(&def.Tuple.ValueTypes[i], element)
			if err != nil {
				return xdr.ScVal{}, fmt.Errorf("element %d: %w", i, err)
			}
			values = append(values, value)
		}
		return scVecValue(values), nil

	case xdr.ScSpecTypeScSpecTypeMap:
		if def.Map == nil {
			return xdr.ScVal{}, fmt.Errorf("want a map, got %T", want)
		}
		wanted, ok := want.(map[string]any)
		if !ok {
			return xdr.ScVal{}, fmt.Errorf("want a map, got %T", want)
		}
		names := make([]string, 0, len(wanted))
		for name := range wanted {
			names = append(names, name)
		}
		sort.Strings(names)
		entries := make([]xdr.ScMapEntry, 0, len(names))
		for _, name := range names {
			key, err := s.specScValDef(&def.Map.KeyType, name)
			if err != nil {
				return xdr.ScVal{}, fmt.Errorf("key %q: %w", name, err)
			}
			value, err := s.specScValDef(&def.Map.ValueType, wanted[name])
			if err != nil {
				return xdr.ScVal{}, fmt.Errorf("value %q: %w", name, err)
			}
			entries = append(entries, xdr.ScMapEntry{Key: key, Val: value})
		}
		return scMapValue(entries), nil

	case xdr.ScSpecTypeScSpecTypeUdt:
		if def.Udt == nil {
			return xdr.ScVal{}, fmt.Errorf("want a value of %T, got %T", want, want)
		}
		name := def.Udt.Name
		if st, ok := s.structs[name]; ok {
			fields := make([]specField, 0, len(st.Fields))
			for _, f := range st.Fields {
				fields = append(fields, specField{name: f.Name, def: &f.Type})
			}
			return s.specScVal(specType{shape: shapeFields, fields: fields}, want)
		}
		if union, ok := s.unions[name]; ok {
			fields := make([]specField, 0, len(union.Cases))
			for _, c := range union.Cases {
				fields = append(fields, specField{name: unionCaseName(c), def: unionCaseType(c)})
			}
			return s.specScVal(specType{shape: shapeFields, fields: fields}, want)
		}
		if enum, ok := s.enums[name]; ok {
			str, ok := want.(string)
			if !ok {
				return xdr.ScVal{}, fmt.Errorf("want an enum case name, got %T", want)
			}
			for _, c := range enum.Cases {
				if c.Name == str {
					v := c.Value
					return xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &v}, nil
				}
			}
			return xdr.ScVal{}, fmt.Errorf("enum %s has no case named %q", name, str)
		}
		return xdr.ScVal{}, fmt.Errorf("the contract specification does not describe %s", name)

	case xdr.ScSpecTypeScSpecTypeError:
		return xdr.ScVal{}, fmt.Errorf("want an error value, got %T", want)

	case xdr.ScSpecTypeScSpecTypeResult:
		return xdr.ScVal{}, fmt.Errorf("want a result value, got %T", want)
	}

	return xdr.ScVal{}, fmt.Errorf("the contract specification does not describe %T", want)
}

// scVecValue wraps elements as a vector value.
func scVecValue(elements []xdr.ScVal) xdr.ScVal {
	vec := xdr.ScVec(elements)
	ptr := &vec
	return xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &ptr}
}

// scMapValue wraps entries as a map value.
func scMapValue(entries []xdr.ScMapEntry) xdr.ScVal {
	m := xdr.ScMap(entries)
	ptr := &m
	return xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &ptr}
}

// specAddress converts a wanted value into an address.
func specAddress(want any) (xdr.ScVal, error) {
	switch val := want.(type) {
	case xdr.ScAddress:
		return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &val}, nil
	case *xdr.ScAddress:
		if val == nil {
			return xdr.ScVal{}, fmt.Errorf("want an address, got nil")
		}
		return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: val}, nil
	case string:
		addr, err := parseScAddress(val)
		if err != nil {
			return xdr.ScVal{}, err
		}
		return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &addr}, nil
	default:
		return xdr.ScVal{}, fmt.Errorf("want an address, got %T", want)
	}
}

// parseScAddress reads a strkey address into the address type it names.
//
// A contract may hold an account, a contract, a muxed account, a liquidity
// pool or a claimable balance, and the strkey version byte says which.
func parseScAddress(s string) (xdr.ScAddress, error) {
	version, raw, err := strkey.DecodeAny(s)
	if err != nil {
		return xdr.ScAddress{}, fmt.Errorf("want an address: %w", err)
	}

	switch version {
	case strkey.VersionByteAccountID:
		if len(raw) != 32 {
			return xdr.ScAddress{}, fmt.Errorf("want an account address, got %d bytes", len(raw))
		}
		var key xdr.Uint256
		copy(key[:], raw)
		return xdr.ScAddress{
			Type:      xdr.ScAddressTypeScAddressTypeAccount,
			AccountId: &xdr.AccountId{Type: xdr.PublicKeyTypePublicKeyTypeEd25519, Ed25519: &key},
		}, nil

	case strkey.VersionByteContract:
		if len(raw) != 32 {
			return xdr.ScAddress{}, fmt.Errorf("want a contract address, got %d bytes", len(raw))
		}
		var id xdr.ContractId
		copy(id[:], raw)
		return xdr.ScAddress{
			Type:       xdr.ScAddressTypeScAddressTypeContract,
			ContractId: &id,
		}, nil

	case strkey.VersionByteMuxedAccount:
		if len(raw) != 40 {
			return xdr.ScAddress{}, fmt.Errorf("want a muxed account address, got %d bytes", len(raw))
		}
		id := xdr.Uint64(0)
		for _, b := range raw[:8] {
			id = id<<8 | xdr.Uint64(b)
		}
		var key xdr.Uint256
		copy(key[:], raw[8:])
		return xdr.ScAddress{
			Type: xdr.ScAddressTypeScAddressTypeMuxedAccount,
			MuxedAccount: &xdr.MuxedEd25519Account{
				Id:      id,
				Ed25519: key,
			},
		}, nil

	case strkey.VersionByteLiquidityPool:
		if len(raw) != 32 {
			return xdr.ScAddress{}, fmt.Errorf("want a liquidity pool address, got %d bytes", len(raw))
		}
		var id xdr.PoolId
		copy(id[:], raw)
		return xdr.ScAddress{
			Type:            xdr.ScAddressTypeScAddressTypeLiquidityPool,
			LiquidityPoolId: &id,
		}, nil

	case strkey.VersionByteClaimableBalance:
		if len(raw) != 33 {
			return xdr.ScAddress{}, fmt.Errorf("want a claimable balance address, got %d bytes", len(raw))
		}
		var hash xdr.Hash
		copy(hash[:], raw[1:])
		return xdr.ScAddress{
			Type: xdr.ScAddressTypeScAddressTypeClaimableBalance,
			ClaimableBalanceId: &xdr.ClaimableBalanceId{
				Type: xdr.ClaimableBalanceIdTypeClaimableBalanceIdTypeV0,
				V0:   &hash,
			},
		}, nil

	default:
		return xdr.ScAddress{}, fmt.Errorf("want an address, got %q", s)
	}
}

// Bit sizes used when checking that a number fits the type the specification
// gives it.
const (
	math32 = 32
	math64 = 64
)

// specUint reads a wanted value as an unsigned integer.
func specUint(want any, bits int) (uint64, error) {
	n, err := specBigInt(want)
	if err != nil {
		return 0, err
	}
	if n.Sign() < 0 {
		return 0, fmt.Errorf("want a non-negative number, got %s", n)
	}
	limit := new(big.Int).Lsh(big.NewInt(1), uint(bits))
	if n.Cmp(limit) >= 0 {
		return 0, fmt.Errorf("want a number that fits in %d bits, got %s", bits, n)
	}
	return n.Uint64(), nil
}

// specInt reads a wanted value as a signed integer.
func specInt(want any, bits int) (int64, error) {
	n, err := specBigInt(want)
	if err != nil {
		return 0, err
	}
	limit := new(big.Int).Lsh(big.NewInt(1), uint(bits-1))
	if n.Cmp(limit) >= 0 || n.Cmp(new(big.Int).Neg(limit)) < 0 {
		return 0, fmt.Errorf("want a number that fits in %d bits, got %s", bits, n)
	}
	return n.Int64(), nil
}

// specBigInt reads a wanted value as an arbitrary-precision integer.
func specBigInt(want any) (*big.Int, error) {
	switch val := want.(type) {
	case *big.Int:
		if val == nil {
			return nil, fmt.Errorf("want a number, got nil")
		}
		return val, nil
	case big.Int:
		return &val, nil
	case int:
		return big.NewInt(int64(val)), nil
	case int8:
		return big.NewInt(int64(val)), nil
	case int16:
		return big.NewInt(int64(val)), nil
	case int32:
		return big.NewInt(int64(val)), nil
	case int64:
		return big.NewInt(val), nil
	case uint:
		return new(big.Int).SetUint64(uint64(val)), nil
	case uint8:
		return new(big.Int).SetUint64(uint64(val)), nil
	case uint16:
		return new(big.Int).SetUint64(uint64(val)), nil
	case uint32:
		return new(big.Int).SetUint64(uint64(val)), nil
	case uint64:
		return new(big.Int).SetUint64(val), nil
	case string:
		n, ok := new(big.Int).SetString(val, 10)
		if !ok {
			return nil, fmt.Errorf("want a number, got %q", val)
		}
		return n, nil
	default:
		return nil, fmt.Errorf("want a number, got %T", want)
	}
}

// bigToUInt128 splits a non-negative integer into the two words an ScU128
// carries. The low word holds the low 64 bits and the high word the next
// 32, so the value is Hi*2^64 + Lo.
func bigToUInt128(n *big.Int) xdr.UInt128Parts {
	value := new(big.Int).Set(n)

	lo := new(big.Int).And(value, big.NewInt(0xffffffff))
	value.Rsh(value, 32)
	mid := new(big.Int).And(value, big.NewInt(0xffffffff))
	value.Rsh(value, 32)
	hi := new(big.Int).And(value, big.NewInt(0xffffffff))

	return xdr.UInt128Parts{
		Hi: xdr.Uint64(hi.Uint64()),
		Lo: xdr.Uint64(mid.Uint64()<<32 | lo.Uint64()),
	}
}

// bigToInt128 splits an integer into the two words an ScI128 carries. The
// low word holds the low 64 bits and the high word the next 32, sign
// extended.
func bigToInt128(n *big.Int) xdr.Int128Parts {
	value := new(big.Int).Set(n)

	lo := new(big.Int).And(value, big.NewInt(0xffffffff))
	value.Rsh(value, 32)
	mid := new(big.Int).And(value, big.NewInt(0xffffffff))
	value.Rsh(value, 32)
	hi := new(big.Int).And(value, big.NewInt(0xffffffff))

	return xdr.Int128Parts{
		Hi: xdr.Int64(int64(hi.Uint64())),
		Lo: xdr.Uint64(mid.Uint64()<<32 | lo.Uint64()),
	}
}

// ContractSpec extracts a contract's interface specification from a compiled
// contract WASM module.
//
// The specification travels inside the module as a custom section, so a test
// that has the contract's bytes can name its fields without a second file:
//
//	spec, err := focus.ContractSpec(wasmBytes)
//	if err != nil {
//	    t.Fatal(err)
//	}
//	focus.AssertEventEmitted(t, meta, focus.Event{
//	    Topics: []any{"transfer"},
//	    Data:   map[string]any{"amount": 100},
//	    Spec:   spec,
//	})
//
// A module without a specification section is not an error: it simply has no
// specification, and an empty list is returned.
func ContractSpec(wasm []byte) ([]xdr.ScSpecEntry, error) {
	payload, err := contractSpecPayload(wasm)
	if err != nil {
		return nil, err
	}
	if payload == nil {
		return nil, nil
	}
	return decodeSpecEntries(payload)
}

// contractSpecSection is the name of the custom section a contract's
// specification is stored in.
const contractSpecSection = "contractspec"

// contractSpecPayload finds the specification section of a WASM module and
// returns its payload.
//
// The module is walked rather than decoded, because the WebAssembly binary
// format is simple enough to read directly and the section is the only part
// of it that matters here.
func contractSpecPayload(wasm []byte) ([]byte, error) {
	if len(wasm) < 8 {
		return nil, fmt.Errorf("the contract is %d bytes long, which is too short to be a WASM module", len(wasm))
	}
	if string(wasm[:4]) != "\x00asm" {
		return nil, fmt.Errorf("the contract does not start with the WASM magic number")
	}

	rest := wasm[8:]
	for len(rest) > 0 {
		id := rest[0]
		size, read, err := readLEB128(rest[1:])
		if err != nil {
			return nil, fmt.Errorf("reading the contract's section size: %w", err)
		}
		rest = rest[1+read:]
		if uint64(len(rest)) < size {
			return nil, fmt.Errorf("the contract's section claims %d bytes, but only %d are left", size, len(rest))
		}
		content := rest[:size]
		rest = rest[size:]

		if id != 0 {
			continue
		}
		name, read, err := readLEB128(content)
		if err != nil {
			return nil, fmt.Errorf("reading the contract's section name: %w", err)
		}
		if uint64(len(content)) < uint64(read)+name {
			return nil, fmt.Errorf("the contract's section name claims %d bytes, but only %d are left", name, len(content))
		}
		sectionName := string(content[read : uint64(read)+name])
		if !strings.HasPrefix(sectionName, contractSpecSection) {
			continue
		}
		return content[uint64(read)+name:], nil
	}
	return nil, nil
}

// readLEB128 reads an unsigned LEB128 integer, as the WebAssembly binary
// format encodes lengths.
func readLEB128(data []byte) (value uint64, read int, err error) {
	for i := 0; i < len(data); i++ {
		if i > 9 {
			return 0, 0, fmt.Errorf("the integer is too long to be a length")
		}
		b := data[i]
		value |= uint64(b&0x7f) << (7 * uint(i))
		if b&0x80 == 0 {
			return value, i + 1, nil
		}
	}
	return 0, 0, fmt.Errorf("the integer is truncated")
}

// decodeSpecEntries decodes the XDR-encoded entries of a specification.
func decodeSpecEntries(payload []byte) ([]xdr.ScSpecEntry, error) {
	if len(payload) < 4 {
		return nil, fmt.Errorf("the contract's specification is %d bytes long, which is too short to hold a count", len(payload))
	}

	count := binary.BigEndian.Uint32(payload[:4])
	rest := payload[4:]

	entries := make([]xdr.ScSpecEntry, 0, count)
	decoder := xdr.NewBytesDecoder()
	for i := uint32(0); i < count; i++ {
		var entry xdr.ScSpecEntry
		n, err := decoder.DecodeBytes(&entry, rest)
		if err != nil {
			return nil, fmt.Errorf("decoding the contract's specification: %w", err)
		}
		rest = rest[n:]
		entries = append(entries, entry)
	}
	return entries, nil
}
