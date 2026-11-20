package agy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// This is the CLI/schema pair whose metadata-only remap was verified with
// actual run_command output. Unknown releases must not resume a prepared copy.
const supportedImportVersion = "1.5.13"
const importProofName = ".ao-import.json"

var _ ports.AgentImportedSessionPreparer = (*Plugin)(nil)
var _ ports.AgentNativeSessionProber = (*Plugin)(nil)
var _ ports.AgentTranscriptLocator = (*Plugin)(nil)

type agyImportProof struct {
	ID, SourceRoot, SourcePath, CWD, Fingerprint string
	Version                                      string
}

func (p *Plugin) checkImportVersion(ctx context.Context) error {
	var version string
	var err error
	if p.importVersion != nil {
		version, err = p.importVersion(ctx)
	} else {
		binary, resolveErr := p.agyBinary(ctx)
		if resolveErr != nil {
			return resolveErr
		}
		version, err = importBinaryVersion(ctx, binary)
	}
	if err != nil {
		return err
	}
	if version != supportedImportVersion {
		return errors.New("agy: native import requires the verified CLI version 1.5.13")
	}
	return nil
}

type importVersionOutput struct{ bytes.Buffer }

func (b *importVersionOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 256 {
		return 0, errors.New("agy: oversized version response")
	}
	return b.Buffer.Write(p)
}
func importBinaryVersion(ctx context.Context, binary string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := aoprocess.CommandContext(ctx, binary, "--version")
	out := &importVersionOutput{}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", errors.New("agy: unable to verify native import CLI version")
	}
	return strings.TrimSpace(out.String()), nil
}

