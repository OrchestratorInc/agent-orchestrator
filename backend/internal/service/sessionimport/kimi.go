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
	"slices"
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

// kimiRecord is one wire.jsonl line. Typed user prompts (append_message with a
// user/absent origin) and streamed assistant text (content.part of type text)
// are display messages; everything else is tool noise, reasoning, injected
// context, or provider bookkeeping. Origin, count and the compaction record also
// drive context.undo so AO retracts exactly the turns Kimi would.
type kimiRecord struct {
	Type string `json:"type"`
	Time int64  `json:"time"`
	// Count is the retracted typed-prompt turn count on a context.undo record.
	Count   int `json:"count"`
	Message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		Origin  json.RawMessage `json:"origin"`
	} `json:"message"`
	Event struct {
		Type string `json:"type"`
		Part struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"part"`
	} `json:"event"`
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
		items       []kimiItem
		pendingText strings.Builder
		pending     bool
		pendingAt   time.Time
	)
	// held is the visible text kept so far. Past maxClaudeHeld the oldest text
	// is dropped while reading, as Claude and Pi do, so a huge wire.jsonl is not
	// held whole; the opening prompt (first) stays.
	held, drop, first := 0, 0, -1
	// hold accounts for the visible item just appended and drops the oldest
	// text over the bound. A dropped item stays as a hidden entry, so undo
	// anchors and compaction boundaries keep their counts.
	hold := func() {
		last := len(items) - 1
		if first < 0 {
			first = last
		}
		held += len(items[last].msg.Text)
		for ; held > maxClaudeHeld && drop < len(items); drop++ {
			if drop != first && !items[drop].hidden {
				held -= len(items[drop].msg.Text)
				items[drop].msg.Text, items[drop].hidden = "", true
				t.shortened = true
			}
		}
	}
	// recount follows a clear or undo that removed newer items.
	recount := func() {
		held, drop = 0, min(drop, len(items))
		if first >= len(items) {
			first = -1
		}
		for _, item := range items {
			if !item.hidden {
				held += len(item.msg.Text)
			}
		}
	}
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
		items = append(items, kimiItem{msg: Message{Role: domain.MessageRole(role), Text: text, At: at}})
		hold()
	}
	// A turn's assistant text streams as many content.part events, possibly split
	// across tool-use steps. Grouping assistant text on the turn boundary avoids
	// depending on the turnId field, whose JSON type varies across Kimi versions.
	flushAssistant := func() {
		if !pending {
			return
		}
		text := pendingText.String()
		pendingText.Reset()
		pending = false
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
			if r.Message.Role != "user" {
				return nil
			}
			origin, hadOrigin := parseKimiOrigin(r.Message.Origin)
			anchor := isKimiUndoAnchor(origin, hadOrigin)
			switch {
			case !hadOrigin || origin.Kind == "user":
				// A typed prompt. An in-turn steer keeps origin.kind "user" but
				// carries inTurn, so it still shows yet is not an undo anchor.
				flushAssistant()
				text := visibleText(r.Message.Content)
				if strings.Contains(text, omittedText) {
					t.shortened, text = true, strings.ReplaceAll(text, omittedText, "")
				}
				switch {
				case strings.TrimSpace(text) != "":
					items = append(items, kimiItem{msg: Message{Role: domain.MessageRoleUser, Text: text, At: at}, anchor: anchor})
					hold()
				case anchor:
					// A typed prompt with no visible text (image/attachment-only, or
					// only the omitted marker) still anchors undo in Kimi; keep it
					// hidden so undo counts match even without text to show.
					items = append(items, kimiItem{anchor: true, hidden: true})
				}
			case anchor:
				// A user-slash skill or plugin command: an undo anchor, but its
				// content is an expansion, so it is tracked hidden, not shown.
				flushAssistant()
				items = append(items, kimiItem{anchor: true, hidden: true})
			default:
				// Injected context and other non-typed user turns are not messages
				// and not undo anchors, so titles stay the user's real prompts.
			}
		case "context.append_loop_event":
			if r.Event.Type != "content.part" || r.Event.Part.Type != "text" {
				// Reasoning ("think"), tool calls and tool results are not messages.
				return nil
			}
			if !pending {
				pending, pendingAt = true, at
			}
			pendingText.WriteString(r.Event.Part.Text)
		case "context.apply_compaction":
			// Kimi never undoes past a compaction; record a hidden boundary so a
			// later context.undo stops here, as its reducer does.
			flushAssistant()
			items = append(items, kimiItem{compaction: true, hidden: true})
		case "context.clear":
			// Kimi resets the conversation here (its reducer sets clearFloor to the
			// current length); a user who ran /clear and started over must import
			// only what followed, so its session/load replay and this history agree.
			flushAssistant()
			items = items[:0]
			recount()
		case "context.undo":
			// Kimi retracts the last `count` undo anchors; mirror its applyUndo.
			flushAssistant()
			items = dropLastKimiTurns(items, r.Count)
			recount()
		}
		return nil
	}
	if err := readOne(providerRoot, root, wirePath, handle); err != nil {
		return t, err
	}
	flushAssistant()

	// Drop the hidden anchor and compaction markers; only real messages display.
	display := make([]tagged, 0, len(items))
	for _, item := range items {
		if item.hidden || item.msg.Role == "" {
			continue
		}
		display = append(display, tagged{Message: item.msg})
	}
	n := len(display)
	display = trimVisible(display)
	t.shortened = t.shortened || len(display) < n
	for _, item := range display {
		t.messages = append(t.messages, item.Message)
	}

	// A conversation needs at least one surviving typed prompt; a session that
	// was fully cleared or undone, or held only injected context, is not one.
	if !slices.ContainsFunc(t.messages, func(m Message) bool { return m.Role == domain.MessageRoleUser }) {
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
	// The root is canonical, while a discovered path may still use a home
	// alias (including macOS's /var -> /private/var). Compare physical paths,
	// retaining os.Root's confinement for the sidecar open.
	statePath, err := filepath.EvalSymlinks(statePath)
	if err != nil {
		return state
	}
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

// kimiItem is one parsed wire.jsonl event. A displayed item carries msg; hidden
// items are undo anchors (user-slash skill/plugin activations) or a compaction
// boundary that only matter to context.undo, never to the shown history.
type kimiItem struct {
	msg        Message
	anchor     bool
	hidden     bool
	compaction bool
}

// kimiOrigin is the discriminating part of a message's origin.
type kimiOrigin struct {
	Kind    string `json:"kind"`
	InTurn  bool   `json:"inTurn"`
	Trigger string `json:"trigger"`
}

// parseKimiOrigin decodes a message origin, which may be an object or, in some
// versions, a bare kind string. hadOrigin is false when the field is absent,
// which Kimi treats as a typed-prompt undo anchor.
func parseKimiOrigin(raw json.RawMessage) (kimiOrigin, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return kimiOrigin{}, false
	}
	var obj kimiOrigin
	if json.Unmarshal(raw, &obj) == nil {
		return obj, true
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return kimiOrigin{Kind: s}, true
	}
	return kimiOrigin{}, true
}

// isKimiUndoAnchor mirrors Kimi Code's isUndoAnchorOrigin: an in-turn steer is
// never an anchor; a missing origin or origin.kind "user" is; and a user-slash
// skill or plugin command is.
func isKimiUndoAnchor(origin kimiOrigin, hadOrigin bool) bool {
	if hadOrigin && origin.InTurn {
		return false
	}
	if !hadOrigin || origin.Kind == "user" {
		return true
	}
	return (origin.Kind == "skill_activation" || origin.Kind == "plugin_command") && origin.Trigger == "user-slash"
}

// dropLastKimiTurns retracts the last n undo anchors and everything after the
// nth, mirroring Kimi's applyUndo: walk from the end, stop at a compaction
// boundary (keeping it and all before it), and cut at the nth anchor. Fewer than
// n anchors with no compaction means the undo emptied the conversation.
func dropLastKimiTurns(items []kimiItem, n int) []kimiItem {
	if n <= 0 {
		return items
	}
	removed := 0
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].compaction {
			return items[:i+1]
		}
		if items[i].anchor {
			removed++
			if removed >= n {
				return items[:i]
			}
		}
	}
	return items[:0]
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
