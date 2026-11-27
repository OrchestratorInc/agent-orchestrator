package opencodev2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	aoprocess "github.com/OrchestratorInc/agent-orchestrator/backend/internal/process"
)

var sessionIDPattern = regexp.MustCompile(`^ses_[A-Za-z0-9]{1,64}$`)

// ExportConversation returns session id as `opencode session export` writes
// it: {"info": {...}, "messages": [...]}.
func (p *Plugin) ExportConversation(ctx context.Context, id string, env map[string]string) ([]byte, error) {
	if !sessionIDPattern.MatchString(id) {
		return nil, fmt.Errorf("opencode-v2: invalid session id %q", id)
	}
	out, err := p.run(ctx, "", env, "session", "export", id, "--standalone")
	if err != nil {
		return nil, err
	}
	got, err := ExportedSessionID(out)
	if err != nil {
		return nil, err
	}
	if got != id {
		return nil, fmt.Errorf("opencode-v2: export of %s returned session %s", id, got)
	}
	return out, nil
}

// ImportConversation places data, a `session export` document, as a session
// run in cwd and returns its id. OpenCode refuses an id it already has, so a
// copy here (the one left when the session moved away) is deleted first. That
// copy is deleted only once it is backed up: exported, and the export names
// this session. The backup is put back if the import fails, so a failed move
// never leaves this machine without the conversation. Only OpenCode's own
// "Session not found" says there is no copy to back up; any other export
// failure (a timeout, a store it cannot read) leaves the copy alone and the
// import is refused.
func (p *Plugin) ImportConversation(ctx context.Context, data []byte, cwd string, env map[string]string) (string, error) {
	id, err := ExportedSessionID(data)
	if err != nil {
		return "", err
	}
	moved, err := writeTempDocument(data)
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(moved) }()
	previous, err := p.run(ctx, cwd, env, "session", "export", id, "--standalone")
	switch {
	case err == nil:
		if got, idErr := ExportedSessionID(previous); idErr != nil || got != id {
			return "", fmt.Errorf("opencode-v2: the conversation here could not be backed up, so it was not replaced: its export is not session %s", id)
		}
		if _, err := p.run(ctx, cwd, env, "session", "delete", id, "--standalone"); err != nil {
			// The delete may have removed it before it failed.
			return "", p.putBack(ctx, id, previous, cwd, env, err)
		}
	case sessionNotFound(err, id):
		// No copy here: nothing to back up or delete.
		previous = nil
	default:
		return "", fmt.Errorf("opencode-v2: the conversation here could not be backed up, so it was not replaced: %w", err)
	}
	if _, err := p.run(ctx, cwd, env, "session", "import", moved, "--directory", cwd, "--standalone"); err != nil {
		if previous != nil {
			return "", p.putBack(ctx, id, previous, cwd, env, err)
		}
		return "", err
	}
	return id, nil
}

// putBack restores backup, the copy deleted to make way for an import that
// then failed with cause. Session id as OpenCode holds it then decides: the
// copy from before (its delete failed before removing it) is left; a session
// the import wrote before it failed is deleted, since OpenCode refuses to
// import over it; then the backup is imported. It runs even when ctx is
// cancelled, which may be why the import failed: a cancelled move must not
// cost the copy that was here. Each command is bounded by run's own timeout.
func (p *Plugin) putBack(ctx context.Context, id string, backup []byte, cwd string, env map[string]string, cause error) error {
	ctx = context.WithoutCancel(ctx)
	current, err := p.run(ctx, cwd, env, "session", "export", id, "--standalone")
	switch {
	case err == nil && bytes.Equal(current, backup):
		return cause
	case err == nil:
		if _, err := p.run(ctx, cwd, env, "session", "delete", id, "--standalone"); err != nil {
			return fmt.Errorf("%w; the copy from before could not be put back: %w", cause, err)
		}
	case !sessionNotFound(err, id):
		return fmt.Errorf("%w; could not tell whether the copy from before is still here: %w", cause, err)
	}
	if err := p.restoreConversation(ctx, backup, env); err != nil {
		return fmt.Errorf("%w; the copy from before could not be put back: %w", cause, err)
	}
	return cause
}

// restoreConversation imports a session's earlier export back where it ran.
func (p *Plugin) restoreConversation(ctx context.Context, doc []byte, env map[string]string) error {
	file, err := writeTempDocument(doc)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file) }()
	var parsed struct {
		Info struct {
			Location struct {
				Directory string `json:"directory"`
			} `json:"location"`
		} `json:"info"`
	}
	args := []string{"session", "import", file, "--standalone"}
	if json.Unmarshal(doc, &parsed) == nil && parsed.Info.Location.Directory != "" {
		args = []string{"session", "import", file, "--directory", parsed.Info.Location.Directory, "--standalone"}
	}
	_, err = p.run(ctx, "", env, args...)
	return err
}

// writeTempDocument writes a session export document to a temp file.
func writeTempDocument(data []byte) (string, error) {
	file, err := os.CreateTemp("", "ao-opencode-import-*.json")
	if err != nil {
		return "", err
	}
	_, err = file.Write(data)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(file.Name())
		return "", err
	}
	return file.Name(), nil
}

// ExportedSessionID is the session id a `session export` document names.
func ExportedSessionID(data []byte) (string, error) {
	var doc struct {
		Info struct {
			ID string `json:"id"`
		} `json:"info"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || !sessionIDPattern.MatchString(doc.Info.ID) {
		return "", fmt.Errorf("opencode-v2: not a session export document")
	}
	return doc.Info.ID, nil
}

// run runs the OpenCode 2 CLI on its isolated data home with a private server.
func (p *Plugin) run(ctx context.Context, dir string, env map[string]string, args ...string) ([]byte, error) {
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, err
	}
	home, err := DataHome(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := aoprocess.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.Env = append(cmd.Env, "XDG_DATA_HOME="+home)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, &runError{args: args, err: err, stderr: string(bytes.TrimSpace(stderr.Bytes()))}
	}
	return out, nil
}

// runError is a failed opencode command, with what it wrote to stderr.
type runError struct {
	args   []string
	err    error
	stderr string
}

func (e *runError) Error() string {
	return fmt.Sprintf("opencode-v2: %s %s: %v: %s", e.args[0], e.args[1], e.err, e.stderr)
}

func (e *runError) Unwrap() error { return e.err }

// sessionNotFound reports OpenCode saying it has no session id: `session
// export` exits with an error and "Session not found: <id>" on stderr. A
// command that failed any other way (a timeout, a store it could not read)
// says nothing about whether the session exists.
func sessionNotFound(err error, id string) bool {
	var failed *runError
	if !errors.As(err, &failed) {
		return false
	}
	var exit *exec.ExitError
	if !errors.As(failed.err, &exit) {
		return false
	}
	for _, line := range strings.Split(failed.stderr, "\n") {
		if strings.TrimSpace(line) == "Session not found: "+id {
			return true
		}
	}
	return false
}
