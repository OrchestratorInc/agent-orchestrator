package opencodev2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
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
// copy is exported beforehand and put back if the import fails, so a failed
// move never leaves this machine without the conversation.
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
	// Deleting an id OpenCode does not have succeeds; exporting one fails.
	previous, err := p.run(ctx, cwd, env, "session", "export", id, "--standalone")
	if got, idErr := ExportedSessionID(previous); err != nil || idErr != nil || got != id {
		previous = nil
	}
	if _, err := p.run(ctx, cwd, env, "session", "delete", id, "--standalone"); err != nil {
		return "", err
	}
	if _, err := p.run(ctx, cwd, env, "session", "import", moved, "--directory", cwd, "--standalone"); err != nil {
		if previous != nil {
			if restoreErr := p.restoreConversation(ctx, previous, env); restoreErr != nil {
				return "", fmt.Errorf("%w; the copy from before could not be put back: %w", err, restoreErr)
			}
		}
		return "", err
	}
	return id, nil
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
		return nil, fmt.Errorf("opencode-v2: %s %s: %w: %s", args[0], args[1], err, bytes.TrimSpace(stderr.Bytes()))
	}
	return out, nil
}
