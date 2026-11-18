package sessionimport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/aider"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const aiderImportHistory = ".aider.chat.history.md"

func discoverAiderRegistered(ctx context.Context, projects []string) ([]sourceFiles, error) {
	var out []sourceFiles
	for _, project := range projects {
		files, err := discoverAiderProject(ctx, project)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue // One unreadable registered project does not hide the others.
		}
		out = append(out, files...)
	}
	return out, nil
}

// discoverAiderProject reads exactly one registered project root. It never
// searches a home directory or recursively traverses a repository.
func discoverAiderProject(ctx context.Context, project string) ([]sourceFiles, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(project)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(aiderImportHistory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > maxVisibleBytes {
		return nil, nil
	}
	project = canonical(project)
	path := filepath.Join(project, aiderImportHistory)
	return []sourceFiles{{harness: domain.HarnessAider, root: project, paths: []string{path}, key: aider.NativeHistoryID(project), modTime: info.ModTime(), size: info.Size()}}, nil
}

func readAiderTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessAider, root: canonical(root)}
	if len(paths) != 1 || canonical(paths[0]) != filepath.Join(t.root, aiderImportHistory) {
		return t, errors.New("aider native history must be at the project root")
	}
	provider, err := os.OpenRoot(t.root)
	if err != nil {
		return t, err
	}
	defer func() { _ = provider.Close() }()
	info, err := provider.Lstat(aiderImportHistory)
	if err != nil {
		return t, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxVisibleBytes {
		return t, errors.New("aider native history is not a bounded regular file")
	}
	f, err := provider.Open(aiderImportHistory)
	if err != nil {
		return t, err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxVisibleBytes+1))
	_ = f.Close()
	if err != nil {
		return t, err
	}
	if len(data) > maxVisibleBytes {
		return t, errors.New("aider native history exceeds read budget")
	}
	t.fingerprint = aider.NativeHistoryFingerprint(data)
	t.path, t.paths, t.cwd = paths[0], append([]string(nil), paths...), t.root
	t.nativeID, t.last = aider.NativeHistoryID(t.root), info.ModTime()
	var user, assistant strings.Builder
	appendMessage := func(role domain.MessageRole, text *strings.Builder) {
		value := strings.TrimSpace(text.String())
		text.Reset()
		if value == "" {
			return
		}
		if role == domain.MessageRoleUser && t.title == "" {
			t.title = importTitle(value)
		}
		t.messages = append(t.messages, Message{Role: role, Text: value, At: t.last})
	}
	// Match Aider utils.split_chat_history_markdown: headings are metadata,
	// #### introduces user text, and > introduces tool output, which is hidden.
	for _, raw := range bytes.SplitAfter(data, []byte("\n")) {
		if err := ctx.Err(); err != nil {
			return t, err
		}
		line := string(raw)
		switch {
		case strings.HasPrefix(line, "# "):
			continue
		case strings.HasPrefix(line, "> "):
			appendMessage(domain.MessageRoleAssistant, &assistant)
			appendMessage(domain.MessageRoleUser, &user)
		case strings.HasPrefix(line, "#### "):
			appendMessage(domain.MessageRoleAssistant, &assistant)
			user.WriteString(line[5:])
		default:
			appendMessage(domain.MessageRoleUser, &user)
			assistant.WriteString(line)
		}
	}
	appendMessage(domain.MessageRoleAssistant, &assistant)
	appendMessage(domain.MessageRoleUser, &user)
	if t.title == "" {
		return t, errors.New("aider native history has no user message")
	}
	finishErr := finishTranscript(&t)
	return t, finishErr
}
