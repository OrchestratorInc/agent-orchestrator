// Package sessionimport reads local provider history without starting or modifying a provider.
package sessionimport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Tool output and images dominate provider files, so whole files stream while
// oversized records keep only their short top-level fields.
const (
	maxTranscriptBytes = 1 << 30
	maxVisibleBytes    = 16 << 20
	maxSkimMember      = 4 << 10
)

// Variables so tests can use small records. A string over maxSkimString in an
// oversized record is omitted; it would exceed maxVisibleBytes on its own.
var (
	maxRecordBytes  = 64 << 20
	maxSkimString   = maxVisibleBytes
	nativeIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	// Codex rollouts may be zstd-compressed and split into `<thread>_<segment>` files.
	codexRolloutName = regexp.MustCompile(`^rollout-.*-([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})(?:_[0-9a-fA-F-]{36})?\.jsonl(?:\.zst)?$`)
	// Codex injects these as separate user-role items; they are not prompts.
	codexContextPrefixes = []string{"# AGENTS.md instructions", "<environment_context", "<user_instructions", "<INSTRUCTIONS>", "<external_codex_apps_open_page", "<recommended_plugins", "<skill>", "<codex_internal_context", "<turn_aborted"}
)

// Message is the readable archive; tool payloads and provider internal state are not display messages.
type Message struct {
	Role domain.MessageRole
	Text string
	At   time.Time
}

type transcript struct {
	harness                   domain.AgentHarness
	root, path, nativeID, cwd string
	paths                     []string
	title                     string
	messages                  []Message
	last                      time.Time
	// shortened reports visible text that was too large to keep.
	shortened bool
}

type textBlock struct{ Type, Text string }

// contentText decodes message content straight to its visible text, so large
// tool results are parsed but never retained.
type contentText string

func (c *contentText) UnmarshalJSON(raw []byte) error {
	*c = contentText(visibleText(raw))
	return nil
}

type record struct {
	Type              string    `json:"type"`
	UUID              string    `json:"uuid"`
	ParentUUID        string    `json:"parentUuid"`
	LogicalParentUUID string    `json:"logicalParentUuid"`
	SessionID         string    `json:"sessionId"`
	CWD               string    `json:"cwd"`
	Timestamp         time.Time `json:"timestamp"`
	Sidechain         bool      `json:"isSidechain"`
	Meta              bool      `json:"isMeta"`
	CompactSummary    bool      `json:"isCompactSummary"`
	Omitted           bool      `json:"aoOmitted"`
	AITitle           string    `json:"aiTitle"`
	CustomTitle       string    `json:"customTitle"`
	Message           struct {
		ID      string      `json:"id"`
		Role    string      `json:"role"`
		Content contentText `json:"content"`
	} `json:"message"`
	Payload struct {
		ID      string          `json:"id"`
		CWD     string          `json:"cwd"`
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Message string          `json:"message"`
		Content contentText     `json:"content"`
		Source  json.RawMessage `json:"source"`
		Item    struct {
			Type    string      `json:"type"`
			ID      string      `json:"id"`
			Content []textBlock `json:"content"`
		} `json:"item"`
	} `json:"payload"`
}

func visibleText(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var blocks []textBlock
	if json.Unmarshal(content, &blocks) != nil {
		return ""
	}
	return blockText(blocks)
}

func blockText(blocks []textBlock) string {
	var parts []string
	for _, b := range blocks {
		switch strings.ToLower(b.Type) {
		case "text", "input_text", "output_text":
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// codexPrompt removes Codex Desktop's ambient wrapper and drops injected context.
func codexPrompt(text string) string {
	if i := strings.LastIndex(text, "## My request:"); i >= 0 {
		return strings.TrimSpace(text[i+len("## My request:"):])
	}
	trimmed := strings.TrimSpace(text)
	for _, prefix := range codexContextPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return ""
		}
	}
	return text
}

// claudePrompt shows a slash command as typed and drops its local output.
func claudePrompt(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "<local-command-stdout>") || strings.HasPrefix(trimmed, "<local-command-stderr>") {
		return "", true
	}
	between := func(start, end string) string {
		_, rest, ok := strings.Cut(trimmed, start)
		value, _, _ := strings.Cut(rest, end)
		if !ok {
			return ""
		}
		return strings.TrimSpace(value)
	}
	name := between("<command-name>", "</command-name>")
	if name == "" {
		return text, false
	}
	if args := between("<command-args>", "</command-args>"); args != "" {
		name += " " + args
	}
	return name, true
}

// codexThreadID is the rollout's own identity; copied fork ancestry never replaces it.
func codexThreadID(path string) string {
	if m := codexRolloutName.FindStringSubmatch(filepath.Base(path)); m != nil {
		return strings.ToLower(m[1])
	}
	return ""
}

