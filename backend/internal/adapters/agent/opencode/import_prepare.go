package opencode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

var _ ports.AgentImportedSessionPreparer = (*Plugin)(nil)

const maxOpenCodeExportBytes = 1 << 30

const openCodeIDTimeLen = 16

func (p *Plugin) PrepareImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	source := strings.TrimSpace(r.NativeSessionID)
	if !openCodeSessionID.MatchString(source) || len(source) <= openCodeIDTimeLen {
		return ports.NativeSessionRef{}, errors.New("opencode: invalid imported session id")
	}
	if !filepath.IsAbs(r.LaunchCWD) {
		return ports.NativeSessionRef{}, errors.New("opencode: launch directory must be absolute")
	}
	launch, err := filepath.EvalSymlinks(r.LaunchCWD)
	if err != nil {
		return ports.NativeSessionRef{}, fmt.Errorf("opencode: resolve launch directory: %w", err)
	}
	configDir := strings.TrimSpace(r.ConfigDir)
	if configDir == "" {
		if configDir, err = p.NativeSessionConfigDir(ctx, r.Env); err != nil {
			return ports.NativeSessionRef{}, err
		}
	}
	target := derivedOpenCodeID(source, launch)
	ref := ports.NativeSessionRef{NativeSessionID: target, ConfigDir: configDir}

	if existing, found, err := readOpenCodeCopy(ctx, configDir, target); err != nil {
		return ports.NativeSessionRef{}, err
	} else if found {
		if !sameOpenCodeDir(existing.directory, launch) {
			return ports.NativeSessionRef{}, errors.New("opencode: the prepared session id is bound to another directory")
		}
		if existing.messages == 0 {
			return ports.NativeSessionRef{}, errors.New("opencode: the prepared session has no messages")
		}
		if err := matchOpenCodeFingerprint(ctx, configDir, target, r.SourceFingerprint); err != nil {
			return ports.NativeSessionRef{}, fmt.Errorf("opencode: the prepared session copy does not hold the selected history: %w", err)
		}
		return ref, nil
	}
	if strings.TrimSpace(r.DataDir) == "" || !filepath.IsAbs(r.DataDir) {
		return ports.NativeSessionRef{}, errors.New("opencode: AO's data directory is required to stage the session copy")
	}
	if err := matchOpenCodeFingerprint(ctx, configDir, source, r.SourceFingerprint); err != nil {
		return ports.NativeSessionRef{}, fmt.Errorf("opencode: the session changed since it was selected; scan again before importing: %w", err)
	}

	binary := p.importBinary
	if binary == "" {
		binary, err = ResolveBinaryForMajor(ctx, 1)
	}
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	env, err := p.openCodeCommandEnv(ctx, r.Env, configDir)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	exported, err := runOpenCode(ctx, binary, launch, env, "export", "--pure", source)
	if err != nil {
		return ports.NativeSessionRef{}, fmt.Errorf("opencode: export imported session: %w", err)
	}
	doc, messages, err := rewriteOpenCodeExport(exported, source, target, launch)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	if messages == 0 {
		return ports.NativeSessionRef{}, errors.New("opencode: the imported session has no messages")
	}
	staging := filepath.Join(r.DataDir, "import-staging")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return ports.NativeSessionRef{}, err
	}
	dir, err := os.MkdirTemp(staging, "opencode-")
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	file := filepath.Join(dir, "session.json")
	if err := os.WriteFile(file, doc, 0o600); err != nil {
		return ports.NativeSessionRef{}, err
	}
	if _, err := runOpenCode(ctx, binary, launch, env, "import", "--pure", file); err != nil {
		return ports.NativeSessionRef{}, fmt.Errorf("opencode: import session copy: %w", err)
	}

	copied, found, err := readOpenCodeCopy(ctx, configDir, target)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	if !found {
		return ports.NativeSessionRef{}, errors.New("opencode: the session copy was not created")
	}
	if !sameOpenCodeDir(copied.directory, launch) {
		return ports.NativeSessionRef{}, errors.New("opencode: the session copy is not bound to AO's workspace")
	}
	if copied.messages != messages {
		return ports.NativeSessionRef{}, fmt.Errorf("opencode: the session copy holds %d of %d messages", copied.messages, messages)
	}
	if err := matchOpenCodeFingerprint(ctx, configDir, target, r.SourceFingerprint); err != nil {
		return ports.NativeSessionRef{}, fmt.Errorf("opencode: the session changed while it was copied; scan again before importing: %w", err)
	}
	return ref, nil
}

func matchOpenCodeFingerprint(ctx context.Context, configDir, id, want string) error {
	if want == "" {
		return nil
	}
	got, err := openCodeSessionFingerprint(ctx, configDir, id)
	if err != nil {
		return err
	}
	if got != want {
		return errors.New("history fingerprint mismatch")
	}
	return nil
}

func derivedOpenCodeID(old, salt string) string {
	sum := sha256.Sum256([]byte(salt + "\x00" + old))
	tail := new(big.Int).SetBytes(sum[:]).Text(62)
	for len(tail) < len(old)-openCodeIDTimeLen {
		tail = "0" + tail
	}
	return old[:openCodeIDTimeLen] + tail[:len(old)-openCodeIDTimeLen]
}

