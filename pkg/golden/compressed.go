package golden

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// GzipSuffix marks a gzipped golden file, e.g. envelope.golden.json.gz.
const GzipSuffix = ".gz"

// ZstdSuffix marks a zstd-compressed golden file, e.g. envelope.golden.json.zst.
const ZstdSuffix = ".zst"

// gzipMagic opens every gzip member, so a compressed file is recognised by
// what it is rather than by what it is called.
var gzipMagic = []byte{0x1f, 0x8b}

// zstdMagic is the standard magic number for Zstandard compressed data (0xFD2FB528).
var zstdMagic = []byte{0x28, 0xb5, 0x2f, 0xfd}

// IsCompressed reports whether path names a compressed file (.gz or .zst).
func IsCompressed(path string) bool {
	return strings.HasSuffix(path, GzipSuffix) || strings.HasSuffix(path, ZstdSuffix)
}

// ReadFile reads a golden file's content: transparently decompressing gzip or zstd
// payloads and normalizing line endings, so callers compare content rather than storage.
func ReadFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var decompressed []byte
	var decErr error

	if bytes.HasPrefix(raw, gzipMagic) || strings.HasSuffix(path, GzipSuffix) {
		decompressed, decErr = gunzip(path, raw)
	} else if bytes.HasPrefix(raw, zstdMagic) || strings.HasSuffix(path, ZstdSuffix) {
		decompressed, decErr = gunzstd(path, raw)
	} else {
		return NormalizeLineEndings(raw), nil
	}

	if decErr != nil {
		return nil, decErr
	}
	return NormalizeLineEndings(decompressed), nil
}

// gunzip decompresses raw gzip payload.
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
	return out, nil
}

// gunzstd decompresses raw zstd payload.
func gunzstd(path string, raw []byte) ([]byte, error) {
	dec, err := zstd.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decompressing %s: %w", path, err)
	}
	defer dec.Close()

	out, err := io.ReadAll(dec)
	if err != nil {
		return nil, fmt.Errorf("decompressing %s: %w", path, err)
	}
	return out, nil
}

// WriteFile writes data to path, creating the directory if needed, storing it
// compressed when the path asks for .gz or .zst and as plain bytes otherwise.
func WriteFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}

	out := data
	var err error

	if strings.HasSuffix(path, GzipSuffix) {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		if _, err := zw.Write(data); err != nil {
			return fmt.Errorf("compressing %s: %w", path, err)
		}
		if err := zw.Close(); err != nil {
			return fmt.Errorf("compressing %s: %w", path, err)
		}
		out = buf.Bytes()
	} else if strings.HasSuffix(path, ZstdSuffix) {
		var buf bytes.Buffer
		zw, zErr := zstd.NewWriter(&buf, zstd.WithEncoderLevel(zstd.DefaultCompression))
		if zErr != nil {
			return fmt.Errorf("compressing %s: %w", path, zErr)
		}
		if _, err := zw.Write(data); err != nil {
			_ = zw.Close()
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
