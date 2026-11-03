package kilocode

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // read-only native identity and collision checks

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

var _ ports.AgentImportedSessionPreparer = (*Plugin)(nil)

// PrepareImportedSession exports and imports an independent native identity in
// the AO worktree. S2 checkpoints it before launch, so retries do not fork again.
func (p *Plugin) PrepareImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	return p.prepareKiloImport(ctx, r, p.runKiloImportCommand)
}

func (p *Plugin) prepareKiloImport(ctx context.Context, r ports.ImportedSessionRelocation, run func(context.Context, string, string, ...string) ([]byte, error)) (ports.NativeSessionRef, error) {
	if !kiloCodeSessionID.MatchString(r.NativeSessionID) || !filepath.IsAbs(r.ConfigDir) || filepath.Base(r.ConfigDir) != "kilo" || !filepath.IsAbs(r.LaunchCWD) {
		return ports.NativeSessionRef{}, errors.New("invalid kilo import source or worktree")
	}
	if os.Getenv("KILO_WORKSPACE_ID") != "" || os.Getenv("OPENCODE_WORKSPACE_ID") != "" {
		return ports.NativeSessionRef{}, errors.New("kilo import requires an implicit local workspace")
	}
	if path := os.Getenv("KILO_DB"); path != "" && path != "kilo.db" {
		return ports.NativeSessionRef{}, errors.New("kilo import does not support a custom database filename")
	}
	cwd, err := filepath.EvalSymlinks(r.LaunchCWD)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	root := os.Getenv("AO_DATA_DIR")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ports.NativeSessionRef{}, err
		}
		root = filepath.Join(home, ".ao", "data")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	root = filepath.Join(root, "tmp", "native-import")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return ports.NativeSessionRef{}, err
	}
	temp, err := os.MkdirTemp(root, "kilo-")
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	defer func() { _ = os.RemoveAll(temp) }()
	dbURL := url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(r.ConfigDir, "kilo.db"))}
	db, err := sql.Open("sqlite", dbURL.String()+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	defer func() { _ = db.Close() }()
	var original int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM session WHERE id = ?`, r.NativeSessionID).Scan(&original); err != nil || original != 1 {
		return ports.NativeSessionRef{}, errors.New("kilo native import source is unavailable")
	}
	exported, err := run(ctx, cwd, r.ConfigDir, "export", r.NativeSessionID)
	if err != nil {
		return ports.NativeSessionRef{}, fmt.Errorf("export kilo history: %w", err)
	}
	if err := os.WriteFile(filepath.Join(temp, "original.json"), exported, 0o600); err != nil {
		return ports.NativeSessionRef{}, err
	}
	now := time.Now().UnixMilli()
	if now < 0 {
		return ports.NativeSessionRef{}, errors.New("kilo import timestamp is before the provider epoch")
	}
	gen := kiloImportID{timestamp: uint64(now), random: rand.Reader}
	remapped, nativeID, err := remapKiloExport(exported, r.NativeSessionID, cwd, gen.next, func(id string) (bool, error) {
		var present bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT id FROM session WHERE id = ? UNION ALL SELECT id FROM message WHERE id = ? UNION ALL SELECT id FROM part WHERE id = ? UNION ALL SELECT id FROM session_message WHERE id = ?)`, id, id, id, id).Scan(&present)
		return present, err
	})
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	exportPath := filepath.Join(temp, "native.json")
	if err := os.WriteFile(exportPath, remapped, 0o600); err != nil {
		return ports.NativeSessionRef{}, err
	}
	if _, err := run(ctx, cwd, r.ConfigDir, "import", exportPath); err != nil {
		return ports.NativeSessionRef{}, fmt.Errorf("import kilo history: %w", err)
	}
	var directory string
	var workspace sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT directory, workspace_id FROM session WHERE id = ?`, nativeID).Scan(&directory, &workspace); err != nil {
		return ports.NativeSessionRef{}, fmt.Errorf("confirm imported kilo identity: %w", err)
	}
	if filepath.Clean(directory) != cwd || (workspace.Valid && workspace.String != "") {
		return ports.NativeSessionRef{}, errors.New("imported kilo execution location does not match the worktree")
	}
	var expected struct {
		Messages []struct {
			Info struct {
				ID string `json:"id"`
			} `json:"info"`
			Parts []struct {
				ID string `json:"id"`
			} `json:"parts"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(remapped, &expected); err != nil {
		return ports.NativeSessionRef{}, err
	}
	for _, message := range expected.Messages {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM message WHERE id = ? AND session_id = ?`, message.Info.ID, nativeID).Scan(&count); err != nil || count != 1 {
			return ports.NativeSessionRef{}, errors.New("imported kilo message history was not confirmed")
		}
		for _, part := range message.Parts {
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM part WHERE id = ? AND session_id = ? AND message_id = ?`, part.ID, nativeID, message.Info.ID).Scan(&count); err != nil || count != 1 {
				return ports.NativeSessionRef{}, errors.New("imported kilo part history was not confirmed")
			}
		}
	}
	return ports.NativeSessionRef{NativeSessionID: nativeID, ConfigDir: r.ConfigDir}, nil
}

// kiloImportOutput bounds both provider streams and cancels the process tree
// when either stream exceeds its limit; stderr contents are never disclosed.
type kiloImportOutput struct {
	buffer    bytes.Buffer
	remaining int
	cancel    context.CancelFunc
}

// Write appends a bounded provider output chunk and cancels on overflow.
func (b *kiloImportOutput) Write(data []byte) (int, error) {
	if len(data) > b.remaining {
		b.cancel()
		return 0, errors.New("kilo native import output limit exceeded")
	}
	b.remaining -= len(data)
	return b.buffer.Write(data)
}

func (p *Plugin) runKiloImportCommand(ctx context.Context, cwd, root string, args ...string) ([]byte, error) {
	binary, err := p.kilocodeBinary(ctx)
	if err != nil {
		return nil, err
	}
	commandCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stdout := &kiloImportOutput{remaining: 64 << 20, cancel: cancel}
	stderr := &kiloImportOutput{remaining: 64 << 10, cancel: cancel}
	command := aoprocess.CommandContext(commandCtx, binary, args...)
	command.Dir = cwd
	command.Env = append(os.Environ(), "XDG_DATA_HOME="+filepath.Dir(root))
	command.Stdout, command.Stderr = stdout, stderr
	command.WaitDelay = 100 * time.Millisecond
	if err := command.Run(); err != nil {
		return nil, err
	}
	return stdout.buffer.Bytes(), nil
}
