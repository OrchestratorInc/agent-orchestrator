package devin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/processenv"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const importCLIVersion = "3000.11.205"
const importStoreEnv = "DEVIN_SESSIONS_DIR"
const importProofFile = ".ao-import.json"

var _ ports.AgentImportedSessionPreparer = (*Plugin)(nil)
var _ ports.AgentNativeSessionProber = (*Plugin)(nil)
var _ ports.AgentTranscriptLocator = (*Plugin)(nil)
var _ ports.AgentRuntimeLaunchEnv = (*Plugin)(nil)

type importProof struct{ ID, SourceRoot, SourceCWD, CWD, Fingerprint, Version, OrgID string }

func importProofKey(p importProof) (string, error) {
	p.OrgID = "" // account selection must not create another history on retry
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:16]), nil
}

func preparedImportNamespace(root string) bool {
	return filepath.IsAbs(root) && filepath.Base(filepath.Dir(root)) == "devin" && filepath.Base(filepath.Dir(filepath.Dir(root))) == "native-imports"
}

func importConfig(p importProof) []byte {
	d := map[string]any{"version": 1, "auto_update": false, "session_store": "sqlite", "shell": map[string]bool{"setup_complete": true}, "hooks": map[string]any{}, "read_config_from": map[string]bool{}}
	readFrom := map[string]bool{}
	for _, k := range []string{"agents_standard", "cursor", "windsurf", "claude", "copilot", "codex", "kiro", "opencode", "zed"} {
		readFrom[k] = false
	}
	readFrom["agents_standard"] = true // read the AO worktree's own instructions
	d["read_config_from"] = readFrom
	if p.OrgID != "" {
		d["devin"] = map[string]string{"org_id": p.OrgID}
	}
	b, _ := json.Marshal(d)
	return b
}

func importEnvValue(env map[string]string, key string) string {
	if value, ok := env[key]; ok {
		return value
	}
	return os.Getenv(key)
}

