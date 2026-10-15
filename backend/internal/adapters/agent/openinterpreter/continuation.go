package openinterpreter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ContinuationCapabilities reports provider-assigned native UUIDs.
func (p *Plugin) ContinuationCapabilities() ports.ContinuationCapabilities {
	return ports.ContinuationCapabilities{FreshNativeSessionID: ports.FreshNativeSessionIDProviderAssigned}
}

// NativeSessionConfigDir preserves the invocation's provider-owned profile.
func (p *Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return nativeconfig.Resolve(env, "INTERPRETER_HOME", ".openinterpreter")
}

// ProbeNativeSession checks active native state without invoking a provider.
// Compressed cold history is read without materializing or changing it.
func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	if ref.ConfigDir == "" {
		return ports.NativeSessionAvailabilityUnknown, ctx.Err()
	}
	_, _, ok, err := findTranscript(ctx, filepath.Join(ref.ConfigDir, "sessions"), ref.NativeSessionID)
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	if ok {
		return ports.NativeSessionAvailabilityAvailable, nil
	}
	return ports.NativeSessionAvailabilityUnavailable, nil
}

// LocateTranscript returns only a regular JSONL with matching native metadata.
func (p *Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if ref.ConfigDir == "" {
		return "", false, ctx.Err()
	}
	path, _, ok, err := findTranscript(ctx, filepath.Join(ref.ConfigDir, "sessions"), ref.NativeSessionID)
	// Shared transcript consumers expect plain JSONL; native resume itself
	// materializes compressed history before appending to it.
	if strings.HasSuffix(path, ".zst") {
		return "", false, err
	}
	return path, ok, err
}

// Native --cd suppresses its historical-workspace check. Validate the recorded
// workspace first, and refuse unreadable history instead of moving a
// foreign conversation into the AO worktree.
func (p *Plugin) validateRestoreWorkspace(ctx context.Context, cfg ports.RestoreConfig, id string) (bool, error) {
	home, err := p.NativeSessionConfigDir(ctx, cfg.Env)
	if err != nil {
		return false, err
	}
	_, metadata, ok, err := findTranscript(ctx, filepath.Join(home, "sessions"), id)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		// Damaged or unreadable history cannot be resumed, but a fresh launch
		// in the AO workspace can still recover the worker.
		return false, nil
	}
	if !ok {
		return false, nil
	}
	native, err := os.Stat(metadata.CWD)
	if err != nil {
		return false, fmt.Errorf("open-interpreter: native session workspace is unavailable: %w", err)
	}
	workspace, err := os.Stat(cfg.Session.WorkspacePath)
	if err != nil {
		return false, fmt.Errorf("open-interpreter: AO workspace is unavailable: %w", err)
	}
	if !native.IsDir() || !workspace.IsDir() || !os.SameFile(native, workspace) {
		return false, fmt.Errorf("open-interpreter: native session workspace does not match the AO workspace")
	}
	return true, nil
}

type sessionMetadata struct {
	ID  string `json:"id"`
	CWD string `json:"cwd"`
}

