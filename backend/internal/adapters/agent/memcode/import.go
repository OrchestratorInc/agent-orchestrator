package memcode

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	_ ports.AgentImportedSessionRelocator = (*Plugin)(nil)
	_ ports.AgentImportedSessionPreparer  = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber      = (*Plugin)(nil)
	_ ports.AgentTranscriptLocator        = (*Plugin)(nil)
)

// NativeHistoryFingerprint pins the exact native wire bytes by size and SHA256.
func NativeHistoryFingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return strconv.Itoa(len(data)) + ":" + hex.EncodeToString(sum[:])
}

func memcodeImportFile(ctx context.Context, root *os.Root, name string, limit int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("memcode import requires a bounded regular file")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, errors.New("memcode native file changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("memcode import exceeds read budget")
	}
	after, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(opened, after) {
		return nil, errors.New("memcode native file changed while reading")
	}
	return data, ctx.Err()
}

func memcodeImportSnapshot(ctx context.Context, source, id, rel, fingerprint string) ([]byte, error) {
	src, err := os.OpenRoot(source)
	if err != nil {
		return nil, err
	}
	defer func() { _ = src.Close() }()
	data, err := memcodeImportFile(ctx, src, rel, 16<<20)
	if err != nil {
		return nil, err
	}
	if fingerprint != "" && NativeHistoryFingerprint(data) != fingerprint {
		return nil, errors.New("memcode source changed since import")
	}
	if _, _, err := transcript(source, id); err != nil {
		return nil, err
	}
	return data, nil
}

// PrepareImportedSession checkpoints the selected native copy in the AO
// worktree before the existing interactive PrepareNativeRestore reads it.
func (p *Plugin) PrepareImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	if err := p.RelocateImportedSession(ctx, r); err != nil {
		return ports.NativeSessionRef{}, err
	}
	return ports.NativeSessionRef{NativeSessionID: r.NativeSessionID, ConfigDir: r.LaunchCWD, TranscriptPath: filepath.Join(r.LaunchCWD, ".memcode", "sessions", r.NativeSessionID, "messages.json")}, nil
}

func installMemcodeImport(ctx context.Context, root *os.Root, name string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	temp := filepath.Join(filepath.Dir(name), ".ao-import-"+rand.Text())
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

// RelocateImportedSession copies only the selected native wire transcript.
// Memcode initializes other project state itself from its current git worktree;
// no source config, credentials, database or other conversation is copied.
func (*Plugin) RelocateImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) error {
	if !nativeIDPattern.MatchString(r.NativeSessionID) || !filepath.IsAbs(r.ConfigDir) || !filepath.IsAbs(r.LaunchCWD) {
		return errors.New("memcode import requires an exact id and absolute project roots")
	}
	rel := filepath.Join(".memcode", "sessions", r.NativeSessionID, "messages.json")
	if filepath.Clean(r.TranscriptPath) != filepath.Join(filepath.Clean(r.ConfigDir), rel) {
		return errors.New("memcode import source path changed")
	}
	launch, err := filepath.EvalSymlinks(r.LaunchCWD)
	if err != nil {
		return err
	}
	dst, err := os.OpenRoot(launch)
	if err != nil {
		return err
	}
	defer func() { _ = dst.Close() }()
	if recovered, err := recoverMemcodePreparation(ctx, dst, r); err != nil || recovered {
		return err
	}
	source, err := filepath.EvalSymlinks(r.ConfigDir)
	if err != nil {
		return err
	}
	if source == launch {
		_, err := memcodeImportSnapshot(ctx, source, r.NativeSessionID, rel, r.SourceFingerprint)
		return err
	}
	wantMarker := memcodeImportOrigin(r.TranscriptPath)
	marker := filepath.Join(filepath.Dir(rel), ".ao-import")
	if _, err := dst.Lstat(rel); err == nil {
		existing, err := memcodeImportFile(ctx, dst, rel, 16<<20)
		if err != nil {
			return err
		}
		got, markerErr := memcodeImportFile(ctx, dst, marker, 128)
		if r.SourceFingerprint != "" {
			if NativeHistoryFingerprint(existing) != r.SourceFingerprint {
				return errors.New("memcode worktree history differs from the imported snapshot")
			}
			if _, err := memcodeImportSnapshot(ctx, source, r.NativeSessionID, rel, r.SourceFingerprint); err != nil {
				return err
			}
			if errors.Is(markerErr, os.ErrNotExist) {
				if err := installMemcodeImport(ctx, dst, marker, []byte(wantMarker)); err != nil && !errors.Is(err, os.ErrExist) {
					return err
				}
				got, markerErr = memcodeImportFile(ctx, dst, marker, 128)
			}
		}
		if markerErr != nil || string(got) != wantMarker {
			return errors.New("memcode destination belongs to another conversation")
		}
		installed, err := memcodeImportFile(ctx, dst, rel, 16<<20)
		if err != nil {
			return err
		}
		if r.SourceFingerprint != "" && NativeHistoryFingerprint(installed) != r.SourceFingerprint {
			return errors.New("memcode worktree history changed during preparation")
		}
		if _, _, err := transcript(launch, r.NativeSessionID); err != nil {
			return err
		}
		return writeMemcodePreparation(ctx, dst, r)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := memcodeImportSnapshot(ctx, source, r.NativeSessionID, rel, r.SourceFingerprint)
	if err != nil {
		return err
	}
	if err := dst.MkdirAll(filepath.Dir(rel), 0o700); err != nil {
		return err
	}
	if err := installMemcodeImport(ctx, dst, rel, data); err != nil {
		return err
	}
	if err := installMemcodeImport(ctx, dst, marker, []byte(wantMarker)); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	got, err := memcodeImportFile(ctx, dst, marker, 128)
	if err != nil || string(got) != wantMarker {
		return errors.New("memcode destination marker changed")
	}
	installed, err := memcodeImportFile(ctx, dst, rel, 16<<20)
	if err != nil {
		return err
	}
	if NativeHistoryFingerprint(installed) != NativeHistoryFingerprint(data) {
		return errors.New("memcode worktree history changed during preparation")
	}
	if _, _, err := transcript(launch, r.NativeSessionID); err != nil {
		return err
	}
	return writeMemcodePreparation(ctx, dst, r)
}

// LocateTranscript checks exact native wire history at the registered project.
func (*Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if ref.ConfigDir == "" || !nativeIDPattern.MatchString(ref.NativeSessionID) {
		return "", false, nil
	}
	_, _, err := transcript(ref.ConfigDir, ref.NativeSessionID)
	if errors.Is(err, ports.ErrNativeHistoryUnavailable) || errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return filepath.Join(ref.ConfigDir, ".memcode", "sessions", ref.NativeSessionID, "messages.json"), true, nil
}

// ProbeNativeSession verifies that Memcode can restore the exact conversation.
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
