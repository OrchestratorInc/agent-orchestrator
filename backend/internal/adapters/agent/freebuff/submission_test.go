package freebuff

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/charmbracelet/x/ansi"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestTerminalMessageEncodingPreservesOneCompleteNativePaste(t *testing.T) {
	p := New()
	for name, prompt := range map[string]string{
		"single line":      "Reply FIRST_OK.",
		"multiline":        "First harmless paragraph.\n\nPreserve the next line.\nReply FIRST_OK.",
		"large attachment": strings.Repeat("Harmless multiline text with 中文 🚀 e\u0301.\n", 90) + "END_6553_ACCEPTANCE",
		"literal markup":   "Preserve literal <user_message> and [Pasted Text] text.\tThen wait.",
		"empty":            "",
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := p.EncodeTerminalMessage(prompt)
			if err != nil {
				t.Fatal(err)
			}
			if prompt == "" {
				if encoded != "" {
					t.Fatal("Enter-only input must remain empty without paste framing")
				}
				return
			}
			if !strings.HasPrefix(encoded, "\x1b[200~") || !strings.HasSuffix(encoded, "\x1b[201~") {
				t.Fatal("terminal message must use one complete native bracketed paste")
			}
			body := strings.TrimSuffix(strings.TrimPrefix(encoded, "\x1b[200~"), "\x1b[201~")
			if body != prompt || strings.Count(encoded, "\x1b") != 2 {
				t.Fatal("paste body changed or contains another terminal control frame")
			}
		})
	}
	if p.PrimesAfterStartSubmit() {
		t.Fatal("native paste must not send an unrelated empty Enter before the first turn")
	}
}

func TestTerminalMessageEncodingRejectsEmbeddedTerminalControlsWithoutContent(t *testing.T) {
	const privateText = "PRIVATE_PAYLOAD_6553"
	for name, control := range map[string]string{
		"paste terminator": "\x1b[201~",
		"paste opener":     "\x1b[200~",
		"csi":              "\x1b[2J",
		"osc":              "\x1b]0;title\a",
		"bare escape":      "\x1b",
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := New().EncodeTerminalMessage(privateText + control + "\nDo not submit.")
			if err == nil || encoded != "" {
				t.Fatal("unsafe terminal message must fail before terminal delivery")
			}
			if strings.Contains(err.Error(), privateText) || strings.ContainsRune(err.Error(), '\x1b') {
				t.Fatal("encoding error leaked prompt contents or terminal controls")
			}
		})
	}
}