// eachRecord streams JSONL. A record too large to decode is rewritten as it
// streams past, so a huge tool result neither hides the text beside it nor
// breaks the parent link that joins the history before it.
func eachRecord(r io.Reader, fn func([]byte) error) error {
	br := bufio.NewReaderSize(r, 256<<10)
	var (
		line      []byte
		skim      recordSkim
		oversized bool
	)
	for {
		chunk, err := br.ReadSlice('\n')
		if !oversized && len(line)+len(chunk) > maxRecordBytes {
			oversized = true
			skim.reset()
			skim.write(line)
			line = nil
		}
		if oversized {
			skim.write(chunk)
		} else {
			line = append(line, chunk...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		record := line
		if oversized {
			record = skim.record()
		}
		if len(bytes.TrimSpace(record)) > 0 {
			if fnErr := fn(record); fnErr != nil {
				return fnErr
			}
		}
		line, oversized = line[:0], false
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// recordSkim rewrites one oversized JSON record: a string over maxSkimString
// becomes omittedText. If the rest is still over maxRecordBytes, only the
// short top-level scalars are kept, marked aoOmitted.
type recordSkim struct {
	full, top, member  []byte
	stringStart, depth int

	inString, escape, longString, drop, overflow, closed bool
}

const omittedText = "\x00omitted\x00"

var omittedJSON = []byte(`"\u0000omitted\u0000"`)

func (s *recordSkim) reset() {
	*s = recordSkim{top: append(s.top[:0], '{'), member: s.member[:0]}
}

func (s *recordSkim) write(p []byte) {
	for _, c := range p {
		if s.closed {
			return
		}
		s.copyFull(c)
		switch {
		case s.inString:
			switch {
			case s.escape:
				s.escape = false
			case c == '\\':
				s.escape = true
			case c == '"':
				s.inString = false
			}
		case c == '"':
			s.inString = true
		case c == '{' || c == '[':
			s.depth++
			if s.depth == 1 {
				continue
			}
			s.drop = true
		case c == '}' || c == ']':
			s.depth--
			if s.depth == 0 {
				s.keep()
				s.closed = true
				continue
			}
		case c == ',' && s.depth == 1:
			s.keep()
			continue
		}
		if s.depth != 1 || s.drop {
			continue
		}
		if len(s.member) >= maxSkimMember {
			s.drop = true
			continue
		}
		s.member = append(s.member, c)
	}
}

// copyFull runs before write updates the string state for c.
func (s *recordSkim) copyFull(c byte) {
	if s.overflow {
		return
	}
	closing := s.inString && !s.escape && c == '"'
	switch {
	case !s.inString && c == '"':
		s.stringStart, s.longString = len(s.full), false
	case s.inString && !s.longString && len(s.full)-s.stringStart > maxSkimString:
		s.full, s.longString = s.full[:s.stringStart], true
	}
	if s.longString {
		if closing {
			s.full, s.longString = append(s.full, omittedJSON...), false
		}
		return
	}
	s.full = append(s.full, c)
	if len(s.full) > maxRecordBytes {
		s.full, s.overflow = nil, true
	}
}

func (s *recordSkim) keep() {
	if m := bytes.TrimSpace(s.member); len(m) > 0 && !s.drop {
		if len(s.top) > 1 {
			s.top = append(s.top, ',')
		}
		s.top = append(s.top, m...)
	}
	s.member, s.drop = s.member[:0], false
}

// record is nil for a torn object, as a torn small record fails to decode.
func (s *recordSkim) record() []byte {
	switch {
	case !s.closed:
		return nil
	case !s.overflow:
		return s.full
	case len(s.top) > 1:
		s.top = append(s.top, ',')
	}
	return append(s.top, `"aoOmitted":true}`...)
}

type tagged struct {
	Message
	kind string
	id   string
}

// claudeNode keeps only display facts, so tool payloads are not retained while walking ancestry.
type claudeNode struct {
	typ, role, id, uuid, parent, logicalParent, cwd, text string
	at                                                    time.Time
	hidden, omitted                                       bool
}

// readTranscript reads one native conversation; Codex segments are passed oldest first.
func readTranscript(ctx context.Context, harness domain.AgentHarness, root string, paths ...string) (transcript, error) {
	t := transcript{harness: harness, root: root}
	if len(paths) == 0 {
		return t, errors.New("no transcript")
	}
	root = canonical(root)
	providerRoot, err := os.OpenRoot(root)
	if err != nil {
		return t, err
	}
	defer func() { _ = providerRoot.Close() }()
	t.root = root
	switch harness {
	case domain.HarnessClaudeCode:
		t.nativeID = strings.TrimSuffix(filepath.Base(paths[0]), ".jsonl")
	case domain.HarnessCodex:
		t.nativeID = codexThreadID(paths[0])
	}
	var (
		items       []tagged
		ids         = map[string]int{}
		claude      []claudeNode
		claudeIndex = map[string]int{}
		claudeLeaf  string
		customTitle string
		prompted    bool
		sawMeta     bool
		visible     int
	)
	now := time.Now()
	add := func(kind, id, role, text string, at time.Time) {
		if role != "user" && role != "assistant" {
			return
		}
		if strings.Contains(text, omittedText) {
			t.shortened, text = true, strings.ReplaceAll(text, omittedText, "")
		}
		if strings.TrimSpace(text) == "" {
			return
		}
		msg := tagged{Message: Message{Role: domain.MessageRole(role), Text: text, At: at}, kind: kind, id: id}
		if id != "" {
			// Streaming providers rewrite one message under the same id; keep its first position.
			if index, ok := ids[kind+"\x00"+id]; ok {
				if kind == "claude" {
					// Claude writes each content block of one reply as its own record.
					msg.Text, msg.At = items[index].Text+"\n\n"+text, items[index].At
				}
				visible += len(msg.Text) - len(items[index].Text)
				items[index] = msg
				return
			}
			ids[kind+"\x00"+id] = len(items)
		}
		visible += len(text)
		items = append(items, msg)
	}
	trim := func() {
		n := len(items)
		items = trimVisible(items)
		t.shortened = t.shortened || len(items) < n
	}
	handle := func(line []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var r record
		if err := json.Unmarshal(line, &r); err != nil {
			return nil //nolint:nilerr // A torn or foreign record does not hide the rest of the conversation.
		}
		if r.Timestamp.After(t.last) && r.Timestamp.Before(now.Add(time.Hour)) {
			t.last = r.Timestamp
		}
		if harness == domain.HarnessClaudeCode {
			if r.Sidechain {
				return nil
			}
			if r.SessionID != "" && r.SessionID != t.nativeID {
				return errors.New("native identity mismatch")
			}
			if title := strings.TrimSpace(r.CustomTitle); title != "" {
				customTitle = title
			}
			if title := strings.TrimSpace(r.AITitle); title != "" {
				t.title = title
			}
			node := claudeNode{typ: r.Type, role: r.Message.Role, id: r.Message.ID, uuid: r.UUID, parent: r.ParentUUID, logicalParent: r.LogicalParentUUID, cwd: r.CWD, at: r.Timestamp, hidden: r.Meta || r.CompactSummary, omitted: r.Omitted}
			if r.Type == "user" || r.Type == "assistant" {
				node.text = string(r.Message.Content)
			}
			if r.UUID != "" {
				claudeLeaf = r.UUID
				if index, rewritten := claudeIndex[r.UUID]; rewritten {
					claude[index] = node // Claude rewrites some records in place, for example after a cwd change.
					return nil
				}
				claudeIndex[r.UUID] = len(claude)
			}
			claude = append(claude, node)
			return nil
		}
		if r.Omitted && (r.Type == "response_item" || r.Type == "event_msg") {
			t.shortened = true // The dropped payload may have held visible text.
		}
		switch r.Type {
		case "session_meta":
			if sawMeta {
				return nil // Forks and later segments repeat ancestor metadata.
			}
			sawMeta = true
			if !strings.EqualFold(r.Payload.ID, t.nativeID) {
				return errors.New("native identity mismatch")
			}
			var source map[string]json.RawMessage
			if json.Unmarshal(r.Payload.Source, &source) == nil && source["subagent"] != nil {
				return errors.New("subagent rollouts belong to their parent conversation")
			}
			t.cwd = r.Payload.CWD
		case "response_item":
			if r.Payload.Type == "message" {
				add("response-"+r.Payload.Role, r.Payload.ID, r.Payload.Role, string(r.Payload.Content), r.Timestamp)
			}
		case "event_msg":
			switch r.Payload.Type {
			case "user_message":
				add("event-user", "", "user", r.Payload.Message, r.Timestamp)
			case "agent_message":
				add("event-assistant", "", "assistant", r.Payload.Message, r.Timestamp)
			case "item_completed":
				switch r.Payload.Item.Type {
				case "UserMessage":
					add("item-user", r.Payload.Item.ID, "user", blockText(r.Payload.Item.Content), r.Timestamp)
				case "AgentMessage":
					add("item-assistant", r.Payload.Item.ID, "assistant", blockText(r.Payload.Item.Content), r.Timestamp)
				}
			}
		}
		if visible > 2*maxVisibleBytes {
			trim()
			visible = 0
			clear(ids)
			for i, item := range items {
				visible += len(item.Text)
				if item.id != "" {
					ids[item.kind+"\x00"+item.id] = i
				}
			}
		}
		return nil
	}
	for _, path := range paths {
		if err := readOne(providerRoot, root, path, handle); err != nil {
			return t, err
		}
		t.paths = append(t.paths, path)
	}
	t.path = t.paths[len(t.paths)-1]
	if harness == domain.HarnessCodex && !sawMeta {
		return t, errors.New("no readable native conversation")
	}
	chain := claude
	if claudeLeaf != "" {
		// Follow the active branch so rewound prompts are not shown as history.
		chain = nil
		visited := map[string]bool{}
		for claudeLeaf != "" {
			index, found := claudeIndex[claudeLeaf]
			if !found || visited[claudeLeaf] {
				break // Show the reachable branch; a dangling parent must not hide the conversation.
			}
			visited[claudeLeaf] = true
			node := claude[index]
			chain = append(chain, node)
			claudeLeaf = node.parent
			if claudeLeaf == "" {
				// Compaction starts a new root; its logical parent continues the visible history.
				if _, found := claudeIndex[node.logicalParent]; found {
					claudeLeaf = node.logicalParent
				}
			}
		}
		slices.Reverse(chain)
	}
	for _, node := range chain {
		if t.cwd == "" {
			t.cwd = node.cwd
		}
		if (node.typ != "user" && node.typ != "assistant") || node.hidden {
			continue
		}
		t.shortened = t.shortened || node.omitted
		role := node.role
		if role == "" {
			role = node.typ
		}
		id := node.uuid
		if node.id != "" {
			id = node.id
		}
		text := node.text
		if role == "user" {
			var command bool
			text, command = claudePrompt(text)
			prompted = prompted || !command && strings.TrimSpace(text) != ""
		}
		add("claude", id, role, text, node.at)
	}
	if customTitle != "" {
		t.title = customTitle
	}
	if harness == domain.HarnessCodex {
		items = codexVisible(items)
		prompted = true
	}
	trim()
	for _, item := range items {
		t.messages = append(t.messages, item.Message)
	}
	// A session of only slash commands, such as /login, is not a conversation to continue.
	if !nativeIDPattern.MatchString(t.nativeID) || !prompted || !slices.ContainsFunc(t.messages, func(m Message) bool { return m.Role == domain.MessageRoleUser }) {
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
	if t.title == "" {
		for _, m := range t.messages {
			if m.Role == domain.MessageRoleUser {
				t.title = m.Text
				break
			}
		}
	}
	t.title = strings.Join(strings.Fields(t.title), " ")
	if runes := []rune(t.title); len(runes) > 100 {
		t.title = string(runes[:100]) + "…"
	}
	return t, nil
}

// codexVisible keeps one representation per role: Codex writes prompts as
// events and/or response items, and only events exclude injected context.
func codexVisible(items []tagged) []tagged {
	pick := func(kinds ...string) string {
		for _, kind := range kinds {
			if slices.ContainsFunc(items, func(i tagged) bool { return i.kind == kind }) {
				return kind
			}
		}
		return ""
	}
	user := pick("event-user", "item-user", "response-user")
	assistant := pick("response-assistant", "event-assistant", "item-assistant")
	out := items[:0]
	for _, item := range items {
		if item.kind != user && item.kind != assistant {
			continue
		}
		if item.Role == domain.MessageRoleUser {
			if item.Text = codexPrompt(item.Text); strings.TrimSpace(item.Text) == "" {
				continue
			}
		}
		out = append(out, item)
	}
	return out
}

// trimVisible keeps the opening prompt and the newest messages within budget.
func trimVisible(items []tagged) []tagged {
	total := 0
	for _, item := range items {
		total += len(item.Text)
	}
	for len(items) > 2 && total > maxVisibleBytes {
		total -= len(items[1].Text)
		items = slices.Delete(items, 1, 2)
	}
	return items
}

// readOne reads a file as it was when opened; appended records belong to the next scan.
func readOne(providerRoot *os.Root, root, path string, fn func([]byte) error) error {
	leaf, err := os.Lstat(path)
	if err != nil || !leaf.Mode().IsRegular() {
		return errors.New("transcript is not a regular file")
	}
	// Never follow a provider-file symlink out of the explicitly scanned root.
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, physical)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("transcript is outside provider home")
	}
	f, err := providerRoot.Open(rel)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() > maxTranscriptBytes {
		return errors.New("transcript exceeds read budget or is not a file")
	}
	r := io.LimitReader(f, before.Size())
	if strings.HasSuffix(path, ".zst") {
		decoder, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(maxTranscriptBytes))
		if err != nil {
			return err
		}
		defer decoder.Close()
		r = decoder
	}
	if err := eachRecord(r, fn); err != nil {
		return err
	}
	current, err := os.Stat(path)
	if err != nil || !os.SameFile(before, current) || current.Size() < before.Size() {
		return errors.New("transcript changed while reading; scan again")
	}
	return nil
}
