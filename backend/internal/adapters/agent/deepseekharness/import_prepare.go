package deepseekharness

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const supportedImportVersion = "0.2.0-rc.2"
const importProofName = ".ao-import.json"

var _ ports.AgentImportedSessionPreparer = (*Plugin)(nil)
var _ ports.AgentNativeSessionProber = (*Plugin)(nil)
var _ ports.AgentTranscriptLocator = (*Plugin)(nil)

type importProof struct {
	ID, SourceRoot, SourcePath, CWD, Fingerprint, CredentialsPath, Version string
}

type importVersionOutput struct{ bytes.Buffer }

func (b *importVersionOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 256 {
		return 0, errors.New("deepseek: oversized version response")
	}
	return b.Buffer.Write(p)
}

func importBinaryVersion(ctx context.Context, binary string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := aoprocess.CommandContext(ctx, binary, "--version")
	cmd.WaitDelay = time.Second
	out := &importVersionOutput{}
	cmd.Stdout, cmd.Stderr = out, io.Discard
	if err := cmd.Run(); err != nil {
		return "", errors.New("deepseek: unable to verify native import CLI version")
	}
	return strings.TrimSpace(out.String()), nil
}

var _ ports.AgentImportVersionChecker = (*Plugin)(nil)

// CheckImportVersion refuses new imports on an unverified dsh version.
func (p *Plugin) CheckImportVersion(ctx context.Context) error {
	return p.checkImportVersion(ctx)
}

func (p *Plugin) checkImportVersion(ctx context.Context) error {
	var version string
	var err error
	if p.importVersion != nil {
		version, err = p.importVersion(ctx)
	} else {
		binary, resolveErr := p.deepseekBinary(ctx)
		if resolveErr != nil {
			return resolveErr
		}
		version, err = importBinaryVersion(ctx, binary)
	}
	if err != nil {
		return err
	}
	if version != supportedImportVersion {
		return errors.New("deepseek: native import requires verified dsh version 0.2.0-rc.2")
	}
	return nil
}

// IsPreparedImportHome recognizes the reserved path namespace. Callers must
// still validate its proof, native identity, workspace and history before use.
func IsPreparedImportHome(root string) bool {
	parent := filepath.Dir(filepath.Clean(root))
	return filepath.IsAbs(root) && filepath.Base(parent) == adapterID && filepath.Base(filepath.Dir(parent)) == "native-imports"
}

func privateImportPath(root, cwd, id string) string {
	return filepath.Join(root, "sessions", importProjectKey(cwd), id, "session.v4.jsonl")
}

func importProofBytes(p importProof) ([]byte, string, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, "", err
	}
	identity := p
	identity.CredentialsPath = ""
	key, err := json.Marshal(identity)
	if err != nil {
		return nil, "", err
	}
	hash := sha256.Sum256(key)
	return b, hex.EncodeToString(hash[:16]), nil
}

func readImportProof(root string) (importProof, error) {
	var p importProof
	r, err := os.OpenRoot(root)
	if err != nil {
		return p, err
	}
	defer func() { _ = r.Close() }()
	info, err := r.Lstat(importProofName)
	if err != nil {
		return p, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16<<10 {
		return p, errors.New("deepseek: invalid private import proof")
	}
	f, err := r.Open(importProofName)
	if err != nil {
		return p, err
	}
	defer func() { _ = f.Close() }()
	err = json.NewDecoder(io.LimitReader(f, (16<<10)+1)).Decode(&p)
	return p, err
}

func validatePrivateImport(ctx context.Context, root, id, cwd string) (string, error) {
	if !IsPreparedImportHome(root) || !importID.MatchString(id) {
		return "", ports.ErrNativeHistoryUnavailable
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", ports.ErrNativeHistoryUnavailable
	}
	p, err := readImportProof(root)
	if err != nil {
		return "", err
	}
	_, name, err := importProofBytes(p)
	if err != nil {
		return "", err
	}
	launch, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", err
	}
	if p.ID != id || p.CWD != launch || p.Version != supportedImportVersion || p.Fingerprint == "" || filepath.Base(root) != name {
		return "", ports.ErrNativeHistoryUnavailable
	}
	if err := checkImportCredentialFormat(p.CredentialsPath); err != nil {
		return "", err
	}
	path := privateImportPath(root, launch, id)
	if err := validatePrivateNativeLog(ctx, root, path, id, launch); err != nil {
		return "", err
	}
	for _, profile := range []string{"acp", headlessProfile} {
		path := filepath.Join("profiles", profile, "cordis.patch.yml")
		want, err := privateImportPatch(root, p.CredentialsPath)
		if err != nil {
			return "", err
		}
		got, err := readPrivateImportFile(root, path)
		if err != nil {
			return "", err
		}
		if !bytes.Equal(got, want) {
			return "", errors.New("deepseek: private import profile changed; refusing native resume")
		}
	}
	return path, nil
}

