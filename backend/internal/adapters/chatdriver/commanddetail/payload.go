package commanddetail

import (
	"encoding/json"
	"unicode/utf8"
)

// MaxToolPayloadChars caps one tool call's arguments or result.
//
// A tool can be handed a whole file and can hand one back, and this payload is
// re-read by every conversation snapshot poll. 8K is well past what a card shows
// and far short of what an unbounded result would cost; over the cap the JSON is
// replaced by a marker rather than cut, because half a JSON document is not JSON
// and a client that tried to parse it would fail on valid provider data.
const MaxToolPayloadChars = 8 * 1024

// MaxOutputChars caps the command output kept in an activity's detail.
//
// It is the store's cap on streamed command output, counted the same way, in
// characters: a command can arrive both ways, and the two copies must agree on
// whether it was cut. A provider that re-sends the whole accumulated output on
// every update would otherwise write all of it again each time.
const MaxOutputChars = 64 * 1024

// MaxPatchChars caps one file's stored patch.
//
// A new file's "patch" is its entire contents, and this payload is re-read by every
// conversation snapshot poll, so an agent generating a 5000-line file would otherwise
// put it in a row that is fetched once a second. 16K is well past what a diff card
// shows; past it the worktree and the turn's diff are where the full change lives.
const MaxPatchChars = 16 * 1024

// Head keeps at most limit bytes from the start of text, cut on a character
// boundary so the kept part is still valid UTF-8.
func Head(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit]
}

// TailChars keeps the last limit characters of text and reports whether it cut
// any. The end is the part worth keeping of a command's output: it is where a build
// or a test run says how it went.
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

// TruncatedJSON keeps a payload only while it is small enough to be worth keeping.
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
