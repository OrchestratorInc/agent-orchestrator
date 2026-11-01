package controllers

import (
	"encoding/json"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// activityPreviewLimit is the most bytes a preview page keeps of one summary or
// one body field.
const activityPreviewLimit = 300

// activityBodyKeys are the detail fields of a settled activity that only an
// expanded row shows in full. Everything else in detail is small and read by
// collapsed rows (exitCode, cwd, toolName, server, error, files[] stats), so it
// stays. A field over the limit is dropped, except the headKeys strings, which
// keep their head.
var activityBodyKeys = map[string][]string{
	string(domain.ActivityKindCommand):    {"output", "command", "rawCommand", "input", "rawInput", "content", "terminalInput"},
	string(domain.ActivityKindMCPTool):    {"arguments", "result", "content", "output", "input", "rawInput"},
	string(domain.ActivityKindFileChange): {"content", "input", "output", "rawInput"},
	string(domain.ActivityKindAutoReview): {"command", "rawCommand", "rationale"},
}

// headKeys are read by collapsed rows: a command's category, the `spawned
// session` line `ao spawn` prints, a minimap hover.
var headKeys = map[string]bool{"output": true, "command": true, "rationale": true}

// fileBodyKeys are the per-file fields of a file_change that only the opened diff
// reads. Path, status and line counts stay.
var fileBodyKeys = []string{"patch", "oldText", "newText"}

// previewActivity slims one activity for a page read with activityDetail=preview.
//
// Pure copies of detail.command go on every row: a rawCommand identical to it is
// dropped, and a summary identical to it is sent empty (the client reads an empty
// summary as the command). Running rows keep everything else, so live output
// still streams. Settled rows of the heavy kinds lose their large bodies and say
// so with DetailOmitted; the client fetches the full row when someone expands it.
func previewActivity(a *ConversationActivityResponse) {
	keys, heavy := activityBodyKeys[a.ActivityKind]
	settled := a.Status != string(domain.ActivityStatusRunning) && a.Status != string(domain.ActivityStatusPending)
	fullBytes := 0
	if heavy && settled && a.Detail != nil {
		encoded, _ := json.Marshal(a.Detail)
		fullBytes = len(encoded)
	}
	command, hasCommand := a.Detail["command"].(string)
	if raw, ok := a.Detail["rawCommand"].(string); ok && hasCommand && raw == command {
		delete(a.Detail, "rawCommand")
	}
	summaryIsCommand := hasCommand && a.Summary == command
	if heavy && settled {
		omitBody(a, keys, fullBytes)
	}
	if summaryIsCommand {
		a.Summary = ""
	}
}

// omitBody cuts a settled row's summary and body fields to the preview limit.
func omitBody(a *ConversationActivityResponse, keys []string, fullBytes int) {
	omitted := false
	if summary, cut := previewString(a.Summary); cut {
		a.Summary, omitted = summary, true
	}
	for _, key := range keys {
		if previewField(a.Detail, key) {
			omitted = true
		}
	}
	if files, ok := a.Detail["files"].([]any); ok {
		for _, file := range files {
			fields, ok := file.(map[string]any)
			if !ok {
				continue
			}
			for _, key := range fileBodyKeys {
				if previewField(fields, key) {
					omitted = true
				}
			}
		}
	}
	if omitted {
		a.DetailOmitted = true
		a.DetailBytes = fullBytes
	}
}

// previewField cuts or drops one field over the limit and reports whether it did.
func previewField(detail map[string]any, key string) bool {
	value, ok := detail[key]
	if !ok {
		return false
	}
	if text, isString := value.(string); isString && headKeys[key] {
		cut, changed := previewString(text)
		if changed {
			detail[key] = cut
		}
		return changed
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) <= activityPreviewLimit {
		return false
	}
	delete(detail, key)
	return true
}

// previewString keeps the head of text up to the limit, on a rune boundary.
func previewString(text string) (string, bool) {
	if len(text) <= activityPreviewLimit {
		return text, false
	}
	end := activityPreviewLimit
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end], true
}