func importOrganization(env map[string]string) (string, error) {
	home := importEnvValue(env, "HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	root := importEnvValue(env, "XDG_CONFIG_HOME")
	if root == "" {
		root = filepath.Join(home, ".config")
	}
	f, err := os.Open(filepath.Join(root, "devin", "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", errors.New("devin: native account selection is unreadable")
	}
	defer func() { _ = f.Close() }()
	var d struct {
		Devin struct {
			OrgID string `json:"org_id"`
		} `json:"devin"`
	}
	if json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&d) != nil {
		return "", errors.New("devin: native account selection is unsupported")
	}
	return d.Devin.OrgID, nil
}

type importVersionBuffer struct{ bytes.Buffer }

func (b *importVersionBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 256 {
		return 0, errors.New("devin: invalid version output")
	}
	return b.Buffer.Write(p)
}

func (p *Plugin) checkImportVersion(ctx context.Context) error {
	if p.importVersion != nil {
		return p.importVersion(ctx)
	}
	bin, err := p.devinBinary(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := aoprocess.CommandContext(ctx, bin, "--version")
	cmd.WaitDelay = time.Second
	b := &importVersionBuffer{}
	cmd.Stdout = b
	cmd.Stderr = io.Discard
	if cmd.Run() != nil { // Provider diagnostics can contain account details.
		return errors.New("devin: unable to verify import CLI version")
	}
	fields := strings.Fields(b.String())
	if len(fields) < 2 || fields[0] != "devin" || fields[1] != importCLIVersion {
		return errors.New("devin: new import requires verified CLI 3000.11.205")
	}
	return nil
}

func (p *Plugin) initializePrivateImport(ctx context.Context, root string, env map[string]string) error {
	if p.importInitialize != nil {
		return p.importInitialize(ctx, root)
	}
	bin, err := p.devinBinary(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	cmd := aoprocess.CommandContext(ctx, bin, "--config", filepath.Join(root, "config", "devin", "config.json"), "list", "--format", "json")
	overrides := maps.Clone(env)
	if overrides == nil {
		overrides = map[string]string{}
	}
	overrides[importStoreEnv] = root
	overrides["DEVIN_SESSION_STORE"] = "sqlite"
	overrides["XDG_CONFIG_HOME"] = filepath.Join(root, "config")
	cmd.Env = processenv.Merge(overrides)
	cmd.Dir = root
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	if cmd.Run() != nil {
		return errors.New("devin: unable to initialize private native store")
	}
	return nil
}

// PrepareImportedSession checkpoints a provider-native copy once. An existing
// private history wins even when the original source is gone or has changed.
func (p *Plugin) PrepareImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	ref := ports.NativeSessionRef{}
	if r.Chat {
		return ref, errors.New("devin: this native import requires Terminal")
	}
	if !importNativeID.MatchString(r.NativeSessionID) || r.SourceFingerprint == "" || !filepath.IsAbs(r.ConfigDir) {
		return ref, errors.New("devin: incomplete native import identity")
	}
	cwd, err := filepath.EvalSymlinks(r.LaunchCWD)
	if err != nil {
		return ref, err
	}
	data, err := filepath.EvalSymlinks(r.DataDir)
	if err != nil {
		return ref, err
	}
	payload := importProof{ID: r.NativeSessionID, SourceRoot: filepath.Clean(r.ConfigDir), SourceCWD: filepath.Clean(r.SourceCWD), CWD: cwd, Fingerprint: r.SourceFingerprint, Version: importCLIVersion}
	key, err := importProofKey(payload)
	if err != nil {
		return ref, err
	}
	parent := filepath.Join(data, "native-imports", "devin")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return ref, err
	}
	for _, part := range []string{filepath.Join(data, "native-imports"), parent} {
		info, err := os.Lstat(part)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ref, errors.New("devin: invalid private import directory")
		}
	}
	dest := filepath.Join(parent, key)
	ref = ports.NativeSessionRef{NativeSessionID: r.NativeSessionID, ConfigDir: dest, TranscriptPath: filepath.Join(dest, "cli", "sessions.db")}
	if _, err := os.Lstat(dest); err == nil {
		return ref, validatePrivateImport(ctx, dest, r.NativeSessionID, cwd)
	} else if !errors.Is(err, os.ErrNotExist) {
		return ref, err
	}
	if err := p.checkImportVersion(ctx); err != nil {
		return ref, err
	}
	source, err := readSourceStore(ctx, r.ConfigDir)
	if err != nil {
		return ref, err
	}
	defer source.Close()
	if source.fingerprint != r.SourceFingerprint {
		return ref, errors.New("devin: selected native history changed; scan again")
	}
	if err := sourceImportSchema(ctx, source.db); err != nil {
		return ref, err
	}
	native, err := loadImportSession(ctx, source.db, r.NativeSessionID)
	if err != nil {
		return ref, err
	}
	if filepath.Clean(native.cwd) != payload.SourceCWD {
		return ref, errors.New("devin: source workspace changed")
	}
	messages, err := importActiveAncestry(ctx, source.db, native)
	if err != nil {
		return ref, err
	}
	if !slices.ContainsFunc(messages, func(m domain.ImportedMessage) bool {
		return m.Role == domain.MessageRoleUser && strings.TrimSpace(m.Text) != ""
	}) {
		return ref, errors.New("devin: native history has no user input")
	}
	payload.OrgID, err = importOrganization(r.Env)
	if err != nil {
		return ref, err
	}
	stage, err := os.MkdirTemp(parent, ".stage-")
	if err != nil {
		return ref, err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := os.MkdirAll(filepath.Join(stage, "config", "devin"), 0o700); err != nil {
		return ref, err
	}
	if err := os.WriteFile(filepath.Join(stage, "config", "devin", "config.json"), importConfig(payload), 0o600); err != nil {
		return ref, err
	}
	if err := p.initializePrivateImport(ctx, stage, r.Env); err != nil {
		return ref, err
	}
	if err := copySelectedImport(ctx, source.db, stage, native, cwd); err != nil {
		return ref, err
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return ref, err
	}
	if err := os.WriteFile(filepath.Join(stage, importProofFile), b, 0o600); err != nil {
		return ref, err
	}
	// Recheck the original after preparing; never publish a mixed selection.
	check, err := readSourceStore(ctx, r.ConfigDir)
	if err != nil {
		return ref, err
	}
	actual := check.fingerprint
	check.Close()
	if actual != payload.Fingerprint {
		return ref, errors.New("devin: source changed while preparing; scan again")
	}
	staged, err := privateImportDB(stage)
	if err != nil {
		return ref, err
	}
	prepared, err := loadImportSession(ctx, staged, r.NativeSessionID)
	if err == nil && prepared.cwd != cwd {
		err = errors.New("devin: staged workspace mismatch")
	}
	if err == nil {
		err = validatePrivateAncestry(ctx, staged, prepared)
	}
	_ = staged.Close()
	if err != nil {
		return ref, err
	}
	if err := os.Rename(stage, dest); err != nil {
		if _, statErr := os.Lstat(dest); statErr == nil {
			return ref, validatePrivateImport(ctx, dest, r.NativeSessionID, cwd)
		}
		return ref, err
	}
	return ref, validatePrivateImport(ctx, dest, r.NativeSessionID, cwd)
}

func privateImportDB(root string) (*sql.DB, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	for _, name := range []string{"cli", "cli/sessions.db"} {
		info, err := r.Lstat(name)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || (name == "cli" && !info.IsDir()) || (name != "cli" && !info.Mode().IsRegular()) {
			return nil, errors.New("devin: invalid private native store")
		}
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(root, "cli", "sessions.db"))}
	db, err := sql.Open("sqlite", u.String()+"?mode=ro&_pragma=busy_timeout(1000)")
	if err == nil {
		db.SetMaxOpenConns(1)
	}
	return db, err
}