func readPrivateImportFile(root, path string) ([]byte, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	info, err := r.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return nil, errors.New("deepseek: invalid private profile file")
	}
	f, err := r.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, (64<<10)+1))
}

// Older credential layouts are migrated by native boot. Refuse them before
// launch so reading the existing store cannot modify the user's configuration.
// Parse errors deliberately omit YAML diagnostics that could contain secrets.
func checkImportCredentialFormat(path string) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil // inherited provider environment may supply the key
	}
	if err != nil {
		return errors.New("deepseek: existing credential store is unreadable")
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(b) > 1<<20 {
		return errors.New("deepseek: existing credential store is unreadable or oversized")
	}
	var doc struct {
		Version int `yaml:"version"`
	}
	if yaml.Unmarshal(b, &doc) != nil || doc.Version != 1 {
		return errors.New("deepseek: native credential migration would modify the original store; update it outside this import first")
	}
	return nil
}

// privateImportPatch contains only paths and policy. Native credentials are
// resolved by the installed provider from its original store, never copied.
// Account/OAuth adapters are disabled: this API-key path must not refresh or
// mutate an original credential record. ACP/headless expose no key-save UI.
func privateImportPatch(root, credentials string) ([]byte, error) {
	return json.Marshal([]map[string]any{
		{"id": "credentials", "config": map[string]any{"path": credentials, "watch": false}},
		{"id": "deepseek-account", "disabled": true},
		{"id": "llm-deepseek-account", "disabled": true},
		{"id": "session-persistence-jsonl", "config": map[string]any{"root": filepath.Join(root, "sessions"), "compression": "none"}},
	})
}