func submissionFixture(t *testing.T) (*Plugin, ports.LaunchConfig) {
	t.Helper()
	p := New()
	cfg := ports.LaunchConfig{SessionID: t.Name(), WorkspacePath: t.TempDir(), Env: map[string]string{configDirEnv: t.TempDir()}}
	release, err := p.BeginStartup(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return p, cfg
}

func writeSubmissionState(t *testing.T, cfg ports.LaunchConfig, projectRoot string, sentAt int64, role string, parts ...string) {
	t.Helper()
	blocks := make([]map[string]string, len(parts))
	for i, text := range parts {
		blocks[i] = map[string]string{"type": "text", "text": text}
	}
	state := map[string]any{"sessionState": map[string]any{
		"fileContext":    map[string]string{"projectRoot": projectRoot},
		"mainAgentState": map[string]any{"messageHistory": []map[string]any{{"role": role, "sentAt": sentAt, "content": blocks}}},
	}}
	dir := filepath.Join(chatsDir(configDir(cfg.Env), cfg.WorkspacePath), "2026-10-21T00-00-00.000Z")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run-state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFirstTurnSubmissionRequiresCompleteRecentNativeUserRecord(t *testing.T) {
	const prompt = "Remember the harmless acceptance marker.\nReply FIRST_OK and wait."
	for name, tc := range map[string]struct {
		role string
		text string
		old  bool
		want ports.AfterStartSubmitState
	}{
		"whole user turn":             {"user", prompt, false, ports.AfterStartSubmitted},
		"whole pasted attachment":     {"user", "[Pasted Text]\n" + prompt, false, ports.AfterStartSubmitted},
		"historical identical turn":   {"user", prompt, true, ports.AfterStartSubmitUnknown},
		"historical pasted turn":      {"user", "[Pasted Text]\n" + prompt, true, ports.AfterStartSubmitUnknown},
		"assistant echoed prompt":     {"assistant", prompt, false, ports.AfterStartSubmitUnknown},
		"assistant echoed attachment": {"assistant", "[Pasted Text]\n" + prompt, false, ports.AfterStartSubmitUnknown},
		"only task tail was saved":    {"user", "Reply FIRST_OK and wait.", false, ports.AfterStartSubmitUnknown},
		"only pasted tail was saved":  {"user", "[Pasted Text]\nReply FIRST_OK and wait.", false, ports.AfterStartSubmitUnknown},
		"different task":              {"user", "A different harmless task.", false, ports.AfterStartSubmitUnknown},
		"different pasted task":       {"user", "[Pasted Text]\nA different harmless task.", false, ports.AfterStartSubmitUnknown},
		"turn embedded in attachment": {"user", "[Pasted Text]\nOther instructions.\n" + prompt, false, ports.AfterStartSubmitUnknown},
		"extra pasted attachment":     {"user", "[Pasted Text]\n" + prompt + "\n\n[Pasted Text]\nAnother task.", false, ports.AfterStartSubmitUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			p, cfg := submissionFixture(t)
			sentAt := time.Now().Add(time.Second).UnixMilli()
			if tc.old {
				sentAt = time.Now().Add(-time.Minute).UnixMilli()
			}
			writeSubmissionState(t, cfg, cfg.WorkspacePath, sentAt, tc.role, tc.text)
			got, err := p.AfterStartSubmitState(context.Background(), cfg, startupComposer("GLM 5.3 Flash", "high"), prompt)
			if err != nil || got != tc.want {
				t.Fatalf("state=%q err=%v want=%q", got, err, tc.want)
			}
		})
	}
}

func TestFirstTurnSubmissionConcatenatesNativeTextBlocksExactly(t *testing.T) {
	p, cfg := submissionFixture(t)
	writeSubmissionState(t, cfg, cfg.WorkspacePath, time.Now().Add(time.Second).UnixMilli(), "user", "First paragraph.\r\n", "Second paragraph.")
	got, err := p.AfterStartSubmitState(context.Background(), cfg, "", "First paragraph.\nSecond paragraph.")
	if err != nil || got != ports.AfterStartSubmitted {
		t.Fatalf("state=%q err=%v", got, err)
	}
}

func TestFirstTurnSubmissionAcceptsCompleteNativeAttachmentWithoutEncodingMarkup(t *testing.T) {
	p, cfg := submissionFixture(t)
	prompt := strings.Repeat("Harmless text with <literal> markup, 中文, and 🚀.\r\n", 80) + "END_6553_ACCEPTANCE"
	writeSubmissionState(t, cfg, cfg.WorkspacePath, time.Now().Add(time.Second).UnixMilli(), "user", "[Pasted Text]\n", prompt)
	got, err := p.AfterStartSubmitState(context.Background(), cfg, "", prompt)
	if err != nil || got != ports.AfterStartSubmitted {
		t.Fatalf("state=%q err=%v", got, err)
	}
}

func TestFirstTurnSubmissionDoesNotUseOtherWorkspaceHistory(t *testing.T) {
	const prompt = "Reply ONLY_ACCEPTED after this exact complete task."
	for name, saved := range map[string]string{"plain": prompt, "pasted": "[Pasted Text]\n" + prompt} {
		t.Run(name, func(t *testing.T) {
			p, cfg := submissionFixture(t)
			writeSubmissionState(t, cfg, t.TempDir(), time.Now().Add(time.Second).UnixMilli(), "user", saved)
			got, err := p.AfterStartSubmitState(context.Background(), cfg, startupComposer("GLM 5.3 Flash", "high"), prompt)
			if err != nil || got != ports.AfterStartSubmitUnknown {
				t.Fatalf("state=%q err=%v", got, err)
			}
		})
	}
}

func TestFirstTurnDraftRequiresCurrentCompleteComposer(t *testing.T) {
	const prompt = "First harmless paragraph.\nThe entire terminal-visible acceptance marker is SAPPHIRE_6553."
	tail := "The entire terminal-visible acceptance marker is SAPPHIRE_6553."
	for name, tc := range map[string]struct {
		output string
		want   ports.AfterStartSubmitState
	}{
		"whole pending turn":                   {freebuffTerminalScreen("unlimited", "First harmless paragraph.", tail), ports.AfterStartDraftPending},
		"native cursor at end of pending turn": {freebuffTerminalScreen("unlimited", "First harmless paragraph.", tail+"\x1b[1m▍\x1b[0m"), ports.AfterStartDraftPending},
		"clipped input tail":                   {freebuffTerminalScreen("unlimited", tail), ports.AfterStartDraftPending},
		"transcript mentions tail":             {tail + "\n" + freebuffTerminalScreen("unlimited", "unrelated human draft"), ports.AfterStartSubmitUnknown},
		"tail embedded in another draft":       {freebuffTerminalScreen("unlimited", "Another draft says "+tail[:28], tail[28:]), ports.AfterStartSubmitUnknown},
		"tiny suffix":                          {freebuffTerminalScreen("unlimited", "SAPPHIRE_6553."), ports.AfterStartSubmitUnknown},
		"empty input without saved turn":       {startupComposer("GLM 5.3 Flash", "high"), ports.AfterStartSubmitUnknown},
		"active input without saved turn":      {freebuffTerminalScreen("working... 2s ■ Esc", "\x1b[1m▍\x1b[0m"+activePlaceholder), ports.AfterStartSubmitUnknown},
		"native admission dialog":              {"Starting your model session… Your message is saved.\n" + tail + "\n" + startupPickerFooter, ports.AfterStartSubmitUnknown},
		"historical native error":              {"Model request failed. Enter retry · Esc back.\n" + startupPickerFooter, ports.AfterStartSubmitUnknown},
		"partial composer":                     {"│ " + tail + " │\n╰────────────────╯\n" + startupPickerFooter, ports.AfterStartSubmitUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			p, cfg := submissionFixture(t)
			got, err := p.AfterStartSubmitState(context.Background(), cfg, tc.output, prompt)
			if err != nil || got != tc.want {
				t.Fatalf("state=%q err=%v want=%q", got, err, tc.want)
			}
		})
	}
}

