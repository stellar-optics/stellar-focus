package golden

import "bytes"

// NormalizeLineEndings rewrites every CRLF and lone-CR line ending in b to a
// plain LF, returning b itself — untouched and uncopied — when there is no CR
// to rewrite.
//
// Golden files are compared byte-for-byte, hashed for integrity manifests and
// diffed in review. A checkout on Windows turns the repository's LF endings
// into CRLF, which would make all three disagree with the very same file
// checked out on Linux, with nothing in the failure explaining why. Bytes are
// normalized at the point they enter this package instead: assertions,
// checksums and diffs see the file's content rather than its working-copy
// encoding. Files written here are still LF, and nothing outside line
// endings is altered.
func NormalizeLineEndings(b []byte) []byte {
	if bytes.IndexByte(b, '\r') < 0 {
		return b
	}
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	return bytes.ReplaceAll(b, []byte("\r"), []byte("\n"))
}
