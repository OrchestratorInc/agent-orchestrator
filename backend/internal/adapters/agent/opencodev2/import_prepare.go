package opencodev2

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // read-only checks of the source and the copy

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencode"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// An imported OpenCode 2 chat lives in the user's own store, but AO runs
// OpenCode 2 with a private data home (DataHome), so the chat is copied there
// with OpenCode 2's own `session export` and `session import --directory`,
// bound to AO's workspace. AO never writes either database itself, and the
// user's chat is left untouched.
//
// `session import` keeps the exported ids and skips a session id it already
// holds ("Session already exists"), and AO's private store can already hold a
// same-id copy from when it was first set up. So the copy gets fresh ids
// derived from the source id and launch directory: a retry finds and reuses
// the copy an earlier attempt published instead of making a second one.
//
// The source must still match the history the selection saw before export,
// and the copy must match it too, so the copy holds exactly what AO archived.

var _ ports.AgentImportedSessionPreparer = (*Plugin)(nil)

// PrepareImportedSession copies the imported chat into AO's OpenCode 2 store,
// bound to LaunchCWD, and returns the copy's id and store.
func (p *Plugin) PrepareImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	source := strings.TrimSpace(r.NativeSessionID)
	if !filepath.IsAbs(r.LaunchCWD) {
		return ports.NativeSessionRef{}, errors.New("opencode-v2: launch directory must be absolute")
	}
	// OpenCode records the resolved directory (for example /private/tmp on macOS).
	launch, err := filepath.EvalSymlinks(r.LaunchCWD)
	if err != nil {
		return ports.NativeSessionRef{}, fmt.Errorf("opencode-v2: resolve launch directory: %w", err)
	}
	target, err := opencode.DerivedID(source, launch)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	// The source store is <data home>/opencode, as OpenCode resolves it.
	sourceDir := filepath.Clean(strings.TrimSpace(r.ConfigDir))
	if !filepath.IsAbs(sourceDir) || filepath.Base(sourceDir) != "opencode" {
		return ports.NativeSessionRef{}, errors.New("opencode-v2: the imported chat's store is not an OpenCode data folder")
	}
	home, err := DataHome(ctx)
	if err != nil {
		return ports.NativeSessionRef{}, fmt.Errorf("opencode-v2: prepare data home: %w", err)
	}
	targetDir := filepath.Join(home, "opencode")
	if sameDir(sourceDir, targetDir) {
		return ports.NativeSessionRef{}, errors.New("opencode-v2: the chat is already in AO's OpenCode 2 store")
	}
	ref := ports.NativeSessionRef{NativeSessionID: target, ConfigDir: targetDir}

	// Reuse a copy an earlier attempt already published.
	if dir, found, err := v2SessionDirectory(ctx, targetDir, target); err != nil {
		return ports.NativeSessionRef{}, err
	} else if found {
		if !sameDir(dir, launch) {
			return ports.NativeSessionRef{}, errors.New("opencode-v2: the prepared session id is bound to another directory")
		}
		if err := matchV2Fingerprint(ctx, targetDir, target, r.SourceFingerprint); err != nil {
			return ports.NativeSessionRef{}, fmt.Errorf("opencode-v2: the prepared copy does not hold the selected history: %w", err)
		}
		return ref, nil
	}
	if !filepath.IsAbs(strings.TrimSpace(r.DataDir)) {
		return ports.NativeSessionRef{}, errors.New("opencode-v2: AO's data directory is required to stage the session copy")
	}
	if err := matchV2Fingerprint(ctx, sourceDir, source, r.SourceFingerprint); err != nil {
		return ports.NativeSessionRef{}, fmt.Errorf("opencode-v2: the chat changed since it was selected; scan again before importing: %w", err)
	}

	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	exported, err := opencode.RunCommand(ctx, binary, launch, v2CommandEnv(r.Env, filepath.Dir(sourceDir)), "session", "export", source, "--standalone")
	if err != nil {
		return ports.NativeSessionRef{}, fmt.Errorf("opencode-v2: export imported chat: %w", err)
	}
	doc, err := rewriteV2Export(exported, source, target)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	// The export holds the whole chat, so it is staged under AO's data folder,
	// never the OS temporary directory.
	staging := filepath.Join(r.DataDir, "import-staging")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return ports.NativeSessionRef{}, err
	}
	dir, err := os.MkdirTemp(staging, "opencode-v2-")
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	file := filepath.Join(dir, "session.json")
	if err := os.WriteFile(file, doc, 0o600); err != nil {
		return ports.NativeSessionRef{}, err
	}
	if _, err := opencode.RunCommand(ctx, binary, launch, v2CommandEnv(r.Env, home), "session", "import", "--directory", launch, file, "--standalone"); err != nil {
		return ports.NativeSessionRef{}, fmt.Errorf("opencode-v2: import chat copy: %w", err)
	}

	// Trust only what OpenCode stored: the copy must exist, be bound to the
	// launch directory, and hold the selected history.
	copied, found, err := v2SessionDirectory(ctx, targetDir, target)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	if !found {
		return ports.NativeSessionRef{}, errors.New("opencode-v2: the chat copy was not created")
	}
	if !sameDir(copied, launch) {
		return ports.NativeSessionRef{}, errors.New("opencode-v2: the chat copy is not bound to AO's workspace")
	}
	want := r.SourceFingerprint
	if want == "" {
		// Without selection evidence the copy must still match its source.
		if want, err = v2Fingerprint(ctx, sourceDir, source); err != nil {
			return ports.NativeSessionRef{}, err
		}
	}
	if err := matchV2Fingerprint(ctx, targetDir, target, want); err != nil {
		return ports.NativeSessionRef{}, fmt.Errorf("opencode-v2: the chat changed while it was copied; scan again before importing: %w", err)
	}
	return ref, nil
}

