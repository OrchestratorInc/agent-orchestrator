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

// This is the CLI/schema pair whose metadata-only preparation was verified
// with actual run_command output. Reopens validate prepared native state so a
// later CLI update does not lock users out of their already imported sessions.
const supportedImportVersion = "1.5.13"
const importProofName = ".ao-import.json"

var errImportSelectionChanged = errors.New("agy: selected source changed after import; terminate this import, then import the conversation again")

var _ ports.AgentImportedSessionPreparer = (*Plugin)(nil)
var _ ports.AgentNativeSessionProber = (*Plugin)(nil)
var _ ports.AgentTranscriptLocator = (*Plugin)(nil)

type agyImportProof struct {
	ID, SourceRoot, SourcePath, CWD, Fingerprint string
	Version                                      string
	Meta                                         bool `json:",omitempty"`
	Brain                                        bool `json:",omitempty"`
}

var _ ports.AgentImportVersionChecker = (*Plugin)(nil)

// CheckImportVersion refuses new imports on an unverified Agy CLI version.
func (p *Plugin) CheckImportVersion(ctx context.Context) error {
	return p.checkImportVersion(ctx)
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
	cmd.WaitDelay = time.Second
	out := &importVersionOutput{}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", errors.New("agy: unable to verify native import CLI version")
	}
	return strings.TrimSpace(out.String()), nil
}

// PrepareImportedSession snapshots the selected closed conversation and assets.
// No settings, keyring state, caches, auth files, or original files are written.
// A separate Gemini root permits the same native ID without two writers sharing
// one conversation. Only the private copy's workspace metadata is changed.
func (p *Plugin) PrepareImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	if !importID.MatchString(r.NativeSessionID) || !filepath.IsAbs(r.DataDir) || !filepath.IsAbs(r.ConfigDir) || !filepath.IsAbs(r.SourceCWD) || r.SourceFingerprint == "" {
		return ports.NativeSessionRef{}, errors.New("agy: incomplete native import selection")
	}
	launch, err := filepath.EvalSymlinks(r.LaunchCWD)
	if err != nil || !filepath.IsAbs(launch) {
		return ports.NativeSessionRef{}, errors.New("agy: import workspace must exist and be absolute")
	}
	proof := agyImportProof{ID: r.NativeSessionID, SourceRoot: r.ConfigDir, SourcePath: r.TranscriptPath, CWD: launch, Fingerprint: r.SourceFingerprint, Version: supportedImportVersion}
	parent := filepath.Join(r.DataDir, "native-imports", "agy")
	mode := CLIStorageDir
	if r.Chat {
		mode = ACPStorageDir
	}
	// The receipt binds whether optional native assets existed. Try each proven
	// shape without consulting the source or installed binary. Zero flags retain
	// the root names and receipts of imports prepared before asset snapshots.
	for _, flags := range [][2]bool{{false, false}, {true, false}, {false, true}, {true, true}} {
		candidate := proof
		candidate.Meta, candidate.Brain = flags[0], flags[1]
		ref, _, err := preparedImportRef(r.DataDir, mode, candidate)
		if err != nil {
			return ports.NativeSessionRef{}, err
		}
		if info, err := os.Lstat(ref.ConfigDir); err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return ports.NativeSessionRef{}, errors.New("agy: private import root is not a directory")
			}
			if err := validatePreparedImport(ctx, ref, launch, &candidate); err != nil {
				return ports.NativeSessionRef{}, err
			}
			return ref, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return ports.NativeSessionRef{}, err
		}
	}
	if err := p.checkImportVersion(ctx); err != nil {
		return ports.NativeSessionRef{}, err
	}
	archive, err := ReadImportArchive(ctx, r.ConfigDir, r.TranscriptPath)
	if err != nil {
		if errors.Is(err, errImportSourceChanged) {
			return ports.NativeSessionRef{}, errImportSelectionChanged
		}
		return ports.NativeSessionRef{}, err
	}
	if archive.ID != r.NativeSessionID || archive.CWD != filepath.Clean(r.SourceCWD) || archive.Fingerprint != r.SourceFingerprint {
		return ports.NativeSessionRef{}, errImportSelectionChanged
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
	sourceMode := strings.Split(filepath.ToSlash(rel), "/")[0]
	assets, err := readImportAssets(ctx, provider, sourceMode, r.NativeSessionID, true)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	if err := writeImportAssets(stage, mode, assets); err != nil {
		return ports.NativeSessionRef{}, err
	}
	_, proof.Meta = assets[filepath.Join("conversations", r.NativeSessionID+".meta")]
	_, proof.Brain = assets[filepath.Join("brain", r.NativeSessionID)]
	ref, encoded, err := preparedImportRef(r.DataDir, mode, proof)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
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
			return ports.NativeSessionRef{}, errImportSelectionChanged
		}
	}
	if err := remapImportDB(ctx, staged, launch); err != nil {
		return ports.NativeSessionRef{}, err
	}
	after, err := readImportAssets(ctx, provider, sourceMode, r.NativeSessionID, true)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	if !equalImportAssets(assets, after) {
		return ports.NativeSessionRef{}, errImportSelectionChanged
	}
	if err := os.WriteFile(filepath.Join(stage, importProofName), encoded, 0o600); err != nil {
		return ports.NativeSessionRef{}, err
	}
	if err := validateStagedImport(ctx, ports.NativeSessionRef{NativeSessionID: ref.NativeSessionID, ConfigDir: stage, TranscriptPath: staged}, launch, proof); err != nil {
		return ports.NativeSessionRef{}, err
	}
	if err := os.Rename(stage, ref.ConfigDir); err != nil {
		return ports.NativeSessionRef{}, err
	}
	return ref, nil
}

