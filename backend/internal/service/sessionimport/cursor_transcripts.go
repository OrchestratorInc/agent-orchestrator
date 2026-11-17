package sessionimport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// cursorTranscriptSource offers the chats Cursor's editor keeps only as agent
// transcripts, projects/<folder>/agent-transcripts/<id>/<id>.jsonl (or the
// older agent-transcripts/<id>.jsonl). The Cursor CLI cannot resume them, as
// it resumes only its own chats/ databases, so a selected chat is written as
// a new Claude Code transcript and continues there, as Conductor chats do.
const cursorTranscriptSource domain.AgentHarness = "cursor-transcripts"

// cursorFolderChars is what Cursor replaces when it names a folder's project
// directory: every character that is not an ASCII letter or digit.
var cursorFolderChars = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// cursorFolderName is Cursor's projects/ directory name for a folder.
func cursorFolderName(path string) string {
	return strings.Trim(cursorFolderChars.ReplaceAllString(path, "-"), "-")
}

func discoverCursorTranscripts(ctx context.Context, root string) ([]sourceFiles, error) {
	root = canonical(root)
	projects, err := os.ReadDir(filepath.Join(root, "projects"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// A Cursor CLI chat writes a transcript too; its own database resumes it
	// in Cursor, so it is offered from there. A chat Claude Code already holds,
	// such as one imported earlier, is offered from Claude Code.
	skip := cursorCLIChats(root)
	for id := range claudeTranscripts(homeClaudeRoot()) {
		skip[id] = true
	}
	byID := map[string]sourceFiles{}
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		dir := filepath.Join(root, "projects", project.Name(), "agent-transcripts")
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // Most project folders hold no transcripts.
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			id, path := strings.TrimSuffix(entry.Name(), ".jsonl"), filepath.Join(dir, entry.Name())
			if entry.IsDir() {
				id, path = entry.Name(), filepath.Join(dir, entry.Name(), entry.Name()+".jsonl")
			} else if id == entry.Name() {
				continue
			}
			if !nativeIDPattern.MatchString(id) || skip[id] {
				continue
			}
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			// Both layouts can hold one chat after a Cursor upgrade; the newer file is current.
			if prior, ok := byID[id]; ok && !info.ModTime().After(prior.modTime) {
				continue
			}
			byID[id] = sourceFiles{harness: cursorTranscriptSource, root: root, paths: []string{path}, key: id, modTime: info.ModTime(), size: info.Size()}
		}
	}
	out := make([]sourceFiles, 0, len(byID))
	for _, file := range byID {
		out = append(out, file)
	}
	return out, nil
}

// cursorCLIChats names the chats the Cursor CLI keeps in chats/<folder hash>/<id>.
func cursorCLIChats(root string) map[string]bool {
	ids := map[string]bool{}
	buckets, _ := os.ReadDir(filepath.Join(root, "chats"))
	for _, bucket := range buckets {
		chats, _ := os.ReadDir(filepath.Join(root, "chats", bucket.Name()))
		for _, chat := range chats {
			ids[chat.Name()] = true
		}
	}
	return ids
}

