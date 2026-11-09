package sessionimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func geminiRoot(home string) string {
	if override := os.Getenv("GEMINI_CLI_HOME"); override != "" {
		home = override
	}
	return filepath.Join(home, ".gemini")
}

type geminiMessage struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Content   json.RawMessage `json:"content"`
	Timestamp time.Time       `json:"timestamp"`
}

type geminiRecord struct {
	SessionID   string           `json:"sessionId"`
	ProjectHash string           `json:"projectHash"`
	LastUpdated time.Time        `json:"lastUpdated"`
	Kind        string           `json:"kind"`
	Omitted     bool             `json:"aoOmitted"`
	Messages    *[]geminiMessage `json:"messages"`
	geminiMessage
	Set    *geminiRecord `json:"$set"`
	Rewind *string       `json:"$rewindTo"`
	Patch  *struct {
		ID        string          `json:"id"`
		Content   json.RawMessage `json:"content"`
		Updates   []geminiMessage `json:"updates"`
		RemoveIDs []string        `json:"removeIds"`
		OrderIDs  []string        `json:"orderIds"`
	} `json:"$patch"`
}

// Gemini PartListUnion has plain text parts with no type discriminator. It also
// contains function calls/results, thoughts and inline data, which stay hidden.
func geminiText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var part struct {
		Text    string `json:"text"`
		Thought bool   `json:"thought"`
	}
	if json.Unmarshal(raw, &part) == nil {
		if part.Thought {
			return ""
		}
		return part.Text
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var out []string
	for _, raw := range parts {
		if text := geminiText(raw); text != "" {
			out = append(out, text)
		}
	}
	return strings.Join(out, "\n")
}

func geminiPrompt(text string) string {
	trimmed := strings.TrimSpace(text)
	for _, prefix := range []string{"<session_context>", "<hook_context>", "<environment_context", "<system-reminder", "<system_instruction", "This is the Gemini CLI.", "--- Context from:", "# GEMINI.md"} {
		if strings.HasPrefix(trimmed, prefix) {
			return ""
		}
	}
	return text
}

func geminiCWD(ctx context.Context, root *os.Root, path, hash string, projects []string) string {
	if ctx.Err() != nil {
		return ""
	}
	projectDir := filepath.Dir(filepath.Dir(path))
	if body, err := root.ReadFile(filepath.Join(projectDir, ".project_root")); err == nil {
		return strings.TrimSpace(string(body))
	}
	body, err := root.ReadFile("projects.json")
	var registry struct {
		Projects map[string]string `json:"projects"`
	}
	if err == nil && len(body) <= 4<<20 && json.Unmarshal(body, &registry) == nil {
		for cwd, slug := range registry.Projects {
			sum := sha256.Sum256([]byte(cwd))
			if slug == filepath.Base(projectDir) || hex.EncodeToString(sum[:]) == hash {
				return cwd
			}
		}
	}
	// Older releases stored only sha256(cwd). Match explicit AO project paths;
	// never search the filesystem or write a registry/marker into provider state.
	for _, cwd := range projects {
		sum := sha256.Sum256([]byte(cwd))
		expected := hex.EncodeToString(sum[:])
		if filepath.Base(projectDir) == expected && (hash == "" || hash == expected) {
			return cwd
		}
	}
	return ""
}

