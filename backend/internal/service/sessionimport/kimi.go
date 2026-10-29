package sessionimport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Kimi Code (Moonshot AI) keeps one conversation per session directory under
// <root>/sessions/wd_<name>_<hash>/session_<uuid>/. The resumable transcript is
// the main agent's wire.jsonl; a sibling state.json holds the title and cwd.
// The session_<uuid> directory name is the native id `kimi --session` and the
// ACP session/load both accept, so the import binds to it directly.
const (
	kimiSessionsDir = "sessions"
	kimiMainAgent   = "main"
	kimiWireFile    = "wire.jsonl"
	kimiStateFile   = "state.json"
	// kimiStateBudget caps the metadata sidecar read; it holds only scalars.
	kimiStateBudget = 1 << 20
)

// kimiNativeID matches a Kimi session directory name: `session_<uuid>`.
var kimiNativeID = regexp.MustCompile(`^session_[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// kimiState is the readable part of a session's state.json sidecar.
type kimiState struct {
	ID        string `json:"id"`
	CWD       string `json:"cwd"`
	Title     string `json:"title"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

// kimiRecord is one wire.jsonl line. Only user prompts the user actually typed
// (append_message with user origin) and streamed assistant text (content.part
// of type text) are display messages; everything else is tool noise, reasoning,
// injected context, or provider bookkeeping.
type kimiRecord struct {
	Type    string `json:"type"`
	Time    int64  `json:"time"`
	Message struct {
		Role    string          `json:"role"`
		Content []kimiBlock     `json:"content"`
		Origin  json.RawMessage `json:"origin"`
	} `json:"message"`
	Event struct {
		Type   string `json:"type"`
		TurnID int    `json:"turnId"`
		Part   struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"part"`
	} `json:"event"`
}

type kimiBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// kimiSessionDir is the session_<uuid> folder from its main agent's wire path:
// <root>/sessions/wd_*/session_<uuid>/agents/main/wire.jsonl.
func kimiSessionDir(wirePath string) string {
	return filepath.Dir(filepath.Dir(filepath.Dir(wirePath)))
}

// discoverKimi lists each session's main-agent transcript, newest first. Only
// the main agent is a conversation; subagent transcripts belong to their turn.
func discoverKimi(ctx context.Context, root string) ([]sourceFiles, error) {
	dir := filepath.Join(root, kimiSessionsDir)
	var out []sourceFiles
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return nil //nolint:nilerr // Unreadable provider files are deliberately hidden.
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() || entry.Name() != kimiWireFile {
			return nil
		}
		// Require .../session_<uuid>/agents/main/wire.jsonl so subagents, caches
		// and other sidecars never masquerade as conversations.
		if filepath.Base(filepath.Dir(path)) != kimiMainAgent || filepath.Base(filepath.Dir(filepath.Dir(path))) != "agents" {
			return nil
		}
		nativeID := filepath.Base(kimiSessionDir(path))
		if !kimiNativeID.MatchString(nativeID) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil //nolint:nilerr // Unreadable files are ineligible.
		}
		group := sourceFiles{harness: domain.HarnessKimi, root: root, key: nativeID, paths: []string{path}, modTime: info.ModTime(), size: info.Size()}
		// A later title or cwd edit touches state.json alone; count it as activity.
		if stat, err := os.Stat(filepath.Join(kimiSessionDir(path), kimiStateFile)); err == nil {
			group.size += stat.Size()
			if stat.ModTime().After(group.modTime) {
				group.modTime = stat.ModTime()
			}
		}
		out = append(out, group)
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].modTime.After(out[j].modTime) })
	return out, nil
}

// readKimiTranscript reads one Kimi conversation: the user's typed prompts and
// the assistant's streamed text, in order, dropping injected context, reasoning
// and tool traffic. Title and cwd come from the state.json sidecar.
func readKimiTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessKimi, root: root}
	if len(paths) == 0 {
		return t, errors.New("no transcript")
	}
	root = canonical(root)
	t.root = root
	wirePath := paths[len(paths)-1]
	t.path, t.paths = wirePath, []string{wirePath}
	t.nativeID = filepath.Base(kimiSessionDir(wirePath))
	if !kimiNativeID.MatchString(t.nativeID) {
		return t, errors.New("not a Kimi session directory")
	}
	providerRoot, err := os.OpenRoot(root)
	if err != nil {
		return t, err
	}
	defer func() { _ = providerRoot.Close() }()

	state := readKimiState(providerRoot, root, wirePath)
	if state.ID != "" && state.ID != t.nativeID {
		return t, errors.New("native identity mismatch")
	}
	t.cwd = strings.TrimSpace(state.CWD)
	if last := kimiTime(state.UpdatedAt); last.After(t.last) {
		t.last = last
	}

	now := time.Now()
	var (
		items       []tagged
		prompted    bool
		pendingText strings.Builder
		pendingTurn = -1
		pendingAt   time.Time
	)
	note := func(at time.Time) {
		if at.After(t.last) && at.Before(now.Add(time.Hour)) {
			t.last = at
		}
	}
	appendMessage := func(role, text string, at time.Time) {
		if strings.Contains(text, omittedText) {
			t.shortened, text = true, strings.ReplaceAll(text, omittedText, "")
		}
		if strings.TrimSpace(text) == "" {
			return
		}
		items = append(items, tagged{Message: Message{Role: domain.MessageRole(role), Text: text, At: at}})
	}
	flushAssistant := func() {
		if pendingTurn < 0 {
			return
		}
		text := pendingText.String()
		pendingText.Reset()
		pendingTurn = -1
		appendMessage("assistant", text, pendingAt)
	}
	handle := func(line []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var r kimiRecord
		if err := json.Unmarshal(line, &r); err != nil {
			return nil //nolint:nilerr // A torn or foreign record does not hide the rest of the conversation.
		}
		at := kimiTime(r.Time)
		note(at)
		switch r.Type {
		case "context.append_message":
			if r.Message.Role != "user" || kimiOriginKind(r.Message.Origin) != "user" {
				// Drop injected context: environment disclosures, skill expansions
				// and other non-typed user-role turns so titles are real prompts.
				return nil
			}
			flushAssistant()
			text := kimiBlockText(r.Message.Content)
			if strings.TrimSpace(text) != "" {
				prompted = true
			}
			appendMessage("user", text, at)
		case "context.append_loop_event":
			if r.Event.Type != "content.part" || r.Event.Part.Type != "text" {
				// Reasoning ("think"), tool calls and tool results are not messages.
				return nil
			}
			if pendingTurn >= 0 && pendingTurn != r.Event.TurnID {
				flushAssistant()
			}
			if pendingTurn < 0 {
				pendingTurn, pendingAt = r.Event.TurnID, at
			}
			pendingText.WriteString(r.Event.Part.Text)
		}
		return nil
	}
	if err := readOne(providerRoot, root, wirePath, handle); err != nil {
		return t, err
	}
	flushAssistant()

	n := len(items)
	items = trimVisible(items)
	t.shortened = t.shortened || len(items) < n
	for _, item := range items {
		t.messages = append(t.messages, item.Message)
	}

	if !prompted || len(t.messages) == 0 {
		return t, errors.New("no readable native conversation")
	}
	if t.cwd != "" && !filepath.IsAbs(t.cwd) {
		return t, errors.New("transcript working directory must be absolute")
	}
	if t.last.IsZero() {
		if info, err := os.Stat(t.path); err == nil {
			t.last = info.ModTime()
		}
	}
	for i := range t.messages {
		if t.messages[i].At.IsZero() {
			t.messages[i].At = t.last
		}
	}
	t.title = kimiTitle(state.Title, t.messages)
	return t, nil
}

// readKimiState reads the session's state.json sidecar. A missing or malformed
// sidecar is not fatal: the wire.jsonl still yields messages, title and cwd.
func readKimiState(providerRoot *os.Root, root, wirePath string) kimiState {
	var state kimiState
	statePath := filepath.Join(kimiSessionDir(wirePath), kimiStateFile)
	rel, err := filepath.Rel(root, statePath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return state
	}
	f, err := providerRoot.Open(rel)
	if err != nil {
		return state
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, kimiStateBudget))
	if err != nil {
		return state
	}
	_ = json.Unmarshal(data, &state)
	return state
}

// kimiOriginKind reads just the discriminating "kind" from a message origin,
// which may be a bare string or an object.
func kimiOriginKind(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return obj.Kind
	}
	return ""
}

// kimiBlockText joins a message's visible text blocks, as blockText does for the
// other harnesses, so a huge tool result parsed into the record is still dropped.
func kimiBlockText(blocks []kimiBlock) string {
	converted := make([]textBlock, len(blocks))
	for i, b := range blocks {
		converted[i] = textBlock{Type: b.Type, Text: b.Text}
	}
	return blockText(converted)
}

// kimiTitle prefers the session's own title, falling back to the opening prompt.
func kimiTitle(stateTitle string, messages []Message) string {
	title := strings.TrimSpace(stateTitle)
	if title == "" {
		for _, m := range messages {
			if m.Role == domain.MessageRoleUser {
				title = m.Text
				break
			}
		}
	}
	title = strings.Join(strings.Fields(title), " ")
	if runes := []rune(title); len(runes) > 100 {
		return string(runes[:100]) + "…"
	}
	return title
}

// kimiTime converts Kimi's epoch-millisecond timestamps to a time.Time; zero
// stays zero so the file modtime can stand in later.
func kimiTime(millis int64) time.Time {
	if millis <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(millis).UTC()
}
