package aider

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	aiderHistoryFile   = ".aider.chat.history.md"
	aiderHistoryMarker = ".aider.chat.history.md.ao-import"
	aiderHistoryLimit  = 16 << 20
)

var aiderHistoryID = regexp.MustCompile(`^aider_[0-9a-f]{64}$`)

var (
	_ ports.AgentImportedSessionRelocator = (*Plugin)(nil)
	_ ports.AgentImportedSessionPreparer  = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber      = (*Plugin)(nil)
	_ ports.AgentTranscriptLocator        = (*Plugin)(nil)
)

// NativeHistoryID names Aider's one native history file per repository. Aider
// persists no UUID; the original canonical filename remains the stable handle
// after its native Markdown is copied into AO's worktree.
func NativeHistoryID(root string) string {
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	sum := sha256.Sum256([]byte(filepath.Join(filepath.Clean(root), aiderHistoryFile)))
	return "aider_" + hex.EncodeToString(sum[:])
}

func readAiderFile(ctx context.Context, root *os.Root, name string, limit int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("aider history is not a bounded regular file")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("aider history exceeds read budget")
	}
	return data, ctx.Err()
}

func aiderHistory(ctx context.Context, root *os.Root) ([]byte, error) {
	data, err := readAiderFile(ctx, root, aiderHistoryFile, aiderHistoryLimit)
	if err != nil {
		return nil, err
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("#### ")) && len(bytes.TrimSpace(line[5:])) > 0 {
			return data, nil
		}
	}
	return nil, errors.New("aider native history has no user messages")
}

// writeAiderFile installs a whole private file without replacing an existing
// destination, including one created by a concurrent first-open attempt.
func writeAiderFile(ctx context.Context, root *os.Root, name string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	temp := ".aider-import-" + rand.Text()
	f, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(temp) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return root.Link(temp, name)
}

func checkAiderIdentity(ctx context.Context, root *os.Root, cwd, id string) error {
	marker, err := readAiderFile(ctx, root, aiderHistoryMarker, 128)
	if errors.Is(err, os.ErrNotExist) && NativeHistoryID(cwd) == id {
		return nil
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(marker)) != id {
		return errors.New("aider history belongs to a different imported conversation")
	}
	return nil
}

// RelocateImportedSession copies the selected native Markdown into the AO
// worktree. A marker preserves its file-based identity. Any existing native
// history is refused; shared preparation checkpoints prevent a second copy.
func (*Plugin) RelocateImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) error {
	if !aiderHistoryID.MatchString(r.NativeSessionID) || NativeHistoryID(r.ConfigDir) != r.NativeSessionID || filepath.Clean(r.TranscriptPath) != filepath.Join(filepath.Clean(r.ConfigDir), aiderHistoryFile) {
		return errors.New("aider import identity or source path changed")
	}
	dst, err := os.OpenRoot(r.LaunchCWD)
	if err != nil {
		return err
	}
	defer func() { _ = dst.Close() }()
	if _, err := dst.Lstat(aiderHistoryFile); err == nil {
		return errors.New("aider worktree already contains native history")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	src, err := os.OpenRoot(r.ConfigDir)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	data, err := aiderHistory(ctx, src)
	if err != nil {
		return err
	}
	if err := writeAiderFile(ctx, dst, aiderHistoryMarker, []byte(r.NativeSessionID+"\n")); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if err := checkAiderIdentity(ctx, dst, r.LaunchCWD, r.NativeSessionID); err != nil {
		return err
	}
	if err := writeAiderFile(ctx, dst, aiderHistoryFile, data); err != nil {
		return err
	}
	_, err = aiderHistory(ctx, dst)
	return err
}

// PrepareImportedSession returns the native copy for AO's separate resume
// checkpoint, preserving the original repository and history as provenance.
func (p *Plugin) PrepareImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	if err := p.RelocateImportedSession(ctx, r); err != nil {
		return ports.NativeSessionRef{}, err
	}
	return ports.NativeSessionRef{NativeSessionID: r.NativeSessionID, ConfigDir: r.LaunchCWD, TranscriptPath: filepath.Join(r.LaunchCWD, aiderHistoryFile)}, nil
}

// GetRestoreCommand restores Aider's native Markdown into its own message
// history. Explicit flags pin the file in the AO worktree, overriding config.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	id := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if !aiderHistoryID.MatchString(id) {
		if cfg.Session.Metadata[ports.MetadataKeyImportSourceNativeID] != "" {
			return nil, false, ports.ErrNativeHistoryUnavailable
		}
		return nil, false, ctx.Err()
	}
	if !filepath.IsAbs(cfg.Session.WorkspacePath) {
		return nil, false, ports.ErrNativeHistoryUnavailable
	}
	root, err := os.OpenRoot(cfg.Session.WorkspacePath)
	if err != nil {
		return nil, false, errors.Join(ports.ErrNativeHistoryUnavailable, err)
	}
	defer func() { _ = root.Close() }()
	if err := checkAiderIdentity(ctx, root, cfg.Session.WorkspacePath, id); err != nil {
		return nil, false, errors.Join(ports.ErrNativeHistoryUnavailable, err)
	}
	if _, err := aiderHistory(ctx, root); err != nil {
		return nil, false, errors.Join(ports.ErrNativeHistoryUnavailable, err)
	}
	cmd, err := p.GetLaunchCommand(ctx, ports.LaunchConfig{WorkspacePath: cfg.Session.WorkspacePath, Config: cfg.Config, Permissions: cfg.Permissions, SystemPrompt: cfg.SystemPrompt, SystemPromptFile: cfg.SystemPromptFile})
	if err != nil {
		return nil, false, err
	}
	cmd = append(cmd, "--restore-chat-history", "--chat-history-file", filepath.Join(cfg.Session.WorkspacePath, aiderHistoryFile))
	return cmd, true, nil
}

// LocateTranscript finds the repository's exact native Markdown history.
func (*Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if !aiderHistoryID.MatchString(ref.NativeSessionID) || ref.ConfigDir == "" {
		return "", false, ctx.Err()
	}
	root, err := os.OpenRoot(ref.ConfigDir)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer func() { _ = root.Close() }()
	if err := checkAiderIdentity(ctx, root, ref.ConfigDir, ref.NativeSessionID); err != nil {
		return "", false, err
	}
	_, err = aiderHistory(ctx, root)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	return filepath.Join(ref.ConfigDir, aiderHistoryFile), err == nil, err
}

// ProbeNativeSession reports whether the selected file still holds native history.
func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	if ref.ConfigDir == "" {
		return ports.NativeSessionAvailabilityUnknown, ctx.Err()
	}
	_, found, err := p.LocateTranscript(ctx, ref)
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	if found {
		return ports.NativeSessionAvailabilityAvailable, nil
	}
	return ports.NativeSessionAvailabilityUnavailable, nil
}
