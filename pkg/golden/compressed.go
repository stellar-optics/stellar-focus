package golden

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// GzipSuffix marks a gzipped golden file, e.g. envelope.golden.json.gz.
const GzipSuffix = ".gz"

// gzipMagic opens every gzip member, so a compressed file is recognised by
// what it is rather than by what it is called: a fixture renamed without its
// .gz still loads, and a mislabelled plain file is never handed to a
// decompressor.
var gzipMagic = []byte{0x1f, 0x8b}

// IsCompressed reports whether path names a gzipped file. It decides how
// WriteFile stores content, which is the one place the name matters.
func IsCompressed(path string) bool {
	return strings.HasSuffix(path, GzipSuffix)
}

// ReadFile reads a golden file's content: transparently decompressing a gzip
// payload and normalizing line endings, so callers — Load, assertions,
// checksums, diffs — compare content rather than storage.
//
// A missing file is reported as os.ErrNotExist wrapped, so errors.Is keeps
// working and Assert can still offer to create it.
func ReadFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(raw, gzipMagic) {
		return NormalizeLineEndings(raw), nil
	}
	return gunzip(path, raw)
}

// gunzip decompresses raw, validating the checksum gzip carries, and
// normalizes the result: decompressed fixture text goes through the same
// line-ending handling as an uncompressed one.
func gunzip(path string, raw []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decompressing %s: %w", path, err)
	}
	out, readErr := io.ReadAll(zr)
	closeErr := zr.Close()
	if readErr != nil {
		return nil, fmt.Errorf("decompressing %s: %w", path, readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("decompressing %s: %w", path, closeErr)
	}
	return NormalizeLineEndings(out), nil
}

// WriteFile writes data to path, creating the directory if needed, storing it
// gzip-compressed when the path asks for .gz and as plain bytes otherwise.
// The same content written to the same path produces the same bytes, so a
// compressed fixture that only decompresses does not churn in review.
func WriteFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}

	out := data
	if IsCompressed(path) {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		if _, err := zw.Write(data); err != nil {
			return fmt.Errorf("compressing %s: %w", path, err)
		}
		if err := zw.Close(); err != nil {
			return fmt.Errorf("compressing %s: %w", path, err)
		}
		out = buf.Bytes()
	}

	if err := os.WriteFile(path, out, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
