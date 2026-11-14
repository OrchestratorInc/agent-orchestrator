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

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	maxTranscriptBytes = 4 << 30
	maxVisibleBytes    = 32 << 20
	maxSkimMember      = 4 << 10
)

var (
	maxRecordBytes       = 64 << 20
	maxSkimString        = maxVisibleBytes
	maxHeldText          = 2 * maxVisibleBytes
	nativeIDPattern      = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	codexContextPrefixes = []string{"# AGENTS.md instructions", "<environment_context", "<user_instructions", "<INSTRUCTIONS>", "<external_codex_apps_open_page", "<recommended_plugins", "<skill>", "<codex_internal_context", "<turn_aborted"}
)

var (
	errNativeIdentityMismatch = errors.New("native identity mismatch")
	errNoReadableConversation = errors.New("no readable native conversation")
	errRelativeCWD            = errors.New("transcript working directory must be absolute")
)

type Message = domain.ImportedMessage

type transcript struct {
	harness                   domain.AgentHarness
	root, path, nativeID, cwd string
	paths                     []string
	title                     string
	messages                  []Message
	last                      time.Time
	shortened                 bool
	fingerprint               string
	placeAt                   string
}

func (t transcript) folder() string {
	if t.placeAt != "" {
		if info, err := os.Stat(t.cwd); err != nil || !info.IsDir() {
			return t.placeAt
		}
	}
	return t.cwd
}

type textBlock struct{ Type, Text string }

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
		NumTurns uint64 `json:"num_turns"`

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
	if !strings.HasPrefix(trimmed, "<command-name>") && !strings.HasPrefix(trimmed, "<command-message>") {
		return text, false
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

type claudeNode struct {
	typ, role, id, uuid, parent, logicalParent, cwd, text string
	at                                                    time.Time
	hidden, omitted, dropped                              bool
}

func readTranscript(ctx context.Context, harness domain.AgentHarness, root string, paths ...string) (transcript, error) {
	if read := harnessImporters[harness].read; read != nil {
		return read(ctx, root, paths...)
	}
	return transcript{harness: harness, root: root}, errors.New("no transcript reader")
}

func readClaudeTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	return readJSONLTranscript(ctx, domain.HarnessClaudeCode, root, paths...)
}

func readCodexTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	return readJSONLTranscript(ctx, domain.HarnessCodex, root, paths...)
}

