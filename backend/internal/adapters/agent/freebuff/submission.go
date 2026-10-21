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
	"unicode/utf16"

	"github.com/charmbracelet/x/ansi"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentAfterStartSubmitConfirmer = (*Plugin)(nil)
var _ ports.AgentTerminalMessageEncoder = (*Plugin)(nil)

// EncodeTerminalMessage uses Freebuff's native paste handler rather than its
// per-key multiline editor, whose newline handler can overwrite batched text.
func (*Plugin) EncodeTerminalMessage(message string) (string, error) {
	if message == "" {
		return "", nil
	}
	if strings.ContainsRune(message, '\x1b') {
		return "", errors.New("freebuff: terminal message cannot contain terminal escape sequences")
	}
	return "\x1b[200~" + message + "\x1b[201~", nil
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
	if pendingNativeTextAttachment(output, prompt) {
		return ports.AfterStartDraftPending, nil
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

// A long native paste leaves an empty editor and a removable text card below
// it. Its fresh paste toast, complete card, UTF16 size and entire visible
// preview acknowledge a pending paste, never submission. Require all of that
// chrome together before allowing the manager's bounded Enter-only retry.
func pendingNativeTextAttachment(output, prompt string) bool {
	units := utf16.Encode([]rune(prompt))
	if len(units) <= 2000 || (units[39] >= 0xd800 && units[39] <= 0xdbff) {
		return false
	}
	preview := strings.ReplaceAll(string(utf16.Decode(units[:40])), "\n", " ")
	if strings.TrimSpace(preview) == "" || strings.ContainsFunc(preview, unicode.IsControl) {
		return false
	}
	compact := func(value string) string { return strings.Join(strings.Fields(value), "") }
	expectedPreview := compact(preview + "…")
	count := fmt.Sprint(len(units))
	for i := len(count) - 3; i > 0; i -= 3 {
		count = count[:i] + "," + count[i:]
	}
	toast := "📋 Pasted text (" + count + " chars)"
	raw := strings.Split(strings.ReplaceAll(output, "\r", "\n"), "\n")
	lines := terminalui.PlainTerminalLines(output)
	if len(raw) != len(lines) {
		return false
	}
	last := len(lines) - 1
	for last >= 0 && strings.TrimSpace(lines[last]) == "" {
		last--
	}
	if last < 9 || strings.TrimSpace(lines[last]) != "← for history · ? for help" {
		return false
	}
	for bottom := last - 2; bottom >= 7 && bottom >= last-12; bottom-- {
		width := composerBorderWidth(lines[bottom], '╰', '╯')
		if width == 0 {
			continue
		}
		if !nativeFooter(lines[bottom+1 : last]) {
			return false
		}
		// The 0.2.32 card has three preview rows, one count row and square
		// borders, inside a bottom-only rounded banner. Nothing else may
		// share it: another text/image/file card is an ambiguous pending turn.
		start := bottom - 6
		composerWidth := composerBorderWidth(lines[start-1], '╰', '╯')
		if composerWidth != width && composerWidth != width+2 {
			return false
		}
		inside := make([]string, 6)
		for i := range inside {
			row := []rune(strings.TrimSpace(lines[start+i]))
			if len(row) < 2 || row[0] != '│' || row[len(row)-1] != '│' || ansi.StringWidth(string(row)) != width {
				return false
			}
			inside[i] = strings.TrimSpace(string(row[1 : len(row)-1]))
		}
		if inside[0] != "┌──────────────────────┐X" || inside[5] != "└──────────────────────┘" {
			return false
		}
		var visible strings.Builder
		for i := 1; i <= 4; i++ {
			row := []rune(inside[i])
			if len(row) < 2 || row[0] != '│' || row[len(row)-1] != '│' ||
				strings.Count(inside[i], "│") != 2 || ansi.StringWidth(inside[i]) != 24 {
				return false
			}
			content := strings.TrimSpace(string(row[1 : len(row)-1]))
			if i == 4 {
				if content != "📄 "+count+" chars" {
					return false
				}
			} else {
				visible.WriteString(content)
			}
		}
		if compact(visible.String()) != expectedPreview {
			return false
		}
		// Keep the general activity parser unchanged: remove only this proven
		// attachment banner, then require its current, focused idle composer.
		withoutBanner := append(append([]string(nil), raw[:start]...), raw[bottom+1:]...)
		composer, ok := currentComposer(strings.Join(withoutBanner, "\n"))
		return ok && composer.placeholder == idlePlaceholder && composer.placeholderCursor && composer.status == toast
	}
	return false
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
