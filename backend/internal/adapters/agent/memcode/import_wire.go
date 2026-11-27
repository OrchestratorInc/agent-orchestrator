package memcode

import (
	"encoding/json"
	"errors"
)

// Keep the decoding shape aligned with Memcode d7eb359 internal/wire/wire.go.
// RawMessage deliberately accepts arbitrary valid JSON for tool input, while
// scalar/media/cache/nested content fields retain the native decoder's types.
// Unknown fields remain compatible, just as with native json.Unmarshal.
type memcodeNativeBlock struct {
	Type   string `json:"type"`
	Text   string `json:"text"`
	Source *struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	} `json:"source"`
	ID            string               `json:"id"`
	Name          string               `json:"name"`
	Input         json.RawMessage      `json:"input"`
	ToolUseID     string               `json:"tool_use_id"`
	Content       string               `json:"content"`
	IsError       bool                 `json:"is_error"`
	ContentBlocks []memcodeNativeBlock `json:"content_blocks"`
	Thinking      string               `json:"thinking"`
	Signature     string               `json:"signature"`
	Data          string               `json:"data"`
	CacheControl  *struct {
		Type string `json:"type"`
		TTL  string `json:"ttl"`
	} `json:"cache_control"`
}

// ValidateNativeContent refuses blocks that Memcode's native wire decoder
// cannot load. It validates only; callers retain the original native bytes.
func ValidateNativeContent(blocks []json.RawMessage) error {
	for _, raw := range blocks {
		var block memcodeNativeBlock
		if err := json.Unmarshal(raw, &block); err != nil {
			return errors.New("memcode native message has an invalid wire block")
		}
	}
	return nil
}
