package commanddetail

import (
	"encoding/json"
	"unicode/utf8"
)

const MaxToolPayloadChars = 8 * 1024

const MaxOutputChars = 64 * 1024

const MaxPatchChars = 16 * 1024

func Head(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit]
}

func TailChars(text string, limit int) (string, bool) {
	if len(text) <= limit {
		return text, false
	}
	start := len(text)
	for n := 0; n < limit && start > 0; n++ {
		_, size := utf8.DecodeLastRuneInString(text[:start])
		start -= size
	}
	return text[start:], start > 0
}

func TruncatedJSON(raw json.RawMessage) any {
	if len(raw) <= MaxToolPayloadChars {
		return raw
	}
	return map[string]any{
		"truncated": true,
		"bytes":     len(raw),
		"note":      "payload exceeded the daemon's tool payload cap and was not stored",
	}
}
