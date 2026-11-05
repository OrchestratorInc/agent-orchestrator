package collaboration

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Redaction limits. They are small on purpose: the summary exists so an owner can recognise a
// request and decide about it, not so it can be used to reconstruct the request body.
const (
	// maxRedactedTitleHints caps how many candidate titles a summary lists.
	maxRedactedTitleHints = 5
	// maxRedactedTitleRunes caps each hint. A longer title is truncated, never rejected: the
	// summary must not fail because a requester wrote a long sentence.
	maxRedactedTitleRunes = 120
	// maxRedactedRepos caps the repo keys a summary lists.
	maxRedactedRepos = 32
	// maxRedactedNotesRef caps the opaque notes reference.
	maxRedactedNotesRef = 512
)

// RedactTaskRequest projects a stored task request into a redacted, typed summary.
//
// # Why this is a whitelist and not a redaction pass
//
// The stored JSON is arbitrary: it can name repository scopes, absolute paths, prompts, tokens,
// or environment values. A denylist ("strip keys called path") would be defeated by a requester
// who names a path `notes`, and a regex over values cannot tell a repo key from a filesystem
// path in a free-form string. So nothing from the payload is copied verbatim:
//
//   - TaskCount is a number that the request itself declares;
//   - TitleHints are truncated, newline-collapsed strings — they can never carry a path
//     separator into a file operation, and nothing in this daemon resolves one;
//   - Repos are repo KEYS (workflow-local identifiers), never the paths or project ids beside
//     them;
//   - BudgetDelta / ConcurrencyDelta are integers;
//   - NotesRef is an opaque reference, validated against the same closed character class the
//     message payloadRef uses, so it cannot name a path.
//
// Anything else in the payload is dropped. A caller that wants more must be a decider reading
// the durable row through a surface that checks its authority; the ordinary reader gets this.
func RedactTaskRequest(
	request domain.WorkflowTaskRequest,
) (domain.WorkflowTaskRequestSummary, error) {
	summary := domain.WorkflowTaskRequestSummary{}
	if strings.TrimSpace(request.RequestJSON) == "" {
		return summary, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(request.RequestJSON), &decoded); err != nil {
		// A stored payload that is not a JSON object cannot be summarised. Returning an error
		// rather than an empty summary makes the caller report the stored row as unusable
		// instead of presenting "nothing requested" as the answer.
		return domain.WorkflowTaskRequestSummary{}, fmt.Errorf(
			"redact task request %s: payload is not a JSON object: %w", request.ID, err)
	}

	summary.TaskCount = redactedCount(decoded, "taskCount", "tasks")
	summary.TitleHints = redactedTitles(decoded)
	summary.Repos = redactedRepoKeys(decoded)
	summary.BudgetDelta = redactedInt64(decoded, "budgetDelta", "budget")
	summary.ConcurrencyDelta = redactedInt64(decoded, "concurrencyDelta", "concurrency")
	summary.NotesRef = redactedNotesRef(decoded)
	return summary, nil
}

// redactedCount reads the first key that holds a non-negative integer count.
func redactedCount(payload map[string]any, keys ...string) int {
	for _, key := range keys {
		if value, ok := intFrom(payload[key]); ok && value > 0 && value < 1<<16 {
			return int(value)
		}
	}
	return 0
}

// redactedTitles collects at most maxRedactedTitleHints candidate titles.
//
// It reads the documented singular field and an array of task objects, keeping only their title
// strings. Each is collapsed to one line and truncated by runes (not bytes) so a multi-byte
// character is never split into invalid UTF-8.
func redactedTitles(payload map[string]any) []string {
	hints := make([]string, 0, maxRedactedTitleHints)
	if title, ok := payload["title"].(string); ok {
		if hint := truncateTitle(title); hint != "" {
			hints = append(hints, hint)
		}
	}
	for _, key := range []string{"tasks", "taskTitles"} {
		items, ok := payload[key].([]any)
		if !ok {
			continue
		}
		for _, item := range items {
			if len(hints) >= maxRedactedTitleHints {
				return hints
			}
			switch typed := item.(type) {
			case string:
				if hint := truncateTitle(typed); hint != "" {
					hints = append(hints, hint)
				}
			case map[string]any:
				title, _ := typed["title"].(string)
				if hint := truncateTitle(title); hint != "" {
					hints = append(hints, hint)
				}
			}
		}
	}
	if hints == nil {
		return []string{}
	}
	return hints
}

// truncateTitle collapses whitespace and truncates to the rune cap.
func truncateTitle(raw string) string {
	collapsed := strings.Join(strings.Fields(raw), " ")
	if collapsed == "" {
		return ""
	}
	if utf8.RuneCountInString(collapsed) <= maxRedactedTitleRunes {
		return collapsed
	}
	runes := []rune(collapsed)
	return string(runes[:maxRedactedTitleRunes])
}

// redactedRepoKeys collects repo keys only.
//
// A repo key is a workflow-local identifier. The same payload may carry a path or a project id
// next to it; neither is ever copied. Only the key's own closed character class is accepted, so a
// value that looks like a filesystem path is refused rather than truncated into something that
// still names a directory.
func redactedRepoKeys(payload map[string]any) []string {
	keys := make([]string, 0, maxRedactedRepos)
	seen := make(map[string]struct{}, maxRedactedRepos)
	collect := func(value any) {
		key, ok := value.(string)
		if !ok {
			return
		}
		key = strings.TrimSpace(key)
		if !domain.ValidPayloadRef(key) {
			return
		}
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for _, field := range []string{"repos", "repoKeys"} {
		switch typed := payload[field].(type) {
		case []any:
			for _, item := range typed {
				if len(keys) >= maxRedactedRepos {
					sort.Strings(keys)
					return keys
				}
				collect(item)
			}
		case string:
			collect(typed)
		}
	}
	if repoKey, ok := payload["repoKey"].(string); ok && len(keys) < maxRedactedRepos {
		collect(repoKey)
	}
	sort.Strings(keys)
	if keys == nil {
		return []string{}
	}
	return keys
}

// redactedInt64 reads the first key that holds an integer delta.
func redactedInt64(payload map[string]any, keys ...string) *int64 {
	for _, key := range keys {
		if value, ok := intFrom(payload[key]); ok {
			out := value
			return &out
		}
	}
	return nil
}

// redactedNotesRef reads an opaque notes reference and refuses anything path-shaped.
func redactedNotesRef(payload map[string]any) string {
	raw, _ := payload["notesRef"].(string)
	raw = strings.TrimSpace(raw)
	if len(raw) > maxRedactedNotesRef || !domain.ValidPayloadRef(raw) {
		return ""
	}
	return raw
}

// intFrom accepts the JSON number forms encoding/json produces.
// encoding/json decodes every number into float64 unless UseNumber is set, so both are handled.
func intFrom(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		if typed != float64(int64(typed)) {
			return 0, false
		}
		return int64(typed), true
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	default:
		return 0, false
	}
}