// rewriteV2Export gives an exported chat and each of its messages a fresh id.
// Tool call ids belong to the model provider and stay; unknown fields pass
// through.
func rewriteV2Export(data []byte, source, target string) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("opencode-v2: read exported chat: %w", err)
	}
	info, ok := doc["info"].(map[string]any)
	if !ok || info["id"] != source {
		return nil, errors.New("opencode-v2: export does not describe the imported chat")
	}
	messages, ok := doc["messages"].([]any)
	if !ok || len(messages) == 0 {
		return nil, errors.New("opencode-v2: export has no messages")
	}
	info["id"] = target
	for _, raw := range messages {
		message, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("opencode-v2: export has a malformed message")
		}
		old, _ := message["id"].(string)
		fresh, err := opencode.DerivedID(old, target)
		if err != nil {
			return nil, err
		}
		message["id"] = fresh
	}
	return json.Marshal(doc)
}

// v2CommandEnv is the daemon environment with the launch overrides applied (an
// empty value clears a variable) and XDG_DATA_HOME pinned to dataHome, so
// OpenCode opens <dataHome>/opencode.
func v2CommandEnv(overrides map[string]string, dataHome string) []string {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if key, value, ok := strings.Cut(kv, "="); ok {
			env[key] = value
		}
	}
	for key, value := range overrides {
		if value == "" {
			delete(env, key)
		} else {
			env[key] = value
		}
	}
	delete(env, "OPENCODE_DATA_DIR")
	env["XDG_DATA_HOME"] = dataHome
	out := make([]string, 0, len(env))
	for key, value := range env {
		out = append(out, key+"="+value)
	}
	return out
}

// openV2Store opens dir's opencode.db read-only; a missing store returns nil.
func openV2Store(dir string) (*sql.DB, error) {
	path := filepath.Join(dir, "opencode.db")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro&_pragma=busy_timeout(2000)")
}

// v2SessionDirectory returns the directory a chat in dir's store is bound to.
func v2SessionDirectory(ctx context.Context, dir, id string) (string, bool, error) {
	db, err := openV2Store(dir)
	if err != nil || db == nil {
		return "", false, err
	}
	defer func() { _ = db.Close() }()
	if v2, err := opencode.HasV2Store(ctx, db); err != nil || !v2 {
		return "", false, err
	}
	var directory string
	err = db.QueryRowContext(ctx, `SELECT directory FROM session_v2 WHERE id = ?`, id).Scan(&directory)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("opencode-v2: read chat copy: %w", err)
	}
	return directory, true, nil
}

func v2Fingerprint(ctx context.Context, dir, id string) (string, error) {
	db, err := openV2Store(dir)
	if err != nil {
		return "", err
	}
	if db == nil {
		return "", errors.New("opencode-v2: the store is missing")
	}
	defer func() { _ = db.Close() }()
	return opencode.V2SessionFingerprint(ctx, db, id)
}

// matchV2Fingerprint checks a chat against the selection evidence; an import
// without a fingerprint has nothing to check.
func matchV2Fingerprint(ctx context.Context, dir, id, want string) error {
	if want == "" {
		return nil
	}
	got, err := v2Fingerprint(ctx, dir, id)
	if err != nil {
		return err
	}
	if got != want {
		return errors.New("history fingerprint mismatch")
	}
	return nil
}

func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	x, errA := os.Stat(a)
	y, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(x, y)
}
