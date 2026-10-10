package memcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
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
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err = hookutil.AtomicWriteFile(path, b, 0600); err != nil {
		return err
	}
	instructions, err := os.ReadFile(filepath.Join(data, "agent-launches", "memcode", session, "instructions.txt"))
	if err != nil {
		return err
	}
	if len(instructions) > 8192 {
		return errors.New("memcode standing instructions exceed native hook output limit")
	}
	_, err = out.Write(instructions)
	return err
}
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
	if len(instructions) > 8192 {
		return errors.New("memcode standing instructions exceed native hook output limit")
	}
	private := filepath.Join(cfg.DataDir, "agent-launches", "memcode", cfg.SessionID)
	if err := os.MkdirAll(private, 0700); err != nil {
		return err
	}
	if err := hookutil.AtomicWriteFile(filepath.Join(private, "instructions.txt"), instructions, 0600); err != nil {
		return err
	}
	dir := filepath.Join(cfg.WorkspacePath, ".memcode")
	path := filepath.Join(dir, "hooks.json")
	var config map[string]json.RawMessage
	b, err := os.ReadFile(path)
	if err == nil {
		if err = json.Unmarshal(b, &config); err != nil {
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
		if err = json.Unmarshal(b, &hooks); err != nil {
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
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err = hookutil.AtomicWriteFile(path, b, 0600); err != nil {
		return fmt.Errorf("memcode hooks: %w", err)
	}
	return hookutil.EnsureWorkspaceGitignore(dir, "hooks.json")
}
func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "pre-tool-use":
		return domain.ActivityActive, true
	default:
		return "", false
	}
}
