package openinterpreter

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/klauspost/compress/zstd"
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
	_, ok, err := findTranscript(ctx, filepath.Join(ref.ConfigDir, "sessions"), ref.NativeSessionID)
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
	path, ok, err := findTranscript(ctx, filepath.Join(ref.ConfigDir, "sessions"), ref.NativeSessionID)
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
func (p *Plugin) validateRestoreWorkspace(ctx context.Context, cfg ports.RestoreConfig, id string) error {
	home, err := p.NativeSessionConfigDir(ctx, cfg.Env)
	if err != nil {
		return err
	}
	path, ok, err := findTranscript(ctx, filepath.Join(home, "sessions"), id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("open-interpreter: cannot verify native session workspace from active history")
	}
	metadata, err := readSessionMetadata(path)
	if err != nil {
		return err
	}
	native, err := os.Stat(metadata.CWD)
	if err != nil {
		return fmt.Errorf("open-interpreter: native session workspace is unavailable: %w", err)
	}
	workspace, err := os.Stat(cfg.Session.WorkspacePath)
	if err != nil {
		return fmt.Errorf("open-interpreter: AO workspace is unavailable: %w", err)
	}
	if !native.IsDir() || !workspace.IsDir() || !os.SameFile(native, workspace) {
		return fmt.Errorf("open-interpreter: native session workspace does not match the AO workspace")
	}
	return nil
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
	defer file.Close()
	var reader io.Reader = file
	if strings.HasSuffix(path, ".zst") {
		decoder, err := zstd.NewReader(io.LimitReader(file, 2<<20), zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(16<<20), zstd.WithDecoderMaxWindow(16<<20))
		if err != nil {
			return sessionMetadata{}, err
		}
		defer decoder.Close()
		reader = decoder
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 256<<10)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return sessionMetadata{}, err
		}
		return sessionMetadata{}, fmt.Errorf("open-interpreter: empty native transcript")
	}
	var record struct {
		Type    string          `json:"type"`
		Payload sessionMetadata `json:"payload"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
		return sessionMetadata{}, err
	}
	if record.Type != "session_meta" {
		return sessionMetadata{}, fmt.Errorf("open-interpreter: missing native session metadata")
	}
	return record.Payload, nil
}

func findTranscript(ctx context.Context, root, value string) (string, bool, error) {
	id, err := nativeID(value)
	if err != nil {
		return "", false, err
	}
	var found string
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
			return fs.SkipAll
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	return found, found != "", err
}