func preparedImportRef(dataDir, mode string, proof agyImportProof) (ports.NativeSessionRef, []byte, error) {
	encoded, err := json.Marshal(proof)
	if err != nil {
		return ports.NativeSessionRef{}, nil, err
	}
	hash := sha256.Sum256(encoded)
	dest := filepath.Join(dataDir, "native-imports", "agy", hex.EncodeToString(hash[:16]))
	return ports.NativeSessionRef{NativeSessionID: proof.ID, ConfigDir: dest, TranscriptPath: filepath.Join(dest, mode, "conversations", proof.ID+".db")}, encoded, nil
}

func remapImportDB(ctx context.Context, path, cwd string) error {
	db, err := sql.Open("sqlite", importFileURI(path)+"?mode=rw")
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

func readImportProof(configDir string) (agyImportProof, error) {
	var proof agyImportProof
	root, err := os.OpenRoot(configDir)
	if err != nil {
		return proof, importUnavailable(err)
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(importProofName)
	if err != nil {
		return proof, importUnavailable(err)
	}
	if !info.Mode().IsRegular() || info.Size() > 8192 {
		return proof, errors.New("agy: invalid import preparation proof")
	}
	data, err := root.ReadFile(importProofName)
	if err != nil {
		return proof, importUnavailable(err)
	}
	if err := json.Unmarshal(data, &proof); err != nil {
		return proof, importUnavailable(err)
	}
	return proof, nil
}

func preparedImportNamespace(root string) bool {
	return filepath.Base(filepath.Dir(root)) == "agy" && filepath.Base(filepath.Dir(filepath.Dir(root))) == "native-imports"
}

func preparedImportRootMatches(root string, proof agyImportProof) bool {
	if !preparedImportNamespace(root) {
		return false
	}
	encoded, err := json.Marshal(proof)
	if err != nil {
		return false
	}
	hash := sha256.Sum256(encoded)
	return filepath.Base(root) == hex.EncodeToString(hash[:16])
}

func validatePreparedImport(ctx context.Context, ref ports.NativeSessionRef, cwd string, expected *agyImportProof) error {
	proof, err := readImportProof(ref.ConfigDir)
	if err != nil {
		return err
	}
	if expected == nil && !preparedImportRootMatches(ref.ConfigDir, proof) {
		return errors.New("agy: native preparation proof is not bound to this private root")
	}
	// Any recorded version is accepted: the pin gates new copies only, and a
	// prepared copy must stay openable after the pin moves or Agy updates.
	if proof.ID != ref.NativeSessionID || proof.CWD != cwd || proof.Version == "" || proof.Fingerprint == "" || (expected != nil && proof != *expected) {
		return errors.New("agy: prepared native identity/workspace proof mismatch")
	}
	if err := validatePreparedImportAssets(ctx, ref, proof); err != nil {
		return err
	}
	if source, err := os.Stat(proof.SourcePath); err == nil {
		if target, err := os.Stat(ref.TranscriptPath); err == nil && os.SameFile(source, target) {
			return errors.New("agy: prepared history must not share the source inode")
		}
	}
	id, workspace, err := readPreparedIdentity(ctx, ref.ConfigDir, ref.TranscriptPath)
	if err != nil {
		return importUnavailable(err)
	}
	if id != ref.NativeSessionID || workspace != cwd {
		return errors.New("agy: prepared native history is bound to another identity or workspace")
	}
	return nil
}

// validateStagedImport fully audits a freshly remapped copy before it is
// published; reopen later uses the lighter validatePreparedImport only.
func validateStagedImport(ctx context.Context, ref ports.NativeSessionRef, cwd string, proof agyImportProof) error {
	if err := validatePreparedImport(ctx, ref, cwd, &proof); err != nil {
		return err
	}
	a, err := readImportArchive(ctx, ref.ConfigDir, ref.TranscriptPath, false)
	if err != nil {
		return importUnavailable(err)
	}
	if a.ID != ref.NativeSessionID || a.CWD != cwd {
		return errors.New("agy: staged native history is bound to another identity or workspace")
	}
	return nil
}

// LocateTranscript validates the selected native file instead of accepting a
// filename alone as evidence that Agy can restore a real conversation.
func (*Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if !importID.MatchString(ref.NativeSessionID) || ref.ConfigDir == "" {
		return "", false, nil
	}
	proof, proofErr := readImportProof(ref.ConfigDir)
	prepared := proofErr == nil
	if proofErr != nil && (preparedImportNamespace(ref.ConfigDir) || !errors.Is(proofErr, os.ErrNotExist)) {
		return "", false, proofErr
	}
	if prepared && (!preparedImportRootMatches(ref.ConfigDir, proof) || proof.ID != ref.NativeSessionID || proof.Version == "" || !filepath.IsAbs(proof.CWD) || proof.Fingerprint == "") {
		return "", false, errors.New("agy: invalid private native history proof")
	}
	paths := []string{ref.TranscriptPath}
	if ref.TranscriptPath == "" {
		paths = []string{filepath.Join(ref.ConfigDir, ACPStorageDir, "conversations", ref.NativeSessionID+".db"), filepath.Join(ref.ConfigDir, CLIStorageDir, "conversations", ref.NativeSessionID+".db")}
	}
	for _, path := range paths {
		var a ImportArchive
		var err error
		if prepared {
			if err := validatePreparedImportAssets(ctx, ports.NativeSessionRef{NativeSessionID: ref.NativeSessionID, ConfigDir: ref.ConfigDir, TranscriptPath: path}, proof); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return "", false, err
			}
			a.ID, a.CWD, err = readPreparedIdentity(ctx, ref.ConfigDir, path)
		} else {
			a, err = ReadImportArchive(ctx, ref.ConfigDir, path)
		}
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", false, err
		}
		if prepared && a.CWD != proof.CWD {
			return "", false, errors.New("agy: private native workspace differs from its preparation proof")
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

// ValidateImportedACP fences private-profile ACP reopens with native-state
// checks. The preparation version pin does not apply to a later CLI update.
// Normal Agy launches outside the private import directory are unaffected.
func ValidateImportedACP(ctx context.Context, root, id, cwd, binary string) error {
	if !preparedImportNamespace(root) {
		return nil
	}
	if binary == "" {
		return errors.New("agy: imported history requires the verified agy acp CLI server")
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
