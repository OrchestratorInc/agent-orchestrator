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

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var component = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

type witness struct {
	Generation string `json:"generation"`
	NativeID   string `json:"native_id"`
	Sequence   int    `json:"sequence"`
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
func EmitStartWitness(out io.Writer) error {
	data, session, generation := os.Getenv("AO_MEMCODE_DATA_DIR"), os.Getenv("AO_MEMCODE_SESSION"), os.Getenv("AO_MEMCODE_GENERATION")
	path, err := witnessPath(data, session, generation)
	if err != nil {
		return err
	}
	id := os.Getenv("MEMCODE_SESSION_ID")
	if !nativeIDPattern.MatchString(id) {
		return errors.New("invalid memcode native hook identity")
	}
	old, err := readWitness(data, session, generation)
	if err != nil {
		return err
	}
	sequence := 1
	if old.Generation == generation {
		sequence = old.Sequence + 1
	}
	b, _ := json.Marshal(witness{Generation: generation, NativeID: id, Sequence: sequence})
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := hookutil.AtomicWriteFile(path, b, 0o600); err != nil {
		return err
	}
	return EmitInstructionChunk(out, "context-chunk-0")
}

// GetAgentHooks implements the native TUI adapter contract.
func (*Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !component.MatchString(cfg.SessionID) {
		return errors.New("invalid AO session identity")
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
	if len(chunks) > 64 {
		return errors.New("memcode private instruction chunk count exceeds bounded limit")
	}
	private := filepath.Join(cfg.DataDir, "agent-launches", "memcode", cfg.SessionID)
	if err := os.MkdirAll(private, 0o700); err != nil {
		return err
	}
	for i, chunk := range chunks {
		if err := hookutil.AtomicWriteFile(filepath.Join(private, fmt.Sprintf("instructions-%d.txt", i)), chunk, 0o600); err != nil {
			return err
		}
	}
	dir := filepath.Join(cfg.WorkspacePath, ".memcode")
	path := filepath.Join(dir, "hooks.json")
	var config map[string]json.RawMessage
	b, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(b, &config); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if config == nil {
		config = map[string]json.RawMessage{}
	}
	hooks := map[string][]map[string]json.RawMessage{}
	if b := config["hooks"]; len(b) > 0 {
		if err := json.Unmarshal(b, &hooks); err != nil {
			return err
		}
	}
	for native, event := range map[string]string{"session_start": "session-start", "pre_tool_use": "pre-tool-use", "post_tool_use": "post-tool-use"} {
		entries := hooks[native][:0]
		for _, entry := range hooks[native] {
			var command string
			_ = json.Unmarshal(entry["command"], &command)
			if !strings.Contains(command, "ao hooks memcode ") {
				entries = append(entries, entry)
			}
		}
		command, _ := json.Marshal("ao hooks memcode " + event)
		entries = append(entries, map[string]json.RawMessage{"command": command, "timeout": json.RawMessage("2")})
		if native == "session_start" {
			for i := 1; i < len(chunks); i++ {
				command, _ := json.Marshal(fmt.Sprintf("ao hooks memcode context-chunk-%d", i))
				entries = append(entries, map[string]json.RawMessage{"command": command, "timeout": json.RawMessage("2")})
			}
		}
		hooks[native] = entries
	}
	config["hooks"], err = json.Marshal(hooks)
	if err != nil {
		return err
	}
	b, err = json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := hookutil.AtomicWriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("memcode hooks: %w", err)
	}
	return hookutil.EnsureWorkspaceGitignore(dir, "hooks.json")
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
	if err != nil || index < 0 || index > 63 {
		return errors.New("invalid memcode context chunk")
	}
	data, session := os.Getenv("AO_MEMCODE_DATA_DIR"), os.Getenv("AO_MEMCODE_SESSION")
	if !component.MatchString(session) || !filepath.IsAbs(data) {
		return errors.New("invalid private memcode context location")
	}
	file, err := os.Open(filepath.Join(data, "agent-launches", "memcode", session, fmt.Sprintf("instructions-%d.txt", index)))
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil {
		return err
	}
	if len(content) > 8192 {
		return errors.New("memcode native hook chunk exceeds output limit")
	}
	_, err = out.Write(content)
	return err
}
