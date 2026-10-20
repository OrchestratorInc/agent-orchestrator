package freebuff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentAfterStartSubmitConfirmer = (*Plugin)(nil)
var _ ports.AgentAfterStartPromptEncoder = (*Plugin)(nil)

// EncodeAfterStartPrompt uses Freebuff's native paste handler rather than its
// per-key multiline editor, whose newline handler can overwrite batched text.
func (*Plugin) EncodeAfterStartPrompt(prompt string) (string, error) {
	if strings.ContainsRune(prompt, '\x1b') {
		return "", errors.New("freebuff: first prompt cannot contain terminal escape sequences")
	}
	return "\x1b[200~" + prompt + "\x1b[201~", nil
}

// PrimesAfterStartSubmit is false because bracketed paste does not depend on
// Freebuff's Return-versus-linefeed keyboard detection.
func (*Plugin) PrimesAfterStartSubmit() bool { return false }

// AfterStartSubmitState reports success only when Freebuff saved the complete
// first turn after this startup began. Empty/active terminal chrome alone can
// be stale or precede native admission and cannot acknowledge user input.
func (p *Plugin) AfterStartSubmitState(ctx context.Context, cfg ports.LaunchConfig, output, prompt string) (ports.AfterStartSubmitState, error) {
	if err := ctx.Err(); err != nil {
		return ports.AfterStartSubmitUnknown, err
	}
	owner, ok := p.startupOwnerFor(cfg)
	if !ok {
		return ports.AfterStartSubmitUnknown, errors.New("freebuff: first-turn confirmation requires its active startup guard")
	}
	accepted, err := nativePromptAccepted(ctx, cfg, owner.startedAt, prompt)
	if err != nil {
		return ports.AfterStartSubmitUnknown, err
	}
	if accepted {
		return ports.AfterStartSubmitted, nil
	}
	composer, ok := currentComposer(output)
	if !ok || (composer.placeholder != "" && composer.placeholderCursor) {
		return ports.AfterStartSubmitUnknown, nil
	}
	if pendingNativeDraft(composer.content, prompt) {
		return ports.AfterStartDraftPending, nil
	}
	return ports.AfterStartSubmitUnknown, nil
}

func nativePromptAccepted(ctx context.Context, cfg ports.LaunchConfig, startedAt time.Time, prompt string) (bool, error) {
	expected := normalizedSavedPrompt(prompt)
	// Freebuff turns a single paste longer than 2,000 characters into a text
	// attachment and prefixes that complete payload before handing it to the SDK.
	wrappedExpected := normalizedSavedPrompt("[Pasted Text]\n" + prompt)
	if expected == "" || startedAt.IsZero() {
		return false, nil
	}
	dir := configDir(cfg.Env)
	chatID, err := latestChatID(dir, cfg.WorkspacePath)
	if err != nil || chatID == "" {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	data, err := os.ReadFile(filepath.Join(chatsDir(dir, cfg.WorkspacePath), chatID, "run-state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("freebuff: read first-turn acceptance: %w", err)
	}
	var state struct {
		SessionState struct {
			FileContext struct {
				ProjectRoot string `json:"projectRoot"`
			} `json:"fileContext"`
			MainAgentState struct {
				MessageHistory []struct {
					Role    string `json:"role"`
					SentAt  int64  `json:"sentAt"`
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"messageHistory"`
			} `json:"mainAgentState"`
		} `json:"sessionState"`
	}
	// Native state can be between saves. Unreadable or unknown schemas never
	// establish acceptance; the caller's bounded confirmation loop retries.
	if json.Unmarshal(data, &state) != nil || !samePath(state.SessionState.FileContext.ProjectRoot, cfg.WorkspacePath) {
		//nolint:nilerr // A partial native save is not an acknowledgement; retry the read.
		return false, nil
	}
	for _, message := range state.SessionState.MainAgentState.MessageHistory {
		if message.Role != "user" || message.SentAt < startedAt.UnixMilli() {
			continue
		}
		var text strings.Builder
		for _, block := range message.Content {
			if block.Type == "text" {
				text.WriteString(block.Text)
			}
		}
		actual := normalizedSavedPrompt(text.String())
		if actual == expected || actual == wrappedExpected {
			return true, nil
		}
	}
	return false, nil
}

func normalizedSavedPrompt(prompt string) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(prompt, "\r\n", "\n"), "\r", "\n"))
}

func pendingNativeDraft(content, prompt string) bool {
	compact := func(text string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, text)
	}
	visible, expected := compact(content), compact(prompt)
	if visible == "" || expected == "" {
		return false
	}
	if visible == expected {
		return true
	}
	// A vertically scrolled input retains complete native composer borders
	// but only the tail of a large turn. Every visible character must match
	// that turn's suffix, with at least 24 non-whitespace characters. A tail
	// mentioned in transcript or embedded in another draft never qualifies.
	return len([]rune(visible)) >= 24 && len(visible) < len(expected) && strings.HasSuffix(expected, visible)
}
