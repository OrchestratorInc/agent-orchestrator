package codewhalestore

import "strings"

// Mirror turn_classify::turn_metadata_text: only a separate engine-owned
// metadata block is hidden. A single user-authored metadata example stays text.
func visibleText(message nativeMessage) string {
	metadata := -1
	completeText := func(text string) bool {
		text = strings.TrimSpace(text)
		body, ok := strings.CutPrefix(text, "<turn_meta>")
		if !ok {
			return false
		}
		_, tail, ok := strings.Cut(body, "</turn_meta>")
		return ok && tail == ""
	}
	uncached := func(index int) bool {
		block := message.Content[index]
		return block.Type == "text" && (len(block.Cache) == 0 || string(block.Cache) == "null")
	}
	complete := func(index int) bool {
		return uncached(index) && completeText(message.Content[index].Text)
	}
	if message.Role == "user" {
		if len(message.Content) == 2 && uncached(0) && uncached(1) && message.Content[1].Text == "<!-- codewhale.compaction-checkpoint.v1 -->" {
			text := message.Content[0].Text
			if strings.HasPrefix(text, "Codewhale handoff note") || strings.HasPrefix(text, "Another language model started to solve this problem") {
				return ""
			}
		}
		for _, block := range message.Content {
			switch block.Type {
			case "tool_result", "tool_search_tool_result", "code_execution_tool_result":
				return ""
			}
		}
		if n := len(message.Content); n >= 2 {
			if complete(n - 1) {
				metadata = n - 1
			} else if complete(0) && message.Content[n-1].Type == "text" && !completeText(message.Content[n-1].Text) {
				metadata = 0
			}
		}
		if metadata >= 0 {
			var provenance, legacy bool
			for _, line := range strings.Split(message.Content[metadata].Text, "\n") {
				line = strings.TrimSpace(line)
				if value, ok := strings.CutPrefix(line, "Input provenance: "); ok {
					if strings.HasSuffix(value, " (non-authoritative)") {
						return ""
					}
					provenance = true
				}
				legacy = legacy || line == "Input authority: non_authoritative"
			}
			if provenance && legacy {
				return ""
			}
		}
	}
	parts := make([]string, 0, len(message.Content))
	for i, block := range message.Content {
		if i != metadata && block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}
