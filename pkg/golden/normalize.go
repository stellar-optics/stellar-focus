package golden

import "strconv"

// applyNormalizers runs every normalizer in fns, in order, over the value
// tree rooted at value, each seeing the value the one before it left in
// place. It walks a generic JSON tree as produced by encoding/json when
// unmarshalled into an any: map[string]any, []any, and scalars.
//
// Each normalizer is applied top-down: a node is normalized before its
// children are visited, so a normalizer that replaces a node with a
// different container is free to have its replacement's own children
// normalized in turn, and one that replaces a node with a scalar stops the
// walk there.
//
// value is never mutated in place; every modified node is rebuilt into a
// new map or slice, so a tree built by one call shares no mutable state
// with another — the requirement that lets WithCustomNormalizer run safely
// under AssertAllParallel.
func applyNormalizers(path string, value any, fns []NormalizerFunc) any {
	for _, fn := range fns {
		value = normalizeTree(path, value, fn)
	}
	return value
}

// normalizeTree applies a single normalizer over value and its descendants.
func normalizeTree(path string, value any, fn NormalizerFunc) any {
	value = fn(path, value)
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, child := range v {
			out[k] = normalizeTree(joinPath(path, k), child, fn)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = normalizeTree(joinPath(path, strconv.Itoa(i)), child, fn)
		}
		return out
	default:
		return value
	}
}

// joinPath appends a segment to a normalizer path, matching the
// slash-separated layout NormalizerFunc documents.
func joinPath(path, segment string) string {
	if path == "" {
		return segment
	}
	return path + "/" + segment
}
