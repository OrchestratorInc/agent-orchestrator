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
	"slices"
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
	// in Cursor, so it is offered from there.
	skip := cursorCLIChats(root)
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
// after: the path Cursor recorded when the folder was trusted, else the folder
// the editor's chat list files the chat under, else the one registered AO
// project with that name, else, when no project has it, the folder the name
// spells when every "-" stands for a separator. Many folders share a name, so
// a recorded path comes before a name match, and each must have the
// directory's name, as Cursor derives one from the other.
func cursorWorkspace(root, folder string, projects []string, recorded func() string) string {
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
	if recorded != nil {
		if path := recorded(); filepath.IsAbs(path) && cursorFolderName(path) == folder {
			if info, err := os.Stat(path); err == nil && info.IsDir() {
				return path
			}
		}
	}
	match, ambiguous := "", false
	for _, project := range projects {
		if cursorFolderName(project) != folder {
			continue
		}
		if match != "" && match != project {
			ambiguous = true
			break
		}
		match = project
	}
	if match != "" && !ambiguous {
		return match
	}
	// Two projects with the name rule out a guess too.
	if ambiguous || runtime.GOOS == "windows" {
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
	recorded := func() string { return cursorEditorFolders(ctx)[t.nativeID] }
	// A chat whose folder cannot be recovered has nowhere to continue.
	if t.cwd = cursorWorkspace(t.root, folder, projects, recorded); t.cwd == "" {
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
	for _, item := range kept {
		t.messages = append(t.messages, item.Message)
	}
	editor, inEditor := readCursorEditorChat(ctx, t.nativeID)
	if inEditor {
		t.messages = mergeCursorEditor(t.messages, editor.messages)
		for _, m := range t.messages {
			if m.At.After(latest) {
				latest = m.At
			}
		}
	}
	if !slices.ContainsFunc(t.messages, func(m Message) bool { return m.Role == domain.MessageRoleUser }) {
		return t, errNoReadableConversation
	}
	if inEditor && editor.title != "" {
		t.title = editor.title
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

// mergeCursorEditor fills the transcript from the editor's own copy, turn by
// turn. Each transcript prompt is matched, in order, with the same prompt in
// the editor's copy; a matched turn takes the editor's times, and the
// editor's reply where the transcript's hides text as [REDACTED]. The editor's
// copy can lag the transcript, so a turn or reply it has not recorded, or a
// reply shorter than what the transcript shows, keeps the transcript's; the
// transcript can lag it too, so the editor's turns after the transcript's
// last prompt are added.
func mergeCursorEditor(messages, editor []Message) []Message {
	type turn struct {
		prompt Message
		reply  *Message
	}
	var turns []turn
	for i, m := range editor {
		switch {
		case m.Role == domain.MessageRoleUser:
			turns = append(turns, turn{prompt: m})
		case len(turns) > 0 && turns[len(turns)-1].reply == nil:
			turns[len(turns)-1].reply = &editor[i]
		}
	}
	same := func(a, b string) bool {
		return strings.Join(strings.Fields(a), " ") == strings.Join(strings.Fields(b), " ")
	}
	out := make([]Message, 0, len(messages))
	next := 0
	var matched *turn
	replied := false
	// A matched turn whose reply only the editor recorded keeps that reply.
	closeTurn := func() {
		if matched != nil && !replied && matched.reply != nil {
			out = append(out, *matched.reply)
		}
	}
	for _, m := range messages {
		switch m.Role {
		case domain.MessageRoleUser:
			closeTurn()
			matched, replied = nil, false
			for k := next; k < len(turns); k++ {
				if same(turns[k].prompt.Text, m.Text) {
					matched, next = &turns[k], k+1
					break
				}
			}
			if matched != nil && !matched.prompt.At.IsZero() {
				m.At = matched.prompt.At
			}
		case domain.MessageRoleAssistant:
			replied = true
			if matched != nil && matched.reply != nil {
				reply := matched.reply
				if strings.Contains(m.Text, "[REDACTED]") && !strings.Contains(reply.Text, "[REDACTED]") && len(reply.Text) >= len(strings.ReplaceAll(m.Text, "[REDACTED]", "")) {
					m.Text = reply.Text
				}
				if !reply.At.IsZero() {
					m.At = reply.At
				}
			}
		}
		out = append(out, m)
	}
	closeTurn()
	// Turns after the transcript's last prompt, when that prompt is matched,
	// are ones the transcript has not recorded yet.
	if matched != nil {
		for _, rest := range turns[next:] {
			out = append(out, rest.prompt)
			if rest.reply != nil {
				out = append(out, *rest.reply)
			}
		}
	}
	return out
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
		if text := cursorReply(block.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// cursorReply drops the "[REDACTED]" lines that stand for hidden thinking.
func cursorReply(text string) string {
	lines := strings.Split(text, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if strings.TrimSpace(line) != "[REDACTED]" {
			kept = append(kept, line)
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
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
// Claude Code can continue it. Cursor keeps no tool results with a chat, so
// only the conversation's text is carried over. A transcript is written once:
// publishClaudeTranscript never replaces one.
func writeCursorTranscript(ctx context.Context, t transcript) (transcript, error) {
	root := homeClaudeRoot()
	// A chat imported before, whose session has since ended, continues from
	// the transcript written then; its history is that transcript's, which
	// holds the turns added since.
	if path := claudeTranscripts(root)[t.nativeID]; path != "" {
		held, err := readClaudeTranscript(ctx, root, path)
		if err != nil {
			return t, err
		}
		t.harness, t.root, t.path, t.paths = domain.HarnessClaudeCode, held.root, held.path, held.paths
		t.messages, t.shortened = held.messages, held.shortened
		if held.last.After(t.last) {
			t.last = held.last
		}
		return t, nil
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
