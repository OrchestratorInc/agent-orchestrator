package sessionimport

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/auggie"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func auggieRoot(home string) string { return filepath.Join(home, ".augment") }

func discoverAuggie(ctx context.Context, root string) ([]sourceFiles, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	binary, err := auggie.New().ResolveBinary(ctx)
	if err != nil {
		return nil, err
	}
	data, err := readOnlyCLIList(ctx, binary, "--augment-cache-dir", root, "session", "list", "--all", "--json", "--limit", "100")
	if err != nil {
		return nil, err
	}
	return auggieListedSources(root, data)
}

func auggieListedSources(root string, data []byte) ([]sourceFiles, error) {
	var listed []struct {
		ID       string    `json:"sessionId"`
		Modified time.Time `json:"modified"`
		CWD      string    `json:"workspaceRoot"`
		Cloud    bool      `json:"isCloudAgent"`
		Poseidon string    `json:"poseidonAgentId"`
		Parent   string    `json:"parentConversationId"`
	}
	if err := json.Unmarshal(data, &listed); err != nil {
		return nil, err
	}
	files := make([]sourceFiles, 0, len(listed))
	seen := map[string]bool{}
	for _, item := range listed {
		if !nativeIDPattern.MatchString(item.ID) || !filepath.IsAbs(item.CWD) || item.Cloud || item.Poseidon != "" || item.Parent != "" || seen[item.ID] {
			continue
		}
		seen[item.ID] = true
		path := filepath.Join(root, "sessions", item.ID+".json")
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, sourceFiles{harness: domain.HarnessAuggie, root: root, paths: []string{path}, key: item.ID, modTime: info.ModTime(), size: info.Size()})
	}
	return files, nil
}

func readAuggieTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessAuggie, root: root, paths: paths}
	if len(paths) != 1 {
		return t, errors.New("invalid auggie transcript handle")
	}
	t.path = paths[0]
	t.nativeID = strings.TrimSuffix(filepath.Base(t.path), ".json")
	if filepath.Clean(t.path) != filepath.Join(root, "sessions", t.nativeID+".json") {
		return t, errors.New("auggie transcript is outside its cache")
	}
	session, err := auggie.ReadNativeImportSession(ctx, root, t.nativeID)
	if err != nil {
		return t, err
	}
	t.last, err = time.Parse(time.RFC3339Nano, session.Modified)
	if err != nil {
		return t, err
	}
	t.model = session.State.Model
	var items []tagged
	for _, history := range session.History {
		if err := ctx.Err(); err != nil {
			return t, err
		}
		exchange := history.Exchange
		for _, node := range exchange.RequestNodes {
			if t.cwd == "" && node.Type == 4 && len(node.IDEState.Folders) > 0 {
				folder := node.IDEState.Folders[0]
				t.cwd = folder.RepositoryRoot
				if t.cwd == "" {
					t.cwd = folder.FolderRoot
				}
			}
		}
		if history.Summary {
			continue
		}
		var request, response []string
		for _, node := range exchange.RequestNodes {
			if node.Type == 0 && node.TextNode.Content != "" {
				request = append(request, node.TextNode.Content)
			}
		}
		for _, node := range exchange.ResponseNodes {
			if node.Type == 0 && node.Content != "" {
				response = append(response, node.Content)
			}
		}
		user, assistant := strings.Join(request, "\n"), strings.Join(response, "\n")
		if user == "" {
			user = exchange.Request
		}
		if assistant == "" {
			assistant = exchange.Response
		}
		at, err := time.Parse(time.RFC3339Nano, history.Finished)
		if err != nil {
			at = t.last
		}
		for _, message := range []Message{{Role: domain.MessageRoleUser, Text: user, At: at}, {Role: domain.MessageRoleAssistant, Text: assistant, At: at}} {
			if strings.TrimSpace(message.Text) == "" {
				continue
			}
			if len(message.Text) > maxVisibleBytes {
				t.shortened = true
				continue
			}
			items = append(items, tagged{Message: message})
		}
		before := len(items)
		items = trimVisible(items)
		t.shortened = t.shortened || len(items) < before
	}
	if !filepath.IsAbs(t.cwd) {
		return t, errors.New("auggie session has no absolute workspace root")
	}
	for _, item := range items {
		t.messages = append(t.messages, item.Message)
		if t.title == "" && item.Role == domain.MessageRoleUser {
			t.title = item.Text
		}
	}
	if session.Title != "" {
		t.title = session.Title
	}
	if session.Custom != "" {
		t.title = session.Custom
	}
	t.title = importTitle(t.title)
	return t, nil
}