func readPrivateImportFile(root, name string) ([]byte, error) {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ports.ErrNativeHistoryUnavailable
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	info, err = r.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, ports.ErrNativeHistoryUnavailable
	}
	f, err := r.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if len(b) > 1<<20 {
		return nil, ports.ErrNativeHistoryUnavailable
	}
	return b, err
}

func readImportProof(root string) (importProof, error) {
	var p importProof
	b, err := readPrivateImportFile(root, importProofFile)
	if err != nil {
		return p, err
	}
	if json.Unmarshal(b, &p) != nil {
		return p, ports.ErrNativeHistoryUnavailable
	}
	return p, nil
}

func validatePrivateImport(ctx context.Context, root, id, cwd string) error {
	if !preparedImportNamespace(root) || !importNativeID.MatchString(id) {
		return ports.ErrNativeHistoryUnavailable
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ports.ErrNativeHistoryUnavailable
	}
	proof, err := readImportProof(root)
	if err != nil {
		return err
	}
	key, err := importProofKey(proof)
	if err != nil {
		return err
	}
	launch, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return err
	}
	if key != filepath.Base(root) || proof.ID != id || proof.CWD != launch || proof.Version != importCLIVersion || proof.Fingerprint == "" {
		return ports.ErrNativeHistoryUnavailable
	}
	config, err := readPrivateImportFile(root, "config/devin/config.json")
	if err != nil {
		return err
	}
	if !bytes.Equal(config, importConfig(proof)) {
		return errors.New("devin: private import configuration changed")
	}
	db, err := privateImportDB(root)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	s, err := loadImportSession(ctx, db, id)
	if err != nil {
		return err
	}
	if s.cwd != launch {
		return errors.New("devin: private import workspace changed")
	}
	return validatePrivateAncestry(ctx, db, s)
}

// Private history may grow past source admission limits. Validate one ancestor
// row at a time without retaining its text or imposing the source total budget.
func validatePrivateAncestry(ctx context.Context, db *sql.DB, s importSession) error {
	seen := map[int64]bool{}
	id := s.head
	user := false
	for id.Valid {
		if id.Int64 < 0 || seen[id.Int64] {
			return errors.New("devin: cyclic private ancestry")
		}
		seen[id.Int64] = true
		var parent sql.NullInt64
		var raw string
		if err := db.QueryRowContext(ctx, `SELECT parent_node_id,chat_message FROM message_nodes WHERE session_id=? AND node_id=?`, s.id, id.Int64).Scan(&parent, &raw); err != nil {
			return ports.ErrNativeHistoryUnavailable
		}
		var m importNativeMessage
		if json.Unmarshal([]byte(raw), &m) != nil || m.ID == "" {
			return errors.New("devin: invalid private native message")
		}
		user = user || (m.Role == "user" && m.Metadata.IsUserInput && strings.TrimSpace(m.Content) != "")
		id = parent
	}
	if !user {
		return ports.ErrNativeHistoryUnavailable
	}
	return ctx.Err()
}

func (p *Plugin) PrepareRuntimeLaunch(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if cfg.ImportConfigDir == "" {
		return nil
	}
	if cfg.Env[importStoreEnv] != cfg.ImportConfigDir {
		return errors.New("devin: private import environment mismatch")
	}
	return validatePrivateImport(ctx, cfg.ImportConfigDir, cfg.NativeSessionID, cfg.WorkspacePath)
}

func (p *Plugin) AugmentRuntimeLaunchEnv(env map[string]string, _ string, _ domain.SessionID, _ string) {
	if root := env[importStoreEnv]; preparedImportNamespace(root) {
		env["DEVIN_SESSION_STORE"] = "sqlite"
		env["XDG_CONFIG_HOME"] = filepath.Join(root, "config")
	}
}

func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	if !preparedImportNamespace(ref.ConfigDir) {
		return ports.NativeSessionAvailabilityUnknown, nil
	}
	proof, err := readImportProof(ref.ConfigDir)
	if err != nil {
		return ports.NativeSessionAvailabilityUnavailable, nil
	}
	if err := validatePrivateImport(ctx, ref.ConfigDir, ref.NativeSessionID, proof.CWD); err != nil {
		return ports.NativeSessionAvailabilityUnavailable, nil
	}
	return ports.NativeSessionAvailabilityAvailable, nil
}

func (p *Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	state, err := p.ProbeNativeSession(ctx, ref)
	if err != nil || state != ports.NativeSessionAvailabilityAvailable {
		return "", false, err
	}
	return filepath.Join(ref.ConfigDir, "cli", "sessions.db"), true, nil
}
