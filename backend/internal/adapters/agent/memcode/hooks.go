package memcode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hooksjson"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var component = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

const maxInstructionChunks = 64

const instructionChunkCountEnv = "AO_MEMCODE_CHUNK_COUNT"

var memcodeHooks = hooksjson.Manager{
	Label: "memcode", Flat: true, CommandPrefix: "ao hooks memcode ",
	LegacyCommandPrefixes: []string{"ao hooks memcode "}, Timeout: 30,
	Path: func(workspace string) string { return filepath.Join(workspace, ".memcode", "hooks.json") },
	Managed: func() []hooksjson.HookSpec {
		// Memcode truncates each hook's stdout at 8 KiB. Stable slots preserve
		// that bound without letting sessions sharing a workspace change each
		// other's hook list. Unused slots emit nothing.
		specs := []hooksjson.HookSpec{{Event: "session_start", Command: "ao hooks memcode session-start"}}
		for i := 1; i < maxInstructionChunks; i++ {
			specs = append(specs, hooksjson.HookSpec{Event: "session_start", Command: fmt.Sprintf("ao hooks memcode context-chunk-%d", i)})
		}
		return append(specs, hooksjson.HookSpec{Event: "pre_tool_use", Command: "ao hooks memcode pre-tool-use"},
			hooksjson.HookSpec{Event: "post_tool_use"}) // remove the formerly installed no-op hook
	}(),
}

type witness struct {
	Generation      string `json:"generation"`
	NativeID        string `json:"native_id"`
	Sequence        int    `json:"sequence"`
	RestoreSequence int    `json:"restore_sequence,omitempty"`
}

func witnessPath(data, session, generation string) (string, error) {
	if !component.MatchString(session) || !component.MatchString(generation) || !filepath.IsAbs(data) {
		return "", errors.New("invalid memcode launch witness location")
	}
	return filepath.Join(data, "agent-launches", "memcode", session, generation, "witness.json"), nil
}
func readWitness(data, session, generation string) (witness, error) {
	path, err := witnessPath(data, session, generation)
	if err != nil {
		return witness{}, err
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return witness{}, nil
	}
	if err != nil {
		return witness{}, err
	}
	var w witness
	err = json.Unmarshal(b, &w)
	return w, err
}

// AugmentRuntimeLaunchEnv implements the native TUI adapter contract.
func (*Plugin) AugmentRuntimeLaunchEnv(env map[string]string, data string, id domain.SessionID, generation string) {
	env["AO_MEMCODE_DATA_DIR"] = data
	env["AO_MEMCODE_GENERATION"] = generation
	env["AO_MEMCODE_SESSION"] = string(id)
}

// EmitStartWitness runs before any daemon callback. Interactive restore hooks
// return locally so PrepareLaunch cannot deadlock and a fresh ID cannot overwrite
// the durable target. Native session_end is never reported as process exit.
func EmitStartWitness(out io.Writer) (bool, error) {
	data, session, generation := os.Getenv("AO_MEMCODE_DATA_DIR"), os.Getenv("AO_MEMCODE_SESSION"), os.Getenv("AO_MEMCODE_GENERATION")
	path, err := witnessPath(data, session, generation)
	if err != nil {
		return false, err
	}
	id := os.Getenv("MEMCODE_SESSION_ID")
	if !nativeIDPattern.MatchString(id) {
		return false, errors.New("invalid memcode native hook identity")
	}
	old, err := readWitness(data, session, generation)
	if err != nil {
		return false, err
	}
	sequence := 1
	if old.Generation == generation {
		sequence = old.Sequence + 1
	}
	restoreSequence := old.RestoreSequence
	if target := os.Getenv("AO_MEMCODE_RESTORE_TARGET"); target != "" && id == target && old.Sequence > 0 && restoreSequence == 0 {
		restoreSequence = sequence
	}
	b, _ := json.Marshal(witness{Generation: generation, NativeID: id, Sequence: sequence, RestoreSequence: restoreSequence})
	if err := EmitInstructionChunk(out, "context-chunk-0"); err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	if err := hookutil.AtomicWriteFile(path, b, 0o600); err != nil {
		return false, err
	}
	return restoreSequence > 0 && sequence > restoreSequence, nil
}