// PrepareImportedSession snapshots just the selected closed conversation DB.
// No settings, keyring state, caches, auth files, or original files are written.
// A separate Gemini root permits the same native ID without two writers sharing
// one conversation. Only the private copy's workspace metadata is changed.
func (p *Plugin) PrepareImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	if err := p.checkImportVersion(ctx); err != nil {
		return ports.NativeSessionRef{}, err
	}
	if !importID.MatchString(r.NativeSessionID) || !filepath.IsAbs(r.DataDir) || !filepath.IsAbs(r.ConfigDir) || !filepath.IsAbs(r.SourceCWD) || r.SourceFingerprint == "" {
		return ports.NativeSessionRef{}, errors.New("agy: incomplete native import selection")
	}
	launch, err := filepath.EvalSymlinks(r.LaunchCWD)
	if err != nil || !filepath.IsAbs(launch) {
		return ports.NativeSessionRef{}, errors.New("agy: import workspace must exist and be absolute")
	}
	proof := agyImportProof{ID: r.NativeSessionID, SourceRoot: r.ConfigDir, SourcePath: r.TranscriptPath, CWD: launch, Fingerprint: r.SourceFingerprint, Version: supportedImportVersion}
	encoded, err := json.Marshal(proof)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	hash := sha256.Sum256(encoded)
	parent := filepath.Join(r.DataDir, "native-imports", "agy")
	dest := filepath.Join(parent, hex.EncodeToString(hash[:16]))
	mode := CLIStorageDir
	if r.Chat {
		mode = ACPStorageDir
	}
	ref := ports.NativeSessionRef{NativeSessionID: r.NativeSessionID, ConfigDir: dest, TranscriptPath: filepath.Join(dest, mode, "conversations", r.NativeSessionID+".db")}
	if info, err := os.Lstat(dest); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ports.NativeSessionRef{}, errors.New("agy: private import root is not a directory")
		}
		if err := validatePreparedImport(ctx, ref, launch, &proof); err != nil {
			return ports.NativeSessionRef{}, err
		}
		return ref, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return ports.NativeSessionRef{}, err
	}
	archive, err := ReadImportArchive(ctx, r.ConfigDir, r.TranscriptPath)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	if archive.ID != r.NativeSessionID || archive.CWD != filepath.Clean(r.SourceCWD) || archive.Fingerprint != r.SourceFingerprint {
		return ports.NativeSessionRef{}, errors.New("agy: selected source changed; scan again before importing")
	}
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return ports.NativeSessionRef{}, err
	}
	stage, err := os.MkdirTemp(parent, ".agy-")
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	staged := filepath.Join(stage, mode, "conversations", r.NativeSessionID+".db")
	if err := os.MkdirAll(filepath.Dir(staged), 0o700); err != nil {
		return ports.NativeSessionRef{}, err
	}
	root, rel, err := importPath(r.ConfigDir, r.TranscriptPath)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	provider, err := os.OpenRoot(root)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	defer func() { _ = provider.Close() }()
	input, err := provider.Open(rel)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	defer func() { _ = input.Close() }()
	output, err := os.OpenFile(staged, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	buffer := make([]byte, 64<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			_ = output.Close()
			return ports.NativeSessionRef{}, err
		}
		n, readErr := input.Read(buffer)
		total += int64(n)
		if total > maxImportDB {
			_ = output.Close()
			return ports.NativeSessionRef{}, errors.New("agy: source exceeds snapshot budget")
		}
		if _, err := output.Write(buffer[:n]); err != nil {
			_ = output.Close()
			return ports.NativeSessionRef{}, err
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			_ = output.Close()
			return ports.NativeSessionRef{}, readErr
		}
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		return ports.NativeSessionRef{}, err
	}
	if err := output.Close(); err != nil {
		return ports.NativeSessionRef{}, err
	}
	// Both the frozen bytes and the original must still be the selected snapshot.
	for _, input := range []struct{ root, path string }{{stage, staged}, {r.ConfigDir, r.TranscriptPath}} {
		fingerprint, err := ImportFingerprint(ctx, input.root, input.path)
		if err != nil {
			return ports.NativeSessionRef{}, err
		}
		if fingerprint != r.SourceFingerprint {
			return ports.NativeSessionRef{}, errors.New("agy: source changed while copying; scan again")
		}
	}
	if err := remapImportDB(ctx, staged, launch); err != nil {
		return ports.NativeSessionRef{}, err
	}
	if err := os.WriteFile(filepath.Join(stage, importProofName), encoded, 0o600); err != nil {
		return ports.NativeSessionRef{}, err
	}
	if err := validatePreparedImport(ctx, ports.NativeSessionRef{NativeSessionID: ref.NativeSessionID, ConfigDir: stage, TranscriptPath: staged}, launch, &proof); err != nil {
		return ports.NativeSessionRef{}, err
	}
	if err := os.Rename(stage, dest); err != nil {
		return ports.NativeSessionRef{}, err
	}
	return ref, nil
}

func remapImportDB(ctx context.Context, path, cwd string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	// Switch the PRIVATE copy to rollback journaling, so atomic publication
	// includes all metadata bytes and does not depend on a staged WAL filename.
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=DELETE"); err != nil {
		return err
	}
	var data []byte
	if err := db.QueryRowContext(ctx, "SELECT data FROM trajectory_metadata_blob WHERE id='main'").Scan(&data); err != nil {
		return err
	}
	data, err = rewriteImportWorkspace(data, cwd)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "UPDATE trajectory_metadata_blob SET data=? WHERE id='main'", data)
	return err
}