func readSessionMetadata(path string) (sessionMetadata, error) {
	file, err := os.Open(path)
	if err != nil {
		return sessionMetadata{}, err
	}
	defer func() { _ = file.Close() }()
	var reader io.Reader = file
	if strings.HasSuffix(path, ".zst") {
		decoder, err := zstd.NewReader(io.LimitReader(file, 32<<20), zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(16<<20), zstd.WithDecoderMaxWindow(16<<20))
		if err != nil {
			return sessionMetadata{}, err
		}
		defer decoder.Close()
		reader = decoder
	}
	data, err := io.ReadAll(io.LimitReader(reader, (64<<20)+1))
	if err != nil {
		return sessionMetadata{}, err
	}
	if len(data) > 64<<20 {
		return sessionMetadata{}, fmt.Errorf("open-interpreter: native history exceeds size limit")
	}
	lines := bytes.Split(data, []byte{'\n'})
	if len(lines[0]) == 0 {
		return sessionMetadata{}, fmt.Errorf("open-interpreter: empty native transcript")
	}
	var record struct {
		Timestamp *string         `json:"timestamp"`
		Type      string          `json:"type"`
		Payload   sessionMetadata `json:"payload"`
	}
	if len(lines[0]) > 256<<10 {
		return sessionMetadata{}, fmt.Errorf("open-interpreter: native metadata exceeds size limit")
	}
	if err := json.Unmarshal(lines[0], &record); err != nil {
		return sessionMetadata{}, err
	}
	if record.Type != "session_meta" || record.Timestamp == nil {
		return sessionMetadata{}, fmt.Errorf("open-interpreter: missing native session metadata")
	}
	// A valid user turn proves preserved conversation history. Native resume
	// tolerates a torn final write, so it does not invalidate earlier turns.
	hasUser := false
	for index, line := range lines[1:] {
		if len(line) == 0 {
			continue
		}
		var item struct {
			Timestamp *string `json:"timestamp"`
			Type      string  `json:"type"`
			Payload   struct {
				Type    string          `json:"type"`
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(line, &item); err != nil {
			if index == len(lines)-2 && data[len(data)-1] != '\n' {
				continue
			}
			return sessionMetadata{}, fmt.Errorf("open-interpreter: malformed native history: %w", err)
		}
		if item.Timestamp == nil {
			return sessionMetadata{}, fmt.Errorf("open-interpreter: native history is missing a timestamp")
		}
		if item.Type == "response_item" && item.Payload.Type == "message" && item.Payload.Role == "user" {
			if !validNativeUserContent(item.Payload.Content) {
				return sessionMetadata{}, fmt.Errorf("open-interpreter: native user history has invalid or empty content")
			}
			hasUser = true
		}
	}
	if !hasUser {
		return sessionMetadata{}, fmt.Errorf("open-interpreter: native history has no user conversation")
	}
	return record.Payload, nil
}

// Match the released native ContentItem variants for the user row used as
// conversation evidence. A role tag alone is not a native-decodable message.
func validNativeUserContent(data json.RawMessage) bool {
	var parts []struct {
		Type     string  `json:"type"`
		Text     *string `json:"text"`
		ImageURL *string `json:"image_url"`
		FileID   *string `json:"file_id"`
		AudioURL *string `json:"audio_url"`
		Detail   *string `json:"detail"`
	}
	if json.Unmarshal(data, &parts) != nil || len(parts) == 0 {
		return false
	}
	meaningful := false
	for _, part := range parts {
		var value *string
		switch part.Type {
		case "input_text", "output_text":
			value = part.Text
		case "input_audio":
			value = part.AudioURL
		case "input_image":
			if part.Detail != nil && *part.Detail != "auto" && *part.Detail != "low" && *part.Detail != "high" && *part.Detail != "original" {
				return false
			}
			value = part.ImageURL
			if value == nil {
				value = part.FileID
			}
		default:
			return false
		}
		if value == nil {
			return false
		}
		meaningful = meaningful || strings.TrimSpace(*value) != ""
	}
	return meaningful
}

func findTranscript(ctx context.Context, root, value string) (string, sessionMetadata, bool, error) {
	id, err := nativeID(value)
	if err != nil {
		return "", sessionMetadata{}, false, err
	}
	var found string
	var foundMetadata sessionMetadata
	visited := 0
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		visited++
		if visited > 50000 {
			return fmt.Errorf("open-interpreter: native transcript scan limit exceeded")
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		if !strings.HasSuffix(entry.Name(), "-"+id+".jsonl") && !strings.HasSuffix(entry.Name(), "-"+id+".jsonl.zst") {
			return nil
		}
		metadata, err := readSessionMetadata(path)
		if err != nil {
			return err
		}
		if metadata.ID == id {
			found = path
			foundMetadata = metadata
			return fs.SkipAll
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	return found, foundMetadata, found != "", err
}
