package searchtext

import (
	"bytes"
	"compress/flate"
	"hash/fnv"
	"io"
	"strings"
	"sync"
)

const (
	searchBodyRaw         byte = 0
	searchBodyDeflate     byte = 1
	searchBodyCompressMin      = 256
)

var searchBodyWriters = sync.Pool{New: func() any {
	writer, _ := flate.NewWriter(io.Discard, flate.BestSpeed)
	return writer
}}

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
		if writeErr == nil && closeErr == nil && buffer.Len() < len(text)+1 {
			return buffer.Bytes()
		}
	}
	out := make([]byte, 0, len(text)+1)
	out = append(out, searchBodyRaw)
	return append(out, text...)
}

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

func Hash(title, body, paths string) int64 {
	hasher := fnv.New64a()
	for _, part := range []string{title, body, paths} {
		_, _ = hasher.Write([]byte(part))
		_, _ = hasher.Write([]byte{0})
	}
	return int64(hasher.Sum64()) //nolint:gosec // a fingerprint; wrapping is fine
}

func Normalize(value string) string {
	if strings.IndexByte(value, 0) >= 0 {
		value = strings.ReplaceAll(value, "\x00", " ")
	}
	return strings.ToValidUTF8(value, "\uFFFD")
}
