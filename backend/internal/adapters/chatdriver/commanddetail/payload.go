package commanddetail

import "encoding/json"

// MaxToolPayloadChars caps one tool call's arguments or result.
//
// A tool can be handed a whole file and can hand one back, and this payload is
// re-read by every conversation snapshot poll. 8K is well past what a card shows
// and far short of what an unbounded result would cost; over the cap the JSON is
// replaced by a marker rather than cut, because half a JSON document is not JSON
// and a client that tried to parse it would fail on valid provider data.
const MaxToolPayloadChars = 8 * 1024

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