func TestComposerContentPreservesRowsAndLiteralCursorGlyphs(t *testing.T) {
	output := freebuffTerminalScreen("unlimited", "First literal ▍ character.", "", "Second line.\x1b[1m▍\x1b[0m")
	composer, ok := currentComposer(output)
	if !ok || composer.content != "First literal ▍ character.\n\nSecond line." {
		t.Fatalf("composer=%+v known=%v", composer, ok)
	}
}

func TestFirstTurnAcceptanceRequiresActiveStartupIdentity(t *testing.T) {
	p, cfg := submissionFixture(t)
	const prompt = "The exact complete native user task."
	writeSubmissionState(t, cfg, cfg.WorkspacePath, time.Now().Add(time.Second).UnixMilli(), "user", prompt)
	cfg.SessionID = "another-session"
	got, err := p.AfterStartSubmitState(context.Background(), cfg, startupComposer("GLM 5.3 Flash", "high"), prompt)
	if err == nil || got != ports.AfterStartSubmitUnknown || !strings.Contains(err.Error(), "active startup guard") {
		t.Fatalf("state=%q err=%v", got, err)
	}
}

// This is a sanitized Freebuff 0.2.32 capture after a harmless 3,131-character
// bracketed paste and before Enter. The native editor remains empty because
// the paste became one text attachment below the composer.
func TestFirstTurnPendingNativeTextAttachment(t *testing.T) {
	data, err := os.ReadFile("testdata/pending-text-attachment.ansi.txt")
	if err != nil {
		t.Fatal(err)
	}
	output := string(data)
	withInput := func(value string, focused bool) string {
		lines := terminalui.PlainTerminalLines(output)
		for i, line := range lines {
			if !strings.Contains(line, idlePlaceholder) {
				continue
			}
			if focused {
				value = "\x1b[1m▍\x1b[0m" + value
			}
			width := ansi.StringWidth(line)
			lines[i] = "│  " + value + strings.Repeat(" ", width-4-ansi.StringWidth(value)) + "│"
			break
		}
		return strings.Join(lines, "\n")
	}
	const prefix = "Runtime paste transport validation only."
	prompt := prefix + strings.Repeat("x", 3131-len(prefix))
	for name, tc := range map[string]struct {
		output string
		prompt string
		want   ports.AfterStartSubmitState
	}{
		"one complete pending attachment":            {output, prompt, ports.AfterStartDraftPending},
		"matching UTF16 count":                       {output, prefix + strings.Repeat("x", 3131-len(prefix)-2) + "🚀", ports.AfterStartDraftPending},
		"wrong UTF16 count":                          {output, prefix + strings.Repeat("x", 3131-len(prefix)-1) + "🚀", ports.AfterStartSubmitUnknown},
		"wrong count":                                {strings.ReplaceAll(output, "3,131", "3,132"), prompt, ports.AfterStartSubmitUnknown},
		"wrong preview":                              {strings.Replace(output, "Runtime paste", "Another paste", 1), prompt, ports.AfterStartSubmitUnknown},
		"missing toast":                              {strings.Replace(output, "📋 Pasted text (3,131 chars)", "", 1), prompt, ports.AfterStartSubmitUnknown},
		"different toast count":                      {strings.Replace(output, "📋 Pasted text (3,131 chars)", "📋 Pasted text (3,130 chars)", 1), prompt, ports.AfterStartSubmitUnknown},
		"editable input":                             {withInput("A different human draft", true), prompt, ports.AfterStartSubmitUnknown},
		"unfocused input":                            {strings.Replace(output, ";1m▍", "m▍", 1), prompt, ports.AfterStartSubmitUnknown},
		"active placeholder":                         {withInput(activePlaceholder, true), prompt, ports.AfterStartSubmitUnknown},
		"current admission modal":                    {withInput("Starting your model session… Your message is saved.", false), prompt, ports.AfterStartSubmitUnknown},
		"current spending confirmation":              {withInput("Pro model · Use credits for this model. · Use model", false), prompt, ports.AfterStartSubmitUnknown},
		"current error modal":                        {withInput("Model request failed. Enter retry · Esc back.", false), prompt, ports.AfterStartSubmitUnknown},
		"current active status":                      {strings.Replace(output, "📋 Pasted text (3,131 chars)", "working... 2s ■ Esc", 1), prompt, ports.AfterStartSubmitUnknown},
		"historical admission notice":                {"Starting your model session… Your message is saved.\n" + output, prompt, ports.AfterStartDraftPending},
		"historical quota denial":                    {"Not enough hours for this model.\n" + output, prompt, ports.AfterStartDraftPending},
		"historical active state":                    {"working... 2s ■ Esc\n" + output, prompt, ports.AfterStartDraftPending},
		"historical native error":                    {"Model request failed. Enter retry · Esc back.\n" + output, prompt, ports.AfterStartDraftPending},
		"card no longer removable":                   {strings.Replace(output, "┐\x1b[38;2;172;179;191mX", "┐\x1b[38;2;172;179;191m ", 1), prompt, ports.AfterStartSubmitUnknown},
		"image card":                                 {strings.Replace(output, "📄", "🖼", 1), prompt, ports.AfterStartSubmitUnknown},
		"incomplete card":                            {strings.Replace(output, "└──────────────────────┘", "                       ", 1), prompt, ports.AfterStartSubmitUnknown},
		"truncated preview":                          {strings.Replace(output, "validation only.…", "validation only. ", 1), prompt, ports.AfterStartSubmitUnknown},
		"missing native help":                        {strings.Replace(output, " for history · ", " for something else · ", 1), prompt, ports.AfterStartSubmitUnknown},
		"short paste does not become attachment":     {output, prefix, ports.AfterStartSubmitUnknown},
		"old transcript card above current composer": {output + startupComposer("MiMo v2.6 Flash Fast", ""), prompt, ports.AfterStartSubmitUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			p, cfg := submissionFixture(t)
			got, err := p.AfterStartSubmitState(context.Background(), cfg, tc.output, tc.prompt)
			if err != nil || got != tc.want {
				t.Fatalf("state=%q err=%v want=%q", got, err, tc.want)
			}
		})
	}
	// Make the entire banner appear twice: a second attachment must not be
	// mistaken for one matching paste, including when both have the same size.
	lines := strings.Split(output, "\n")
	extra := append([]string(nil), lines[:14]...)
	extra = append(extra, lines[7:]...)
	p, cfg := submissionFixture(t)
	got, err := p.AfterStartSubmitState(context.Background(), cfg, strings.Join(extra, "\n"), prompt)
	if err != nil || got != ports.AfterStartSubmitUnknown {
		t.Fatalf("multiple attachment banners: state=%q err=%v", got, err)
	}
	if len(utf16.Encode([]rune(prefix))) != 40 {
		t.Fatal("native fixture must identify all 40 visible UTF16 preview characters")
	}
}
