// Package searchtext encodes the text of a session search document for
// storage. The store writes it; the database layer registers ao_search_body()
// so SQL (the index's delete trigger) can read it back.
package searchtext

import (
	"bytes"
	"compress/flate"
	"hash/fnv"
	"io"
	"strings"
	"sync"
)

// A session search document's text is stored once, compressed, and read back
// only to cut the snippets of a page of results. The full-text index is
// contentless (it keeps postings, not text), so this column is the only copy.
//
// The first byte says how the rest is encoded. Short texts are stored as they
// are: deflate's framing would make them longer, and most messages are short.
// Command output, which is most of the bytes, compresses several times over.
const (
	searchBodyRaw     byte = 0
	searchBodyDeflate byte = 1
	// searchBodyCompressMin is the length below which compressing is not worth it.
	searchBodyCompressMin = 256
)

var searchBodyWriters = sync.Pool{New: func() any {
	writer, _ := flate.NewWriter(io.Discard, flate.BestSpeed)
	return writer
}}

// Encode returns the stored form of a document's text.
func Encode(text string) []byte {
	if text == "" {
		return nil
	}
	if len(text) >= searchBodyCompressMin {
		var buffer bytes.Buffer
		buffer.Grow(len(text)/3 + 16)
		buffer.WriteByte(searchBodyDeflate)
		writer, ok := searchBodyWriters.Get().(*flate.Writer)
		if !ok || writer == nil {
			writer, _ = flate.NewWriter(io.Discard, flate.BestSpeed)
		}
		writer.Reset(&buffer)
		_, writeErr := writer.Write([]byte(text))
		closeErr := writer.Close()
		searchBodyWriters.Put(writer)
		// Keep the compressed form only when it is smaller.
		if writeErr == nil && closeErr == nil && buffer.Len() < len(text)+1 {
			return buffer.Bytes()
		}
	}
	out := make([]byte, 0, len(text)+1)
	out = append(out, searchBodyRaw)
	return append(out, text...)
}

// Decode returns the text of a stored body. A body that cannot be
// decoded reads as empty: it only costs that row its snippet.
func Decode(stored []byte) string {
	if len(stored) == 0 {
		return ""
	}
	switch stored[0] {
	case searchBodyRaw:
		return string(stored[1:])
	case searchBodyDeflate:
		reader := flate.NewReader(bytes.NewReader(stored[1:]))
		defer func() { _ = reader.Close() }()
		text, err := io.ReadAll(reader)
		if err != nil {
			return ""
		}
		return string(text)
	default:
		return ""
	}
}

// Hash fingerprints what a document puts in the index. A pass that
// re-reads an unchanged document compares hashes and leaves its index row
// alone, which is what keeps incremental passes cheap.
func Hash(title, body, paths string) int64 {
	hasher := fnv.New64a()
	for _, part := range []string{title, body, paths} {
		_, _ = hasher.Write([]byte(part))
		_, _ = hasher.Write([]byte{0})
	}
	return int64(hasher.Sum64()) //nolint:gosec // a fingerprint; wrapping is fine
}

// Normalize makes a document field safe to send to SQLite inside JSON and to
// store: invalid UTF-8 becomes U+FFFD (JSON encoding would otherwise rewrite
// it, and the index and the stored text would disagree) and NUL becomes a
// space (SQLite text functions stop at NUL). The index row and the stored
// text are written from the same normalized value, so deleting a row with its
// stored text removes exactly what was indexed.
func Normalize(value string) string {
	if strings.IndexByte(value, 0) >= 0 {
		value = strings.ReplaceAll(value, "\x00", " ")
	}
	return strings.ToValidUTF8(value, "\uFFFD")
}