func readGeminiTranscriptWithProjects(ctx context.Context, root string, projects []string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessGemini, root: canonical(root)}
	if len(paths) != 1 {
		return t, errors.New("no gemini transcript")
	}
	provider, err := os.OpenRoot(t.root)
	if err != nil {
		return t, err
	}
	defer func() { _ = provider.Close() }()
	rel, err := filepath.Rel(t.root, paths[0])
	if err != nil || !filepath.IsLocal(rel) {
		return t, errors.New("gemini transcript is outside provider home")
	}
	t.path, t.paths = paths[0], paths
	var metadata geminiRecord
	var order []string
	messages := map[string]geminiMessage{}
	evicted := map[string]bool{}
	held := 0
	add := func(msg geminiMessage) {
		if msg.ID == "" {
			return
		}
		if old, exists := messages[msg.ID]; exists {
			held -= len(old.Content)
		} else {
			order = append(order, msg.ID)
		}
		// Bound retained text, even when a recording contains huge tool payloads.
		text := geminiText(msg.Content)
		if strings.Contains(text, omittedText) {
			t.shortened = true
			text = strings.ReplaceAll(text, omittedText, "")
		}
		msg.Content, _ = json.Marshal(text)
		held += len(msg.Content)
		messages[msg.ID] = msg
		for held > maxHeldText && len(order) > 1 {
			id := order[1]
			held -= len(messages[id].Content)
			delete(messages, id)
			order = append(order[:1], order[2:]...)
			evicted[id] = true
			t.shortened = true
		}
	}
	var apply func(geminiRecord)
	apply = func(rec geminiRecord) {
		t.shortened = t.shortened || rec.Omitted
		if rec.SessionID != "" {
			metadata.SessionID = rec.SessionID
		}
		if rec.ProjectHash != "" {
			metadata.ProjectHash = rec.ProjectHash
		}
		if rec.Kind != "" {
			metadata.Kind = rec.Kind
		}
		if rec.LastUpdated.After(t.last) {
			t.last = rec.LastUpdated
		}
		if rec.Messages != nil {
			order, messages, evicted, held = nil, map[string]geminiMessage{}, map[string]bool{}, 0
			for _, msg := range *rec.Messages {
				add(msg)
			}
		}
		if rec.Set != nil {
			apply(*rec.Set)
		}
		if rec.Rewind != nil {
			// A rewind to a message evicted by the bound above drops everything
			// after the opening message, which stays. An id never seen changes
			// nothing.
			keep := len(order)
			if evicted[*rec.Rewind] {
				keep = min(1, len(order))
			}
			for i, id := range order {
				if id == *rec.Rewind {
					keep = i
					break
				}
			}
			for _, id := range order[keep:] {
				held -= len(messages[id].Content)
				delete(messages, id)
			}
			order = order[:keep]
		}
		if rec.Patch != nil {
			patches := rec.Patch.Updates
			if rec.Patch.ID != "" {
				patches = append(patches, geminiMessage{ID: rec.Patch.ID, Content: rec.Patch.Content})
			}
			for _, patch := range patches {
				if old, ok := messages[patch.ID]; ok && patch.Content != nil {
					old.Content = patch.Content
					add(old)
				}
			}
			for _, id := range rec.Patch.RemoveIDs {
				held -= len(messages[id].Content)
				delete(messages, id)
			}
			if len(rec.Patch.RemoveIDs) > 0 {
				retained := order[:0]
				for _, id := range order {
					if _, ok := messages[id]; ok {
						retained = append(retained, id)
					}
				}
				order = retained
			}
			if rec.Patch.OrderIDs != nil {
				order = rec.Patch.OrderIDs
			}
		}
		if rec.ID != "" {
			add(rec.geminiMessage)
		}
	}
	handle := func(line []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var rec geminiRecord
		if json.Unmarshal(line, &rec) == nil {
			apply(rec)
		}
		return nil
	}
	if strings.HasSuffix(paths[0], ".jsonl") {
		err = readOne(provider, t.root, paths[0], handle)
	} else {
		var f *os.File
		f, err = provider.Open(rel)
		if err == nil {
			var body []byte
			body, err = io.ReadAll(io.LimitReader(f, int64(maxRecordBytes)+1))
			_ = f.Close()
			if len(body) > maxRecordBytes {
				return t, errors.New("gemini json exceeds read budget")
			}
			if err == nil {
				err = handle(body)
			}
		}
	}
	if err != nil {
		return t, err
	}
	if !nativeIDPattern.MatchString(metadata.SessionID) || metadata.Kind == "subagent" {
		return t, errors.New("invalid gemini conversation")
	}
	t.nativeID = metadata.SessionID
	t.cwd = geminiCWD(ctx, provider, rel, metadata.ProjectHash, projects)
	if t.cwd == "" {
		return t, errors.New("gemini source folder is unknown")
	}
	visible := 0
	for _, id := range order {
		msg, ok := messages[id]
		if !ok || (msg.Type != "user" && msg.Type != "gemini") {
			continue
		}
		text := geminiText(msg.Content)
		role := domain.MessageRoleAssistant
		if msg.Type == "user" {
			role, text = domain.MessageRoleUser, geminiPrompt(text)
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		if msg.Timestamp.After(t.last) {
			t.last = msg.Timestamp
		}
		if visible+len(text) > maxVisibleBytes {
			t.shortened = true
			continue
		}
		visible += len(text)
		t.messages = append(t.messages, Message{Role: role, Text: text, At: msg.Timestamp})
		if t.title == "" && role == domain.MessageRoleUser {
			t.title = importTitle(text)
		}
	}
	if len(t.messages) == 0 {
		return t, errors.New("no readable gemini conversation")
	}
	finishErr := finishTranscript(&t)
	return t, finishErr
}

func discoverGemini(ctx context.Context, root string) ([]sourceFiles, error) {
	var files []sourceFiles
	err := filepath.WalkDir(filepath.Join(root, "tmp"), func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil || !entry.Type().IsRegular() || filepath.Base(filepath.Dir(path)) != "chats" || !strings.HasPrefix(entry.Name(), "session-") || (!strings.HasSuffix(path, ".json") && !strings.HasSuffix(path, ".jsonl")) {
			return nil //nolint:nilerr // Unreadable provider files are deliberately hidden.
		}
		info, err := entry.Info()
		if err != nil {
			return nil //nolint:nilerr // Files removed or made unreadable during discovery are ineligible.
		}
		files = append(files, sourceFiles{harness: domain.HarnessGemini, root: root, key: path, paths: []string{path}, modTime: info.ModTime(), size: info.Size()})
		return nil
	})
	return files, err
}
