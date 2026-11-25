package codewhale

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/agent/codewhalestore"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

var (
	_ ports.AgentNativeSessionConfigProvider = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber         = (*Plugin)(nil)
	_ ports.AgentTranscriptLocator           = (*Plugin)(nil)
	_ ports.AgentImportedSessionPreparer     = (*Plugin)(nil)
)

// NativeSessionConfigDir selects the native profile, including legacy sessions.
func (*Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	root, err := nativeconfig.Resolve(env, "CODEWHALE_HOME", ".codewhale")
	if err != nil {
		return "", err
	}
	_, explicit := env["CODEWHALE_HOME"]
	if explicit || os.Getenv("CODEWHALE_HOME") != "" {
		return root, nil
	}
	if info, err := os.Stat(filepath.Join(root, "sessions")); err == nil && info.IsDir() {
		return root, nil
	}
	legacy := filepath.Join(filepath.Dir(root), ".deepseek")
	if info, err := os.Stat(filepath.Join(legacy, "sessions")); err == nil && info.IsDir() {
		return legacy, nil
	}
	return root, nil
}

// LocateTranscript validates the exact native snapshot; it never picks a prefix.
func (*Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if ref.ConfigDir == "" || ref.NativeSessionID == "" {
		return "", false, nil
	}
	if !nativeSessionIDPattern.MatchString(ref.NativeSessionID) {
		return "", false, errors.New("invalid codewhale native session id")
	}
	path := filepath.Join(ref.ConfigDir, "sessions", ref.NativeSessionID+".json")
	archive, err := readNativeResumeSnapshot(ctx, ref.ConfigDir, ref.NativeSessionID)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return path, archive.ID == ref.NativeSessionID, nil
}

// ProbeNativeSession checks the resumable native document without invoking CLI.
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

// PrepareImportedSession copies native history under a worktree-specific id,
// removing the old Runtime binding so Codewhale creates authority in that root.
func (*Plugin) PrepareImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	archive, err := codewhalestore.Read(ctx, r.ConfigDir, r.TranscriptPath)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	if archive.ID != r.NativeSessionID {
		return ports.NativeSessionRef{}, errors.New("codewhale source no longer holds the imported conversation")
	}
	prepared, path, err := codewhalestore.Copy(ctx, r.ConfigDir, r.TranscriptPath, r.LaunchCWD)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	return ports.NativeSessionRef{NativeSessionID: prepared.ID, ConfigDir: r.ConfigDir, TranscriptPath: path}, nil
}

