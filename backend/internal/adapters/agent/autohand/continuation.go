package autohand

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var autohandNativeID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}-[0-9]+$`)

var (
	_ ports.AgentNativeSessionConfigProvider  = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber          = (*Plugin)(nil)
	_ ports.AgentTranscriptLocator            = (*Plugin)(nil)
	_ ports.AgentInterfaceHandoffHistoryProbe = (*Plugin)(nil)
	_ ports.AgentNativeRestoreCommand         = (*Plugin)(nil)
)

// NativeSessionConfigDir mirrors AUTOHAND_HOME, not AUTOHAND_CONFIG (which
// selects only a config file). A launch-local empty override uses the default.
func (p *Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	root, set := env["AUTOHAND_HOME"]
	if !set {
		root = os.Getenv("AUTOHAND_HOME")
	}
	if root = strings.TrimSpace(root); root != "" {
		return filepath.Abs(root)
	}
	home := env["HOME"]
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(home, ".autohand"), nil
}

// LocateTranscript finds Autohand's native conversation within its state root.
func (p *Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if !autohandNativeID.MatchString(ref.NativeSessionID) {
		return "", false, errors.New("autohand invalid native session id")
	}
	if ref.ConfigDir == "" {
		return "", false, nil
	}
	root, err := os.OpenRoot(ref.ConfigDir)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer func() { _ = root.Close() }()
	rel := filepath.Join("sessions", ref.NativeSessionID, "conversation.jsonl")
	info, err := root.Lstat(rel)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > 128<<20 {
		return "", false, errors.New("autohand native history is not a bounded regular file")
	}
	if err := validateAutohandNativeHistory(ctx, root, ref.NativeSessionID); err != nil {
		return "", false, err
	}
	return filepath.Join(ref.ConfigDir, rel), true, nil
}

func validateAutohandNativeHistory(ctx context.Context, root *os.Root, id string) error {
	metadataPath := filepath.Join("sessions", id, "metadata.json")
	info, err := root.Lstat(metadataPath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return errors.New("autohand native metadata is not a bounded regular file")
	}
	metadata, err := root.Open(metadataPath)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(metadata, (1<<20)+1))
	_ = metadata.Close()
	if err != nil {
		return err
	}
	var meta struct {
		ID  string `json:"sessionId"`
		CWD string `json:"projectPath"`
	}
	if len(data) > 1<<20 || json.Unmarshal(data, &meta) != nil || meta.ID != id || !filepath.IsAbs(meta.CWD) {
		return errors.New("autohand native metadata is invalid")
	}
	f, err := root.Open(filepath.Join("sessions", id, "conversation.jsonl"))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(io.LimitReader(f, (128<<20)+1))
	scanner.Buffer(make([]byte, 64<<10), 64<<20)
	count, readBytes := 0, 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Bytes()
		readBytes += len(line) + 1
		if readBytes > 128<<20 {
			return errors.New("autohand native history exceeds read budget")
		}
		if strings.TrimSpace(string(line)) == "" {
			continue
		}
		var message struct {
			Role    string  `json:"role"`
			Content *string `json:"content"`
		}
		if json.Unmarshal(line, &message) != nil || message.Content == nil || message.Role != "user" && message.Role != "assistant" && message.Role != "system" && message.Role != "tool" {
			return errors.New("autohand native history contains a malformed record")
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if count == 0 {
		return errors.New("autohand native history is empty")
	}
	return validateAutohandNativeState(ctx, root, id)
}

func validateAutohandNativeState(ctx context.Context, root *os.Root, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path := filepath.Join("sessions", id, "state.json")
	info, err := root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 8<<20 {
		return errors.New("autohand native state is not a bounded regular file")
	}
	f, err := root.Open(path)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	_ = f.Close()
	if err != nil {
		return err
	}
	var state map[string]json.RawMessage
	if len(data) > 8<<20 || json.Unmarshal(data, &state) != nil || state == nil {
		return errors.New("autohand native state is malformed")
	}
	return ctx.Err()
}

// GetRestoreCommandWithSessionID makes imported history validation a hard
// command-construction error even after adoption, when the ordinary manager
// probe treats an error as unknown. Managed non-import recovery is unchanged.
func (p *Plugin) GetRestoreCommandWithSessionID(ctx context.Context, cfg ports.RestoreConfig) ([]string, string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", false, err
	}
	id := cfg.Session.Metadata[ports.MetadataKeyAgentSessionID]
	if source := cfg.Session.Metadata[ports.MetadataKeyImportSourceNativeID]; source != "" {
		expected := cfg.Session.Metadata[ports.MetadataKeyImportResumeNativeID]
		if expected == "" {
			expected = source
		}
		root := cfg.Session.Metadata[ports.MetadataKeyImportConfigDir]
		if !autohandNativeID.MatchString(id) || id != expected || !filepath.IsAbs(root) {
			return nil, "", false, ports.ErrNativeHistoryUnavailable
		}
		_, found, err := p.LocateTranscript(ctx, ports.NativeSessionRef{NativeSessionID: id, ConfigDir: root})
		if err != nil || !found {
			return nil, "", false, errors.Join(ports.ErrNativeHistoryUnavailable, err)
		}
	}
	cmd, ok, err := p.GetRestoreCommand(ctx, cfg)
	return cmd, id, ok, err
}

// NativeConversationExists validates the exact files native resume loads.
// Autohand catches malformed history and starts fresh, so strict resumes must
// refuse it before launching, including later opens of an adopted import.
func (p *Plugin) NativeConversationExists(ctx context.Context, _ ports.SessionRef, nativeID string, env map[string]string) (bool, error) {
	root, err := p.NativeSessionConfigDir(ctx, env)
	if err != nil {
		return false, err
	}
	_, found, err := p.LocateTranscript(ctx, ports.NativeSessionRef{NativeSessionID: nativeID, ConfigDir: root})
	return found, err
}

// ProbeNativeSession reports whether Autohand's local conversation is available.
func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	if ref.ConfigDir == "" {
		return ports.NativeSessionAvailabilityUnknown, ctx.Err()
	}
	_, exists, err := p.LocateTranscript(ctx, ref)
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	if exists {
		return ports.NativeSessionAvailabilityAvailable, nil
	}
	return ports.NativeSessionAvailabilityUnavailable, nil
}