func readJSONLTranscript(ctx context.Context, harness domain.AgentHarness, root string, paths ...string) (transcript, error) {
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
		t.nativeID = nativeconfig.CodexRolloutThread(paths[0])
	}
	var (
		items       []tagged
		ids         = map[string]int{}
		claude      []claudeNode
		claudeIndex = map[string]int{}
		claudeLeaf  string
		claudeHeld  int
		claudeFirst = -1
		claudeDrop  int
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
		if harness == domain.HarnessCodex && role == "user" {
			text = codexPrompt(text)
		}
		if strings.TrimSpace(text) == "" {
			return
		}
		msg := tagged{Message: Message{Role: domain.MessageRole(role), Text: text, At: at}, kind: kind, id: id}
		if id != "" {
			if index, ok := ids[kind+"\x00"+id]; ok {
				if kind == "claude" {
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
	reindex := func() {
		visible = 0
		clear(ids)
		for i, item := range items {
			visible += len(item.Text)
			if item.id != "" {
				ids[item.kind+"\x00"+item.id] = i
			}
		}
	}
	trim := func() {
		if harness == domain.HarnessCodex {
			items = codexVisible(items)
		}
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
			own := r.SessionID == "" || r.SessionID == t.nativeID
			if title := strings.TrimSpace(r.CustomTitle); title != "" && own {
				customTitle = title
			}
			if title := strings.TrimSpace(r.AITitle); title != "" && own {
				t.title = title
			}
			node := claudeNode{typ: r.Type, role: r.Message.Role, id: r.Message.ID, uuid: r.UUID, parent: r.ParentUUID, logicalParent: r.LogicalParentUUID, cwd: r.CWD, at: r.Timestamp, hidden: r.Meta || r.CompactSummary, omitted: r.Omitted}
			if r.Type == "user" || r.Type == "assistant" {
				node.text = string(r.Message.Content)
			}
			index := len(claude)
			if r.UUID != "" {
				claudeLeaf = r.UUID
				if at, rewritten := claudeIndex[r.UUID]; rewritten {
					index = at
				} else {
					claudeIndex[r.UUID] = index
				}
			}
			if index < len(claude) {
				claudeHeld -= len(claude[index].text)
				claude[index] = node
			} else {
				claude = append(claude, node)
			}
			if claudeFirst < 0 && node.text != "" {
				claudeFirst = index
			}
			if index < claudeDrop && index != claudeFirst {
				claude[index].text, claude[index].dropped = "", node.text != ""
			}
			claudeHeld += len(claude[index].text)
			dropOldestHeld(claude, &claudeHeld, &claudeDrop, claudeFirst, func(n *claudeNode) (*string, *bool) { return &n.text, &n.dropped })
			return nil
		}
		if r.Omitted && (r.Type == "response_item" || r.Type == "event_msg") {
			t.shortened = true
		}
		switch r.Type {
		case "session_meta":
			if sawMeta {
				return nil
			}
			sawMeta = true
			if !strings.EqualFold(r.Payload.ID, t.nativeID) {
				return errNativeIdentityMismatch
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
			case "thread_rolled_back":
				if r.Payload.NumTurns > 0 {
					items = codexRollback(items, r.Payload.NumTurns)
					reindex()
				}
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
			reindex()
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
		return t, errNoReadableConversation
	}
	chain := claude
	if claudeLeaf != "" {
		chain = activeBranch(claude, claudeIndex, claudeLeaf, func(n claudeNode) string {
			if _, found := claudeIndex[n.logicalParent]; n.parent == "" && found {
				return n.logicalParent
			}
			return n.parent
		})
	}
	for _, node := range chain {
		if t.cwd == "" {
			t.cwd = node.cwd
		}
		if (node.typ != "user" && node.typ != "assistant") || node.hidden {
			continue
		}
		t.shortened = t.shortened || node.omitted || node.dropped
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
		prompted = true
	}
	trim()
	for _, item := range items {
		t.messages = append(t.messages, item.Message)
	}
	if !nativeIDPattern.MatchString(t.nativeID) || !prompted || !slices.ContainsFunc(t.messages, func(m Message) bool { return m.Role == domain.MessageRoleUser }) {
		return t, errNoReadableConversation
	}
	finishErr := finishTranscript(&t)
	return t, finishErr
}

func finishTranscript(t *transcript) error {
	if t.cwd != "" && !filepath.IsAbs(t.cwd) {
		return errRelativeCWD
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
	t.title = importTitle(t.title)
	return nil
}

func activeBranch[N any](nodes []N, index map[string]int, leaf string, parent func(N) string) []N {
	var chain []N
	visited := map[string]bool{}
	for leaf != "" && !visited[leaf] {
		i, found := index[leaf]
		if !found {
			break
		}
		visited[leaf] = true
		chain = append(chain, nodes[i])
		leaf = parent(nodes[i])
	}
	slices.Reverse(chain)
	return chain
}

func dropOldestHeld[N any](nodes []N, held, drop *int, first int, fields func(*N) (text *string, dropped *bool)) {
	for ; *held > maxHeldText && *drop < len(nodes); *drop++ {
		if text, dropped := fields(&nodes[*drop]); *drop != first && *text != "" {
			*held -= len(*text)
			*text, *dropped = "", true
		}
	}
}

func codexVisible(items []tagged) []tagged {
	preference := map[string]int{
		"event-user": 0, "item-user": 1, "response-user": 2,
		"response-assistant": 0, "event-assistant": 1, "item-assistant": 2,
	}
	out := items[:0]
	for start := 0; start < len(items); {
		end := start + 1
		for end < len(items) && items[end].Role == items[start].Role {
			end++
		}
		best := -1
		for _, item := range items[start:end] {
			if rank, ok := preference[item.kind]; ok && (best < 0 || rank < best) {
				best = rank
			}
		}
		for _, item := range items[start:end] {
			if rank, ok := preference[item.kind]; ok && rank == best {
				out = append(out, item)
			}
		}
		start = end
	}
	return out
}

func codexRollback(items []tagged, turns uint64) []tagged {
	items = codexVisible(items)
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Role == domain.MessageRoleUser {
			turns--
			if turns == 0 {
				return items[:i]
			}
		}
	}
	return nil
}

func trimVisible(items []tagged) []tagged {
	total := 0
	for _, item := range items {
		total += len(item.Text)
	}
	cut := 1
	for len(items)-cut > 1 && total > maxVisibleBytes {
		total -= len(items[cut].Text)
		cut++
	}
	if cut == 1 {
		return items
	}
	return slices.Delete(items, 1, cut)
}

func readOne(providerRoot *os.Root, root, path string, fn func([]byte) error) error {
	leaf, err := os.Lstat(path)
	if err != nil || !leaf.Mode().IsRegular() {
		return errors.New("transcript is not a regular file")
	}
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