func validatePreparedImport(ctx context.Context, ref ports.NativeSessionRef, cwd string, expected *agyImportProof) error {
	root, err := os.OpenRoot(ref.ConfigDir)
	if err != nil {
		return importUnavailable(err)
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(importProofName)
	if err != nil {
		return importUnavailable(err)
	}
	if !info.Mode().IsRegular() || info.Size() > 8192 {
		return errors.New("agy: invalid import preparation proof")
	}
	data, err := root.ReadFile(importProofName)
	if err != nil {
		return importUnavailable(err)
	}
	var proof agyImportProof
	if err := json.Unmarshal(data, &proof); err != nil {
		return importUnavailable(err)
	}
	if proof.ID != ref.NativeSessionID || proof.CWD != cwd || proof.Version != supportedImportVersion || proof.Fingerprint == "" || (expected != nil && proof != *expected) {
		return errors.New("agy: prepared native identity/workspace proof mismatch")
	}
	if source, err := os.Stat(proof.SourcePath); err == nil {
		if target, err := os.Stat(ref.TranscriptPath); err == nil && os.SameFile(source, target) {
			return errors.New("agy: prepared history must not share the source inode")
		}
	}
	a, err := readImportArchive(ctx, ref.ConfigDir, ref.TranscriptPath, false)
	if err != nil {
		return importUnavailable(err)
	}
	if a.ID != ref.NativeSessionID || a.CWD != cwd {
		return errors.New("agy: prepared native history is bound to another identity or workspace")
	}
	return nil
}

// LocateTranscript validates the selected native file instead of accepting a
// filename alone as evidence that Agy can restore a real conversation.
func (*Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if !importID.MatchString(ref.NativeSessionID) || ref.ConfigDir == "" {
		return "", false, nil
	}
	paths := []string{ref.TranscriptPath}
	if ref.TranscriptPath == "" {
		paths = []string{filepath.Join(ref.ConfigDir, ACPStorageDir, "conversations", ref.NativeSessionID+".db"), filepath.Join(ref.ConfigDir, CLIStorageDir, "conversations", ref.NativeSessionID+".db")}
	}
	for _, path := range paths {
		a, err := ReadImportArchive(ctx, ref.ConfigDir, path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", false, err
		}
		if a.ID != ref.NativeSessionID {
			return "", false, errors.New("agy: native ID mismatch")
		}
		return path, true, nil
	}
	return "", false, nil
}

// ProbeNativeSession checks native history without launching the provider.
func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	if ref.ConfigDir == "" {
		return ports.NativeSessionAvailabilityUnknown, nil
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

// ValidateImportedACP fences every private-profile ACP reopen, including a
// missing proof/database and an upgrade to an unverified native binary. Normal
// Agy launches outside this adapter's private import directory are unaffected.
func ValidateImportedACP(ctx context.Context, dataDir, root, id, cwd, binary string) error {
	if dataDir == "" {
		return nil
	}
	base := filepath.Join(dataDir, "native-imports", "agy")
	if !strings.EqualFold(filepath.VolumeName(base), filepath.VolumeName(root)) {
		return nil
	}
	rel, err := filepath.Rel(base, root)
	if err != nil {
		return err
	}
	if !filepath.IsLocal(rel) || filepath.Dir(rel) != "." {
		return nil
	}
	if binary == "" {
		return errors.New("agy: imported history requires the verified agy acp CLI server")
	}
	version, err := importBinaryVersion(ctx, binary)
	if err != nil {
		return err
	}
	if version != supportedImportVersion {
		return errors.New("agy: unsupported CLI version for imported restore")
	}
	launch, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return err
	}
	return validatePreparedImport(ctx, ports.NativeSessionRef{NativeSessionID: id, ConfigDir: root, TranscriptPath: filepath.Join(root, ACPStorageDir, "conversations", id+".db")}, launch, nil)
}

// PrepareRuntimeLaunch verifies an imported binding before terminal startup.
func (p *Plugin) PrepareRuntimeLaunch(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if cfg.ImportConfigDir == "" {
		return nil
	}
	if err := p.checkImportVersion(ctx); err != nil {
		return err
	}
	root, err := GeminiHome(cfg.Env)
	if err != nil {
		return err
	}
	if filepath.Clean(root) != filepath.Clean(cfg.ImportConfigDir) {
		return errors.New("agy: imported launch profile mismatch")
	}
	launch, err := filepath.EvalSymlinks(cfg.WorkspacePath)
	if err != nil {
		return err
	}
	path, found, err := p.LocateTranscript(ctx, ports.NativeSessionRef{NativeSessionID: cfg.NativeSessionID, ConfigDir: root})
	if err != nil {
		return err
	}
	if !found {
		return ports.ErrNativeHistoryUnavailable
	}
	return validatePreparedImport(ctx, ports.NativeSessionRef{NativeSessionID: cfg.NativeSessionID, ConfigDir: root, TranscriptPath: path}, launch, nil)
}
