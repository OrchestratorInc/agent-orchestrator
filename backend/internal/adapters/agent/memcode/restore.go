package memcode

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var nativeIDPattern = regexp.MustCompile(`^sess_([0-9a-f]{8}|[0-9a-f]{16})$`)

type restorePlan struct {
	argv                                []string
	target, workspace, dataDir, session string
	digest                              [32]byte
	count                               int
	freshSequence                       int
}

func (p *Plugin) PrepareNativeRestore(ctx context.Context, cfg ports.RestoreConfig) (ports.NativeRestoreInitialization, error) {
	target := cfg.Session.Metadata[ports.MetadataKeyAgentSessionID]
	digest, count, err := transcript(cfg.Session.WorkspacePath, target)
	if err != nil {
		return nil, err
	}
	argv, err := p.GetLaunchCommand(ctx, ports.LaunchConfig{WorkspacePath: cfg.Session.WorkspacePath, Config: cfg.Config, Permissions: cfg.Permissions, AllowedTools: cfg.AllowedTools, DisallowedTools: cfg.DisallowedTools})
	if err != nil {
		return nil, err
	}
	return &restorePlan{argv: argv, target: target, workspace: cfg.Session.WorkspacePath, dataDir: cfg.DataDir, session: cfg.Session.ID, digest: digest, count: count}, nil
}
func (p *restorePlan) Argv() []string { return append([]string(nil), p.argv...) }
func (*restorePlan) LaunchEnv() map[string]string {
	return map[string]string{"AO_MEMCODE_INTERACTIVE_RESTORE": "1"}
}
func (p *restorePlan) ResumeInput() string { return "/resume " + p.target }
func (p *restorePlan) TargetID() string    { return p.target }
func (p *restorePlan) verifyHistory() error {
	digest, count, err := transcript(p.workspace, p.target)
	if err != nil {
		return err
	}
	if digest != p.digest || count != p.count {
		return errors.New("memcode target history changed during restore")
	}
	return nil
}
func (p *restorePlan) Ready(ctx context.Context, generation, output string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := p.verifyHistory(); err != nil {
		return false, err
	}
	w, err := readWitness(p.dataDir, p.session, generation)
	if err != nil {
		return false, err
	}
	if w.Generation != generation || w.Sequence == 0 {
		return false, nil
	}
	if !nativeIDPattern.MatchString(w.NativeID) || w.NativeID == p.target {
		return false, errors.New("memcode fresh initialization identity invalid")
	}
	if !emptyComposer(output) {
		return false, nil
	}
	p.freshSequence = w.Sequence
	return true, nil
}
func (p *restorePlan) Restored(ctx context.Context, generation, output string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := p.verifyHistory(); err != nil {
		return false, err
	}
	w, err := readWitness(p.dataDir, p.session, generation)
	if err != nil {
		return false, err
	}
	if w.Generation != generation || w.Sequence <= p.freshSequence {
		return false, nil
	}
	if w.NativeID != p.target {
		return false, errors.New("memcode restored a different native identity")
	}
	text := terminalui.PlainTerminalText(output)
	return emptyComposer(output) && strings.Contains(text, fmt.Sprintf("↩ resumed %s (%d messages)", p.target, p.count)), nil
}
func emptyComposer(output string) bool {
	lines := strings.Split(strings.TrimSpace(terminalui.PlainTerminalText(output)), "\n")
	if len(lines) > 16 {
		lines = lines[len(lines)-16:]
	}
	idle, composer, footer := false, false, false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		idle = idle || line == "○ idle"
		composer = composer || line == "→  Ask memcode…   ·   $ = shell"
		footer = footer || strings.HasPrefix(line, "memcode · ")
	}
	return idle && composer && footer && !strings.Contains(output, "panic:")
}
func transcript(workspace, id string) ([32]byte, int, error) {
	var zero [32]byte
	if !nativeIDPattern.MatchString(id) {
		return zero, 0, errors.New("memcode full native session identity required")
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return zero, 0, err
	}
	relative := filepath.Join(".memcode", "sessions", id, "messages.json")
	// os.Root confines both traversal and symlinks to the selected workspace.
	confined, err := os.OpenRoot(root)
	if err != nil {
		return zero, 0, err
	}
	defer confined.Close()
	f, err := confined.Open(relative)
	if err != nil {
		return zero, 0, errors.New("memcode native history missing")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > 16<<20 {
		return zero, 0, errors.New("memcode native history is not a bounded regular file")
	}
	data := make([]byte, st.Size())
	if _, err = f.ReadAt(data, 0); err != nil {
		return zero, 0, err
	}
	var history struct {
		ID       string `json:"session_id"`
		Messages []struct {
			Role    string            `json:"role"`
			Content []json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err = json.Unmarshal(data, &history); err != nil {
		return zero, 0, errors.New("memcode malformed native history")
	}
	if history.ID != id || len(history.Messages) == 0 {
		return zero, 0, errors.New("memcode native history identity mismatch or empty history")
	}
	for _, m := range history.Messages {
		if m.Content == nil || m.Role != "user" && m.Role != "assistant" && m.Role != "system" && m.Role != "tool" {
			return zero, 0, errors.New("memcode invalid native wire history")
		}
	}
	return sha256.Sum256(data), len(history.Messages), nil
}