func rewriteOpenCodeExport(data []byte, source, target, launch string) ([]byte, int, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, 0, fmt.Errorf("opencode: read exported session: %w", err)
	}
	info, ok := doc["info"].(map[string]any)
	if !ok || info["id"] != source {
		return nil, 0, errors.New("opencode: export does not describe the imported session")
	}
	messages, ok := doc["messages"].([]any)
	if !ok {
		return nil, 0, errors.New("opencode: export has no message list")
	}
	var idErr error
	fresh := func(value any) any {
		old, ok := value.(string)
		if !ok || old == "" {
			return value
		}
		if len(old) <= openCodeIDTimeLen {
			idErr = fmt.Errorf("opencode: unexpected id %q in export", old)
			return value
		}
		return derivedOpenCodeID(old, target)
	}
	oldDir, _ := info["directory"].(string)
	info["id"] = target
	info["directory"] = launch
	if revert, ok := info["revert"].(map[string]any); ok {
		for _, key := range []string{"messageID", "partID"} {
			if _, present := revert[key]; present {
				revert[key] = fresh(revert[key])
			}
		}
	}
	for _, raw := range messages {
		message, ok := raw.(map[string]any)
		if !ok {
			return nil, 0, errors.New("opencode: export has a malformed message")
		}
		mInfo, ok := message["info"].(map[string]any)
		if !ok {
			return nil, 0, errors.New("opencode: export has a message without info")
		}
		mInfo["id"] = fresh(mInfo["id"])
		mInfo["sessionID"] = target
		if _, present := mInfo["parentID"]; present {
			mInfo["parentID"] = fresh(mInfo["parentID"])
		}
		if path, ok := mInfo["path"].(map[string]any); ok && oldDir != "" {
			for _, key := range []string{"cwd", "root"} {
				if path[key] == oldDir {
					path[key] = launch
				}
			}
		}
		parts, _ := message["parts"].([]any)
		for _, rawPart := range parts {
			part, ok := rawPart.(map[string]any)
			if !ok {
				return nil, 0, errors.New("opencode: export has a malformed message part")
			}
			part["id"] = fresh(part["id"])
			part["sessionID"] = target
			if _, present := part["messageID"]; present {
				part["messageID"] = fresh(part["messageID"])
			}
		}
	}
	if idErr != nil {
		return nil, 0, idErr
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, 0, err
	}
	return out, len(messages), nil
}

type openCodeCopy struct {
	directory string
	messages  int
}

func readOpenCodeCopy(ctx context.Context, configDir, id string) (openCodeCopy, bool, error) {
	db, err := openOpenCodeStore(configDir)
	if err != nil || db == nil {
		return openCodeCopy{}, false, err
	}
	defer func() { _ = db.Close() }()
	var copied openCodeCopy
	err = db.QueryRowContext(ctx, `
		SELECT directory, (SELECT COUNT(*) FROM message m WHERE m.session_id = s.id)
		FROM session s WHERE s.id = ?`, id).Scan(&copied.directory, &copied.messages)
	if errors.Is(err, sql.ErrNoRows) {
		return openCodeCopy{}, false, nil
	}
	if err != nil {
		return openCodeCopy{}, false, fmt.Errorf("opencode: read session copy: %w", err)
	}
	return copied, true, nil
}

func openOpenCodeStore(configDir string) (*sql.DB, error) {
	dbPath := filepath.Join(configDir, "opencode.db")
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return sql.Open("sqlite", ReadOnlyDSN(dbPath, 2000))
}

func sameOpenCodeDir(stored, launch string) bool {
	if filepath.Clean(stored) == filepath.Clean(launch) {
		return true
	}
	a, errA := os.Stat(stored)
	b, errB := os.Stat(launch)
	return errA == nil && errB == nil && os.SameFile(a, b)
}

func (p *Plugin) openCodeCommandEnv(ctx context.Context, overrides map[string]string, configDir string) ([]string, error) {
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
	resolved, err := p.NativeSessionConfigDir(ctx, overrides)
	if err != nil {
		return nil, err
	}
	if !sameOpenCodeDir(resolved, configDir) {
		env["OPENCODE_DATA_DIR"] = configDir
	}
	out := make([]string, 0, len(env))
	for key, value := range env {
		out = append(out, key+"="+value)
	}
	return out, nil
}

type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("output exceeds the supported size")
	}
	return b.Buffer.Write(p)
}

func runOpenCode(ctx context.Context, binary, dir string, env []string, args ...string) ([]byte, error) {
	cmd := aoprocess.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.Env = env
	stdout := &boundedBuffer{limit: maxOpenCodeExportBytes}
	stderr := &boundedBuffer{limit: 64 << 10}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 400 {
			msg = msg[:400]
		}
		return nil, fmt.Errorf("%s %s: %w: %s", filepath.Base(binary), args[0], err, msg)
	}
	return stdout.Bytes(), nil
}
