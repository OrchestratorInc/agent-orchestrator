package triage

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func readRetainedLog(ctx context.Context, dataDir, relative, id string, budget int, filterSession string) Evidence {
	item := Evidence{ID: id, Kind: "retained-log", Source: "known AO-owned log", CollectedAt: time.Now().UTC(), Status: "unavailable"}
	data, info, truncated, partial, err := readRetainedTail(ctx, dataDir, relative, budget)
	if err != nil {
		item.Text = "No readable retained log was found at its known AO-owned location."
		return item
	}
	lines := strings.Split(string(bytes.ToValidUTF8(data, []byte("?"))), "\n")
	if filterSession != "" {
		matching := lines[:0]
		for _, line := range lines {
			parts := strings.SplitN(line, " ", 3)
			if len(parts) == 3 && parts[1] == "session="+filterSession {
				matching = append(matching, line)
			}
		}
		lines = matching[max(0, len(matching)-12):]
	}
	for i, line := range lines {
		var value any
		if decodeJSON([]byte(line), &value, false) == nil {
			if encoded, err := json.Marshal(redactValue(value)); err == nil {
				lines[i] = string(encoded)
			}
		}
	}
	item.Text = domain.SanitizeControlChars(Redact(strings.Join(lines, "\n")))
	item.Status = "collected"
	if truncated || partial {
		item.Status = "truncated"
	}
	item.Values = map[string]any{"sourceModifiedAt": info.ModTime().UTC(), "readBytes": len(data), "tailTruncated": truncated, "partialLineOmitted": partial}
	if strings.TrimSpace(item.Text) == "" {
		item.Text = "The retained log contains no complete lines in the bounded tail."
	}
	return item
}