// PrepareImportedSession preserves the UUID and every native event. Only the
// PRIVATE V4 header cwd is remapped; neither the source nor credentials/config
// are copied or modified. Shipped profiles use the already installed bundles.
func (p *Plugin) PrepareImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	if !importID.MatchString(r.NativeSessionID) || !filepath.IsAbs(r.ConfigDir) || !filepath.IsAbs(r.SourceCWD) || !filepath.IsAbs(r.DataDir) || r.SourceFingerprint == "" {
		return ports.NativeSessionRef{}, errors.New("deepseek: incomplete native import selection")
	}
	launch, err := filepath.EvalSymlinks(r.LaunchCWD)
	if err != nil || !filepath.IsAbs(launch) {
		return ports.NativeSessionRef{}, errors.New("deepseek: import workspace must exist and be absolute")
	}
	credentials, present := r.Env[credentialsPathEnv]
	if !present {
		credentials = os.Getenv(credentialsPathEnv)
	}
	if strings.TrimSpace(credentials) == "" {
		credentials = filepath.Join(r.ConfigDir, ".credentials.yaml")
	}
	credentials, err = filepath.Abs(credentials)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	if err := checkImportCredentialFormat(credentials); err != nil {
		return ports.NativeSessionRef{}, err
	}
	proof := importProof{ID: r.NativeSessionID, SourceRoot: r.ConfigDir, SourcePath: r.TranscriptPath, CWD: launch, Fingerprint: r.SourceFingerprint, CredentialsPath: credentials, Version: supportedImportVersion}
	encoded, name, err := importProofBytes(proof)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	parent := filepath.Join(r.DataDir, "native-imports", adapterID)
	dest := filepath.Join(parent, name)
	ref := ports.NativeSessionRef{NativeSessionID: r.NativeSessionID, ConfigDir: dest, TranscriptPath: privateImportPath(dest, launch, r.NativeSessionID)}
	if _, err := os.Lstat(dest); err == nil {
		existing, err := readImportProof(dest)
		if err != nil {
			return ports.NativeSessionRef{}, err
		}
		if existing.CredentialsPath != credentials {
			return ports.NativeSessionRef{}, errors.New("deepseek: prepared history uses a different credential store; refusing to rebuild it")
		}
		_, err = validatePrivateImport(ctx, dest, ref.NativeSessionID, launch)
		return ref, err
	} else if !errors.Is(err, os.ErrNotExist) {
		return ports.NativeSessionRef{}, err
	}
	if err := p.checkImportVersion(ctx); err != nil {
		return ports.NativeSessionRef{}, err
	}
	a, err := ReadImportArchive(ctx, r.ConfigDir, r.TranscriptPath)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	if a.ID != r.NativeSessionID || a.CWD != filepath.Clean(r.SourceCWD) || a.Fingerprint != r.SourceFingerprint {
		return ports.NativeSessionRef{}, errors.New("deepseek: selected source changed; terminate this import, then import again")
	}
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return ports.NativeSessionRef{}, err
	}
	stage, err := os.MkdirTemp(parent, ".deepseek-")
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	a.header["cwd"], err = json.Marshal(launch)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	header, err := json.Marshal(a.header)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	if int64(len(header))+1+int64(len(a.body)) > maxImportDecoded {
		return ports.NativeSessionRef{}, errors.New("deepseek: remapped native history exceeds import budget")
	}
	path := privateImportPath(stage, launch, a.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return ports.NativeSessionRef{}, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	_, writeErr := f.Write(append(append(header, '\n'), a.body...))
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return ports.NativeSessionRef{}, err
	}
	patch, err := privateImportPatch(dest, credentials)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	for _, profile := range []string{"acp", headlessProfile} {
		dir := filepath.Join(stage, "profiles", profile)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return ports.NativeSessionRef{}, err
		}
		if err := os.WriteFile(filepath.Join(dir, "cordis.patch.yml"), patch, 0o600); err != nil {
			return ports.NativeSessionRef{}, err
		}
	}
	if err := os.WriteFile(filepath.Join(stage, importProofName), encoded, 0o600); err != nil {
		return ports.NativeSessionRef{}, err
	}
	after, err := ReadImportArchive(ctx, r.ConfigDir, r.TranscriptPath)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	if after.Fingerprint != r.SourceFingerprint {
		return ports.NativeSessionRef{}, errors.New("deepseek: source changed during preparation")
	}
	if err := validatePrivateNativeLog(ctx, stage, path, a.ID, launch); err != nil {
		return ports.NativeSessionRef{}, err
	}
	if err := os.Rename(stage, dest); err != nil {
		return ports.NativeSessionRef{}, err
	}
	_, err = validatePrivateImport(ctx, dest, ref.NativeSessionID, launch)
	return ref, err
}

// ProbeNativeSession only treats a proof-bound private root as authoritative.
func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	if !IsPreparedImportHome(ref.ConfigDir) {
		return ports.NativeSessionAvailabilityUnknown, nil
	}
	proof, err := readImportProof(ref.ConfigDir)
	if err != nil {
		return ports.NativeSessionAvailabilityUnavailable, err
	}
	_, err = validatePrivateImport(ctx, ref.ConfigDir, ref.NativeSessionID, proof.CWD)
	if err != nil {
		return ports.NativeSessionAvailabilityUnavailable, err
	}
	return ports.NativeSessionAvailabilityAvailable, nil
}

// LocateTranscript returns only the already prepared native copy.
func (p *Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if !IsPreparedImportHome(ref.ConfigDir) {
		return "", false, nil
	}
	proof, err := readImportProof(ref.ConfigDir)
	if err != nil {
		return "", false, err
	}
	path, err := validatePrivateImport(ctx, ref.ConfigDir, ref.NativeSessionID, proof.CWD)
	return path, err == nil, err
}

// PrepareRuntimeLaunch fences imported terminal launches before starting dsh.
func (p *Plugin) PrepareRuntimeLaunch(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if cfg.ImportConfigDir == "" {
		return nil
	}
	if filepath.Clean(cfg.Env["DSH_HOME"]) != filepath.Clean(cfg.ImportConfigDir) {
		return ports.ErrNativeHistoryUnavailable
	}
	_, err := validatePrivateImport(ctx, cfg.ImportConfigDir, cfg.NativeSessionID, cfg.WorkspacePath)
	return err
}

// ValidateImportedACP guards proof-bound private homes on every Chat open.
// A missing ID or store never falls through to session/new.
func ValidateImportedACP(ctx context.Context, root, id, cwd string) error {
	if !IsPreparedImportHome(root) {
		return nil
	}
	if _, err := validatePrivateImport(ctx, root, id, cwd); err != nil {
		return err
	}
	return nil
}