// cursorProjectFolder returns the projects/ directory name a transcript is in,
// accepting only the transcript layouts Cursor writes. Subagent transcripts
// belong to their parent chat and are not offered.
func cursorProjectFolder(root, path string) (string, bool) {
	rel, err := filepath.Rel(filepath.Join(root, "projects"), path)
	if err != nil || !filepath.IsLocal(rel) {
		return "", false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	switch {
	case len(parts) == 3 && parts[1] == "agent-transcripts":
	case len(parts) == 4 && parts[1] == "agent-transcripts" && parts[2]+".jsonl" == parts[3]:
	default:
		return "", false
	}
	return parts[0], true
}

// cursorWorkspace recovers the folder a Cursor project directory was named
// after: the path Cursor recorded when the folder was trusted, else the one
// registered AO project with that name, else the folder the name spells when
// every "-" stands for a separator.
func cursorWorkspace(root, folder string, projects []string) string {
	if dir, err := os.OpenRoot(filepath.Join(root, "projects", folder)); err == nil {
		var trusted struct {
			WorkspacePath string `json:"workspacePath"`
		}
		if f, err := dir.Open(".workspace-trusted"); err == nil {
			_ = json.NewDecoder(io.LimitReader(f, 64<<10)).Decode(&trusted)
			_ = f.Close()
		}
		_ = dir.Close()
		if filepath.IsAbs(trusted.WorkspacePath) && cursorFolderName(trusted.WorkspacePath) == folder {
			return trusted.WorkspacePath
		}
	}
	match := ""
	for _, project := range projects {
		if cursorFolderName(project) != folder {
			continue
		}
		if match != "" && match != project {
			match = "" // Ambiguous: two projects share the name.
			break
		}
		match = project
	}
	if match != "" {
		return match
	}
	if runtime.GOOS == "windows" {
		return ""
	}
	guess := string(filepath.Separator) + strings.ReplaceAll(folder, "-", string(filepath.Separator))
	if info, err := os.Stat(guess); err == nil && info.IsDir() {
		return guess
	}
	return ""
}

func readCursorTranscriptFile(ctx context.Context, root string, projects []string, paths ...string) (transcript, error) {
	t := transcript{harness: cursorTranscriptSource, root: canonical(root)}
	if len(paths) != 1 {
		return t, errors.New("no cursor transcript")
	}
	t.path, t.paths = paths[0], paths
	t.nativeID = strings.TrimSuffix(filepath.Base(t.path), ".jsonl")
	folder, ok := cursorProjectFolder(t.root, t.path)
	if !ok || !nativeIDPattern.MatchString(t.nativeID) {
		return t, errNoReadableConversation
	}
	// A chat whose folder cannot be recovered has nowhere to continue.
	if t.cwd = cursorWorkspace(t.root, folder, projects); t.cwd == "" {
		return t, errNoReadableConversation
	}
	providerRoot, err := os.OpenRoot(t.root)
	if err != nil {
		return t, err
	}
	defer func() { _ = providerRoot.Close() }()
	var (
		items   []tagged
		latest  time.Time
		wrapped bool
		visible int
	)
	err = readOne(providerRoot, t.root, t.path, func(line []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var r struct {
			Role    string `json:"role"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &r) != nil {
			return nil //nolint:nilerr // A torn or foreign record does not hide the rest of the conversation.
		}
		text := cursorText(r.Message.Content)
		if strings.Contains(text, omittedText) {
			t.shortened, text = true, strings.TrimSpace(strings.ReplaceAll(text, omittedText, ""))
		}
		switch r.Role {
		case "user":
			prompt, at, query := cursorPrompt(text)
			if !at.IsZero() {
				latest = at
			}
			if prompt == "" {
				return nil
			}
			wrapped = wrapped || query
			kind := "prompt"
			if !query {
				kind = "injected"
			}
			items = append(items, tagged{Message: Message{Role: domain.MessageRoleUser, Text: prompt, At: at}, kind: kind})
		case "assistant":
			if text == "" {
				return nil
			}
			// Cursor writes each step of a reply, between tool calls, as its own record.
			if n := len(items); n > 0 && items[n-1].Role == domain.MessageRoleAssistant {
				items[n-1].Text += "\n\n" + text
			} else {
				items = append(items, tagged{Message: Message{Role: domain.MessageRoleAssistant, Text: text, At: latest}})
			}
		default:
			return nil
		}
		visible += len(text)
		if visible > 2*maxVisibleBytes {
			n := len(items)
			items = trimVisible(items)
			t.shortened, visible = t.shortened || len(items) < n, 0
			for _, item := range items {
				visible += len(item.Text)
			}
		}
		return nil
	})
	if err != nil {
		return t, err
	}
	// Cursor wraps what the user typed in <user_query>. When it does, a user
	// record without one is a notice Cursor sent itself, such as a subagent's result.
	kept := items[:0]
	for _, item := range items {
		if wrapped && item.kind == "injected" {
			continue
		}
		if n := len(kept); n > 0 && item.Role == domain.MessageRoleAssistant && kept[n-1].Role == domain.MessageRoleAssistant {
			kept[n-1].Text += "\n\n" + item.Text
			continue
		}
		kept = append(kept, item)
	}
	n := len(kept)
	kept = trimVisible(kept)
	t.shortened = t.shortened || len(kept) < n
	prompted := false
	for _, item := range kept {
		prompted = prompted || item.Role == domain.MessageRoleUser
		t.messages = append(t.messages, item.Message)
	}
	if !prompted {
		return t, errNoReadableConversation
	}
	// The file changes with every turn; its prompts carry only minute-resolution times.
	if info, err := os.Stat(t.path); err == nil {
		t.last = info.ModTime()
	}
	if latest.After(t.last) {
		t.last = latest
	}
	err = finishTranscript(&t)
	return t, err
}

// cursorText is a record's visible text. Cursor appends the model's thinking
// to a reply's text, as "[REDACTED]" where the model hid it; that line is not
// part of the reply. Tool calls are not shown.
func cursorText(content json.RawMessage) string {
	var blocks []textBlock
	var text string
	if json.Unmarshal(content, &text) == nil {
		blocks = []textBlock{{Type: "text", Text: text}}
	} else if json.Unmarshal(content, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, block := range blocks {
		if !strings.EqualFold(block.Type, "text") {
			continue
		}
		lines := strings.Split(block.Text, "\n")
		kept := lines[:0]
		for _, line := range lines {
			if strings.TrimSpace(line) != "[REDACTED]" {
				kept = append(kept, line)
			}
		}
		if text := strings.TrimSpace(strings.Join(kept, "\n")); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// cursorPrompt returns what the user typed, from inside <user_query>, and the
// time in Cursor's <timestamp> beside it. query reports a <user_query>.
func cursorPrompt(text string) (prompt string, at time.Time, query bool) {
	if _, rest, ok := strings.Cut(text, "<timestamp>"); ok {
		if stamp, _, ok := strings.Cut(rest, "</timestamp>"); ok {
			at = cursorTime(stamp)
			text = strings.Replace(text, "<timestamp>"+stamp+"</timestamp>", "", 1)
		}
	}
	_, rest, ok := strings.Cut(text, "<user_query>")
	if !ok {
		return strings.TrimSpace(text), at, false
	}
	if end := strings.LastIndex(rest, "</user_query>"); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest), at, true
}

// cursorTime reads Cursor's prompt time, such as
// "Tuesday, Jun 2, 2026, 11:20 AM (UTC+8)". A time without a readable UTC
// offset is left out rather than guessed.
func cursorTime(text string) time.Time {
	text = strings.NewReplacer("\u202f", " ", "\u00a0", " ").Replace(strings.TrimSpace(text))
	stamp, zone, ok := strings.Cut(text, " (")
	if !ok {
		return time.Time{}
	}
	offset, ok := utcOffset(strings.TrimSuffix(zone, ")"))
	if !ok {
		return time.Time{}
	}
	location := time.FixedZone("", offset)
	for _, layout := range []string{"Monday, Jan 2, 2006, 3:04 PM", "Monday, January 2, 2006, 3:04 PM", "Monday, Jan 2, 2006, 15:04", "Monday, January 2, 2006, 15:04"} {
		if at, err := time.ParseInLocation(layout, stamp, location); err == nil {
			return at
		}
	}
	return time.Time{}
}

// utcOffset reads "UTC", "UTC+8", "UTC-3" or "UTC+5:30" as seconds east of UTC.
func utcOffset(zone string) (int, bool) {
	rest, ok := strings.CutPrefix(zone, "UTC")
	if !ok {
		return 0, false
	}
	if rest == "" {
		return 0, true
	}
	sign := 1
	switch rest[0] {
	case '+':
	case '-':
		sign = -1
	default:
		return 0, false
	}
	hours, minutes, _ := strings.Cut(rest[1:], ":")
	h, err := strconv.Atoi(hours)
	if err != nil || h > 14 {
		return 0, false
	}
	m := 0
	if minutes != "" {
		if m, err = strconv.Atoi(minutes); err != nil || m > 59 {
			return 0, false
		}
	}
	return sign * (h*3600 + m*60), true
}

// writeCursorTranscript writes the chat as a new Claude Code transcript, so
// Claude Code can continue it. Cursor's transcripts keep no tool results, so
// only the conversation's text is carried over.
func writeCursorTranscript(ctx context.Context, t transcript) (transcript, error) {
	root := homeClaudeRoot()
	if claudeTranscripts(root)[t.nativeID] != "" {
		return t, errClaudeHasConversation
	}
	// The Claude API takes alternating turns that open with the user.
	var turns []Message
	for _, m := range t.messages {
		if n := len(turns); n > 0 && turns[n-1].Role == m.Role {
			turns[n-1].Text += "\n\n" + m.Text
		} else if n > 0 || m.Role == domain.MessageRoleUser {
			turns = append(turns, m)
		}
	}
	if len(turns) == 0 {
		return t, errNoReadableConversation
	}
	path, err := publishClaudeTranscript(root, t.cwd, t.nativeID, func(encoder *json.Encoder) error {
		var parent *string
		for _, turn := range turns {
			if err := ctx.Err(); err != nil {
				return err
			}
			var content any = turn.Text
			if turn.Role == domain.MessageRoleAssistant {
				content = []map[string]string{{"type": "text", "text": turn.Text}}
			}
			message, err := json.Marshal(map[string]any{"role": string(turn.Role), "content": content})
			if err != nil {
				return err
			}
			at := turn.At
			if at.IsZero() {
				at = t.last
			}
			id := uuid.NewString()
			line := claudeLine{ParentUUID: parent, UserType: "external", CWD: t.cwd, SessionID: t.nativeID, Type: string(turn.Role), Message: message, UUID: id, Timestamp: claudeTimestamp(at)}
			parent = &id
			if err := encoder.Encode(line); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return t, err
	}
	t.harness, t.root, t.path, t.paths = domain.HarnessClaudeCode, root, path, []string{path}
	return t, nil
}