func (p *Plugin) nativeRestoreConfigDir(ctx context.Context, cfg ports.WorkspaceHookConfig) (string, error) {
	root, err := p.NativeSessionConfigDir(ctx, cfg.Env)
	if err != nil {
		return "", err
	}
	if root == "~" || strings.HasPrefix(root, "~/") || strings.HasPrefix(root, `~\`) {
		home := strings.TrimSpace(cfg.Env["HOME"])
		if home == "" {
			home, err = os.UserHomeDir()
			if err != nil {
				return "", err
			}
		}
		if root == "~" {
			root = home
		} else {
			root = filepath.Join(home, root[2:])
		}
	}
	if !filepath.IsAbs(root) {
		root = filepath.Join(cfg.WorkspacePath, root)
	}
	return filepath.Abs(root)
}

func (p *Plugin) nativeRestoreRuntimeDir(ctx context.Context, cfg ports.WorkspaceHookConfig, nativeID, recordedRoot, freshDir string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if nativeID == "" {
		if recordedRoot != "" {
			return "", errors.New("codewhale native restore did not identify its conversation")
		}
		return freshDir, nil
	}
	if !nativeSessionIDPattern.MatchString(nativeID) {
		return "", errors.New("codewhale restore has an invalid native identity")
	}
	root, err := p.nativeRestoreConfigDir(ctx, cfg)
	if err != nil {
		return "", err
	}
	if recordedRoot != "" {
		selected, err := filepath.EvalSymlinks(root)
		if err != nil {
			return "", err
		}
		recorded, err := filepath.EvalSymlinks(recordedRoot)
		if err != nil {
			return "", err
		}
		if !sameFilesystemPath(selected, recorded) {
			return "", errors.New("codewhale launch profile differs from the recorded import root")
		}
		root = recordedRoot
	}
	// Native first save drops the copy marker and establishes runtime authority.
	// Later restores validate that binding without source-admission budgets.
	archive, err := readNativeRestoreBinding(ctx, root, nativeID)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if recordedRoot != "" || archive.ImportCopy {
			return "", err
		}
		return freshDir, nil
	}
	if archive.ImportCopy || (recordedRoot != "" && !archive.RuntimeBound) {
		archive, err = codewhalestore.Read(ctx, root, filepath.Join(root, "sessions", nativeID+".json"))
		if err != nil {
			return "", err
		}
	}
	workspace, err := filepath.EvalSymlinks(cfg.WorkspacePath)
	if err != nil {
		return "", err
	}
	savedWorkspace, err := filepath.EvalSymlinks(archive.CWD)
	if err != nil {
		if recordedRoot == "" && !archive.ImportCopy {
			return freshDir, nil
		}
		return "", err
	}
	if archive.ID != nativeID || !sameFilesystemPath(workspace, savedWorkspace) {
		if recordedRoot == "" && !archive.ImportCopy {
			return freshDir, nil
		}
		return "", errors.New("codewhale restore snapshot does not match its selected workspace")
	}
	if !archive.RuntimeBound {
		// Native legacy snapshots may acquire their first runtime on resume.
		// An AO copy still needs its recorded import root and exact proof.
		if recordedRoot == "" && !archive.ImportCopy {
			return freshDir, nil
		}
		if recordedRoot != "" && archive.UnopenedCopy {
			return freshDir, nil
		}
		return "", errors.New("codewhale native restore is missing its saved runtime binding")
	}
	if !filepath.IsAbs(archive.RuntimeDir) || strings.TrimSpace(archive.RuntimeScope) == "" {
		return "", errors.New("codewhale native restore has an incomplete runtime binding")
	}
	runtimeDir, err := filepath.EvalSymlinks(archive.RuntimeDir)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(runtimeDir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || !sameFilesystemPath(runtimeDir, archive.RuntimeDir) {
		return "", errors.New("codewhale saved runtime directory is not its canonical existing root")
	}
	scope, err := codewhaleRuntimeScope(archive.RuntimeDir)
	if err != nil {
		return "", err
	}
	if scope != archive.RuntimeScope {
		return "", errors.New("codewhale saved runtime ownership does not match its binding")
	}
	if override := cfg.Env[codewhaleRuntimeDirEnv]; override != "" {
		resolved, err := filepath.EvalSymlinks(override)
		if err != nil {
			return "", err
		}
		if !sameFilesystemPath(resolved, runtimeDir) {
			return "", errors.New("codewhale runtime override conflicts with the saved binding")
		}
	}
	return archive.RuntimeDir, nil
}

// readNativeResumeSnapshot keeps unbound and still-marked copies subject to
// full copy verification, while native-bound conversations may grow freely.
func readNativeResumeSnapshot(ctx context.Context, root, id string) (codewhalestore.Archive, error) {
	archive, err := readNativeRestoreBinding(ctx, root, id)
	if err != nil {
		return archive, err
	}
	if !archive.RuntimeBound || archive.ImportCopy {
		return codewhalestore.Read(ctx, root, filepath.Join(root, "sessions", id+".json"))
	}
	if archive.ID != id {
		return archive, errors.New("codewhale native identity mismatch")
	}
	return archive, nil
}

// readNativeRestoreBinding streams past history instead of buffering it or
// applying source-admission budgets. Callers retain strict first-copy proof
// verification and validate native identity, workspace and runtime authority.
func readNativeRestoreBinding(ctx context.Context, root, id string) (codewhalestore.Archive, error) {
	var archive codewhalestore.Archive
	profile, err := os.OpenRoot(root)
	if err != nil {
		return archive, err
	}
	defer func() { _ = profile.Close() }()
	path := filepath.Join("sessions", id+".json")
	info, err := profile.Lstat(path)
	if err != nil {
		return archive, err
	}
	if !info.Mode().IsRegular() {
		return archive, errors.New("native CodeWhale snapshot is not a regular file")
	}
	f, err := profile.Open(path)
	if err != nil {
		return archive, err
	}
	defer func() { _ = f.Close() }()
	dec := json.NewDecoder(f)
	var metadataErr error
	first, err := dec.Token()
	if err != nil || first != json.Delim('{') {
		return archive, errors.New("invalid native CodeWhale snapshot")
	}
	for dec.More() {
		if err := ctx.Err(); err != nil {
			return archive, err
		}
		key, err := dec.Token()
		if err != nil {
			return archive, err
		}
		if key == "metadata" {
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return archive, err
			}
			var meta struct {
				ID        string `json:"id"`
				Workspace string `json:"workspace"`
				Runtime   *struct {
					Dir   string `json:"data_dir"`
					Scope string `json:"execution_scope"`
				} `json:"runtime_store"`
			}
			if err := json.Unmarshal(raw, &meta); err != nil {
				// Keep scanning valid JSON so an import marker later in the
				// object cannot be hidden by an unsupported native field type.
				metadataErr = err
				continue
			}
			archive.ID, archive.CWD = meta.ID, meta.Workspace
			if meta.Runtime != nil {
				archive.RuntimeBound, archive.RuntimeDir, archive.RuntimeScope = true, meta.Runtime.Dir, meta.Runtime.Scope
			}
			continue
		}
		if key == "ao_import" {
			archive.ImportCopy = true
		}
		if err := skipNativeRestoreValue(ctx, dec); err != nil {
			return archive, err
		}
	}
	if _, err = dec.Token(); err != nil {
		return archive, err
	}
	if _, err = dec.Token(); !errors.Is(err, io.EOF) {
		return archive, errors.New("invalid trailing native CodeWhale snapshot data")
	}
	return archive, metadataErr
}

func skipNativeRestoreValue(ctx context.Context, dec *json.Decoder) error {
	depth := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		token, err := dec.Token()
		if err != nil {
			return err
		}
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
		if depth == 0 {
			return nil
		}
	}
}

func codewhaleRuntimeScope(runtimeDir string) (string, error) {
	root, err := os.OpenRoot(runtimeDir)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat("owner.json")
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return "", errors.New("codewhale saved runtime owner is not a bounded regular file")
	}
	f, err := root.Open("owner.json")
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	var owner struct {
		ID string `json:"owner_id"`
	}
	if err := json.NewDecoder(io.LimitReader(f, 64<<10)).Decode(&owner); err != nil {
		return "", err
	}
	if owner.ID == "" || strings.IndexFunc(owner.ID, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_'
	}) >= 0 {
		return "", errors.New("codewhale saved runtime has an invalid owner id")
	}
	// Matches runtime_execution_scope(owner_id, root/events.lock) in the
	// provider's RuntimeStoreBinding::validate_existing_store.
	payload := append([]byte(owner.ID), 0)
	payload = append(payload, []byte(filepath.Join(runtimeDir, "events.lock"))...)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}