// GetAgentHooks implements the native TUI adapter contract.
func (*Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !component.MatchString(cfg.SessionID) {
		return errors.New("invalid AO session identity")
	}
	if cfg.Config.Model != "" && cfg.Env["MEMCODE_ENDPOINT_URL"] != "" {
		return errors.New("memcode custom endpoint models must be selected in the native TUI")
	}
	instructions := []byte(cfg.SystemPrompt)
	if cfg.SystemPromptFile != "" {
		b, err := os.ReadFile(cfg.SystemPromptFile)
		if err != nil {
			return err
		}
		instructions = b
	}
	if len(instructions) > 512<<10 {
		return errors.New("memcode standing instructions exceed bounded private context limit")
	}
	chunks, err := instructionChunks(instructions)
	if err != nil {
		return err
	}
	if len(chunks) > maxInstructionChunks {
		return errors.New("memcode private instruction chunk count exceeds bounded limit")
	}
	private := filepath.Join(cfg.DataDir, "agent-launches", "memcode", cfg.SessionID)
	if err := os.MkdirAll(private, 0o700); err != nil {
		return err
	}
	// One atomic snapshot also prevents stale chunks after a shorter reprepare.
	b, err := json.Marshal(chunks)
	if err != nil {
		return err
	}
	if err := hookutil.AtomicWriteFile(filepath.Join(private, "instructions.json"), b, 0o600); err != nil {
		return err
	}
	if err := memcodeHooks.Install(ctx, cfg.WorkspacePath); err != nil {
		return err
	}
	if err := hookutil.EnsureWorkspaceGitignore(filepath.Join(cfg.WorkspacePath, ".memcode"), "hooks.json", "sessions/"); err != nil {
		return err
	}
	if cfg.Env != nil {
		// Pin this launch's count, never the last session to install shared hooks.
		cfg.Env[instructionChunkCountEnv] = strconv.Itoa(len(chunks))
	}
	return nil
}

// UninstallHooks removes AO commands without affecting another session's private context.
func (*Plugin) UninstallHooks(ctx context.Context, workspace string) error {
	return memcodeHooks.Uninstall(ctx, workspace)
}

// AreHooksInstalled reports whether the workspace has AO-managed hooks.
func (*Plugin) AreHooksInstalled(ctx context.Context, workspace string) (bool, error) {
	return memcodeHooks.AreInstalled(ctx, workspace)
}

// CleanupWorkspace removes only the private launch artifacts owned by the
// terminated AO session. The project-local Memcode hook configuration remains.
func (*Plugin) CleanupWorkspace(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !component.MatchString(cfg.SessionID) || !filepath.IsAbs(cfg.DataDir) {
		return errors.New("invalid private memcode cleanup location")
	}
	path := filepath.Join(cfg.DataDir, "agent-launches", "memcode", cfg.SessionID)
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("memcode cleanup: %w", err)
	}
	return nil
}

// DeriveActivityState maps supported native tool activity hooks.
func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "pre-tool-use":
		return domain.ActivityActive, true
	default:
		return "", false
	}
}

// Native trims each hook output and joins outputs with two newlines. Only
// paragraph boundaries that reproduce that exact combination may be split.
func instructionChunks(data []byte) ([][]byte, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return [][]byte{{}}, nil
	}
	var chunks [][]byte
	remaining := data
	for len(remaining) > 8192 {
		end := bytes.LastIndex(remaining[:8192], []byte("\n\n"))
		for end > 0 && (!bytes.Equal(bytes.TrimSpace(remaining[:end]), remaining[:end]) ||
			!bytes.Equal(bytes.TrimSpace(remaining[end+2:]), remaining[end+2:])) {
			end = bytes.LastIndex(remaining[:end], []byte("\n\n"))
		}
		if end <= 0 {
			return nil, errors.New("memcode private instruction paragraph exceeds native hook output limit")
		}
		chunk := append([]byte(nil), remaining[:end]...)
		chunks = append(chunks, chunk)
		remaining = remaining[end+2:]
	}
	chunks = append(chunks, append([]byte(nil), remaining...))
	trimmed := make([][]byte, len(chunks))
	for i, chunk := range chunks {
		trimmed[i] = bytes.TrimSpace(chunk)
	}
	if !bytes.Equal(bytes.Join(trimmed, []byte("\n\n")), data) {
		return nil, errors.New("memcode native hook combination would alter standing instructions")
	}
	return chunks, nil
}

// EmitInstructionChunk emits one bounded private native context hook.
func EmitInstructionChunk(out io.Writer, event string) error {
	index, err := strconv.Atoi(strings.TrimPrefix(event, "context-chunk-"))
	if err != nil || index < 0 || index >= maxInstructionChunks {
		return errors.New("invalid memcode context chunk")
	}
	data, session := os.Getenv("AO_MEMCODE_DATA_DIR"), os.Getenv("AO_MEMCODE_SESSION")
	if !component.MatchString(session) || !filepath.IsAbs(data) {
		return errors.New("invalid private memcode context location")
	}
	count := 0
	if raw := os.Getenv(instructionChunkCountEnv); raw != "" {
		count, err = strconv.Atoi(raw)
		if err != nil || count < 1 || count > maxInstructionChunks {
			return errors.New("invalid memcode launch instruction chunk count")
		}
		if index >= count {
			return nil // unused slots need no file I/O or JSON decoding
		}
	}
	file, err := os.Open(filepath.Join(data, "agent-launches", "memcode", session, "instructions.json"))
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	var chunks [][]byte
	if err := json.NewDecoder(io.LimitReader(file, 1<<20)).Decode(&chunks); err != nil {
		return err
	}
	if len(chunks) == 0 || len(chunks) > maxInstructionChunks {
		return errors.New("invalid memcode instruction chunk count")
	}
	if count != 0 && count != len(chunks) {
		return errors.New("memcode launch instruction chunk count changed")
	}
	if index >= len(chunks) {
		return nil
	}
	content := chunks[index]
	if len(content) > 8192 {
		return errors.New("memcode native hook chunk exceeds output limit")
	}
	_, err = out.Write(content)
	return err
}
