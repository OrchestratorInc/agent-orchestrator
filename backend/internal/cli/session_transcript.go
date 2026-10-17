package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

const (
	sessionModeTUI = "tui"

	transcriptSource = "ao_conversation"
	transcriptOrder  = "oldest_first"

	transcriptCapDefault = 1000
	transcriptCapMin     = 80
	transcriptCapMax     = 1000000
	transcriptLimitMax   = 500

	workerReportOpen  = "<ao-worker-reports>"
	workerReportClose = "</ao-worker-reports>"

	elisionStrategy = "head_tail"
)

// ansiPattern matches CSI, OSC, and a single-character escape so color codes
// never reach the caller's terminal or spend tokens.
var ansiPattern = regexp.MustCompile(`\x1b(?:\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\][^\x07]*(?:\x07|\x1b\\)|.)`)

type transcriptRequest struct {
	enabled bool
	n       int
	before  int
	cap     int
}

type sessionTranscriptResponse struct {
	Session    sessionDTO          `json:"session"`
	Transcript *transcriptDocument `json:"transcript,omitempty"`
}

type transcriptDocument struct {
	Source          string          `json:"source"`
	ConversationID  string          `json:"conversationId,omitempty"`
	ActiveBranchID  string          `json:"activeBranchId,omitempty"`
	Order           string          `json:"order"`
	Requested       int             `json:"requested"`
	Returned        int             `json:"returned"`
	LatestSequence  int64           `json:"latestSequence,omitempty"`
	OldestSequence  int64           `json:"oldestSequence,omitempty"`
	HasMoreBefore   bool            `json:"hasMoreBefore"`
	Next            *transcriptNext `json:"next,omitempty"`
	Cap             transcriptCap   `json:"cap"`
	TruncatedFields int             `json:"truncatedFields,omitempty"`
	Controller      string          `json:"controller,omitempty"`
	Incomplete      []any           `json:"incomplete,omitempty"`
	Turns           []any           `json:"turns,omitempty"`
	Usage           any             `json:"usage,omitempty"`
	RateLimits      any             `json:"rateLimits,omitempty"`
	CompactedAt     *string         `json:"compactedAt,omitempty"`
	NoConversation  bool            `json:"-"`
	Entries         []any           `json:"entries"`
}

type transcriptNext struct {
	Before  int64  `json:"before"`
	Command string `json:"command"`
}

type transcriptCap struct {
	Unit  string `json:"unit"`
	Value int    `json:"value"`
}

type fieldTruncation struct {
	Field         string `json:"field"`
	OriginalChars int    `json:"originalChars"`
	KeptChars     int    `json:"keptChars"`
	Strategy      string `json:"strategy"`
}

type conversationSnapshotWire struct {
	ConversationID string            `json:"conversationId"`
	ActiveBranchID string            `json:"activeBranchId"`
	Controller     string            `json:"controller"`
	LatestSequence int64             `json:"latestSequence"`
	OldestSequence int64             `json:"oldestSequence"`
	HasMoreBefore  bool              `json:"hasMoreBefore"`
	CompactedAt    *string           `json:"compactedAt"`
	Turns          []json.RawMessage `json:"turns"`
	Messages       []json.RawMessage `json:"messages"`
	Activities     []json.RawMessage `json:"activities"`
	Usage          json.RawMessage   `json:"usage"`
	RateLimits     json.RawMessage   `json:"rateLimits"`
}

func transcriptRequestFrom(cmd *cobra.Command) (transcriptRequest, error) {
	flags := cmd.Flags()
	var req transcriptRequest
	if !flags.Changed("transcript") {
		if flags.Changed("before") {
			return req, usageError{errors.New("--before requires --transcript")}
		}
		if flags.Changed("cap") {
			return req, usageError{errors.New("--cap requires --transcript")}
		}
		return req, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(flags.Lookup("transcript").Value.String()))
	if err != nil || n < 1 || n > transcriptLimitMax {
		return req, usageError{errors.New("--transcript must be an integer from 1 to 500")}
	}
	req.enabled = true
	req.n = n
	req.cap = transcriptCapDefault
	if flags.Changed("before") {
		before, beforeErr := strconv.Atoi(strings.TrimSpace(flags.Lookup("before").Value.String()))
		if beforeErr != nil || before < 1 {
			return req, usageError{errors.New("--before must be an integer >= 1")}
		}
		req.before = before
	}
	if flags.Changed("cap") {
		fieldCap, capErr := strconv.Atoi(strings.TrimSpace(flags.Lookup("cap").Value.String()))
		if capErr != nil || (fieldCap != 0 && (fieldCap < transcriptCapMin || fieldCap > transcriptCapMax)) {
			return req, usageError{errors.New("--cap must be 0 or an integer from 80 to 1000000")}
		}
		req.cap = fieldCap
	}
	return req, nil
}

func transcriptUnavailable(id string) error {
	return fmt.Errorf("session %s runs in Terminal UI mode; AO keeps no durable transcript for it. Use \"ao session get %s\" for status, or open its terminal", id, id)
}

func (c *commandContext) fetchConversationSnapshot(ctx context.Context, id string, req transcriptRequest) (conversationSnapshotWire, error) {
	params := url.Values{}
	params.Set("limit", strconv.Itoa(req.n))
	if req.before > 0 {
		params.Set("beforeSequence", strconv.Itoa(req.before))
	}
	var snap conversationSnapshotWire
	path := apiPath("sessions/"+url.PathEscape(id)+"/conversation", params)
	if err := c.getJSON(ctx, path, &snap); err != nil {
		return conversationSnapshotWire{}, err
	}
	return snap, nil
}

func buildTranscriptView(id, project string, req transcriptRequest, snap conversationSnapshotWire) *transcriptDocument {
	doc := &transcriptDocument{
		Source:         transcriptSource,
		ConversationID: snap.ConversationID,
		ActiveBranchID: snap.ActiveBranchID,
		Order:          transcriptOrder,
		Requested:      req.n,
		LatestSequence: snap.LatestSequence,
		OldestSequence: snap.OldestSequence,
		HasMoreBefore:  snap.HasMoreBefore,
		Cap:            transcriptCap{Unit: "chars", Value: req.cap},
		Controller:     snap.Controller,
		CompactedAt:    snap.CompactedAt,
		Entries:        []any{},
	}
	if snap.ConversationID == "" && len(snap.Messages) == 0 && len(snap.Activities) == 0 && snap.LatestSequence == 0 {
		doc.NoConversation = true
		doc.HasMoreBefore = false
		doc.OldestSequence = 0
		doc.LatestSequence = 0
	}

	turns := decodeObjects(snap.Turns)
	for _, turn := range turns {
		sanitizeTree(turn)
		doc.TruncatedFields += capTree(turn, "", req.cap, "", turn)
	}
	if len(turns) > 0 {
		doc.Turns = make([]any, len(turns))
		for i := range turns {
			doc.Turns[i] = turns[i]
		}
	}
	if usage := decodeValue(snap.Usage); usage != nil {
		doc.Usage = usage
	}
	if limits := decodeValue(snap.RateLimits); limits != nil {
		doc.RateLimits = limits
	}

	entries := make([]map[string]any, 0, len(snap.Messages)+len(snap.Activities))
	entries = append(entries, decodeObjects(snap.Messages)...)
	entries = append(entries, decodeObjects(snap.Activities)...)
	sort.SliceStable(entries, func(i, j int) bool {
		return entrySequence(entries[i]) < entrySequence(entries[j])
	})

	for _, entry := range entries {
		sanitizeTree(entry)
		stripAttachmentPayloads(entry)
		splitWorkerReportField(entry)
		fullCmd := transcriptCommand(id, 1, entrySequence(entry)+1, intPtr(0), project)
		doc.TruncatedFields += capTree(entry, "", req.cap, fullCmd, entry)
		doc.Entries = append(doc.Entries, entry)
	}
	doc.Returned = len(doc.Entries)
	if doc.Returned > 0 && doc.OldestSequence == 0 {
		doc.OldestSequence = entrySequence(entries[0])
	}
	if doc.HasMoreBefore {
		cursor := doc.OldestSequence
		if cursor < 1 && doc.Returned > 0 {
			cursor = entrySequence(entries[0])
		}
		if cursor >= 1 {
			doc.OldestSequence = cursor
			doc.Next = &transcriptNext{
				Before:  cursor,
				Command: transcriptCommand(id, req.n, cursor, nil, project),
			}
		} else {
			doc.HasMoreBefore = false
		}
	}
	doc.Incomplete = incompleteItems(turns, entries)
	return doc
}

func intPtr(v int) *int { return &v }

func decodeObjects(raws []json.RawMessage) []map[string]any {
	out := make([]map[string]any, 0, len(raws))
	for _, raw := range raws {
		if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
			continue
		}
		var obj map[string]any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&obj); err != nil || obj == nil {
			out = append(out, map[string]any{"kind": "unknown", "raw": sanitizeText(string(raw))})
			continue
		}
		out = append(out, obj)
	}
	return out
}

func decodeValue(raw json.RawMessage) any {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil
	}
	return v
}

func entrySequence(entry map[string]any) int64 {
	n, ok := asInt64(entry["sequence"])
	if !ok {
		return 0
	}
	return n
}

func splitWorkerReportField(entry map[string]any) {
	text, ok := entry["text"].(string)
	if !ok {
		return
	}
	visible, reports, split := splitWorkerReports(text)
	if !split {
		return
	}
	entry["text"] = visible
	entry["workerReports"] = reports
}

// splitWorkerReports matches the desktop humanVisibleText rule: a trailing
// block opened by a blank line and closed at the end of the text. Anything
// else stays in the message so a malformed block is not dropped or half-read.
func splitWorkerReports(text string) (visible, reports string, ok bool) {
	if !strings.HasSuffix(text, workerReportClose) {
		return text, "", false
	}
	marker := "\n\n" + workerReportOpen + "\n"
	start := strings.LastIndex(text, marker)
	if start < 0 {
		return text, "", false
	}
	inner := text[start+len(marker) : len(text)-len(workerReportClose)]
	inner = strings.TrimSuffix(inner, "\n")
	return text[:start], inner, true
}

func stripAttachmentPayloads(entry map[string]any) {
	raw, ok := entry["content"].([]any)
	if !ok {
		return
	}
	cleaned := make([]any, 0, len(raw))
	for _, item := range raw {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		next := map[string]any{}
		if t, ok := obj["type"].(string); ok {
			next["type"] = t
		}
		if name, ok := obj["name"].(string); ok && name != "" {
			next["name"] = name
		}
		if mime, ok := obj["mimeType"].(string); ok && mime != "" {
			next["mimeType"] = mime
		}
		cleaned = append(cleaned, next)
	}
	entry["content"] = cleaned
}

func sanitizeTree(v any) {
	switch node := v.(type) {
	case map[string]any:
		for key, child := range node {
			switch typed := child.(type) {
			case string:
				node[key] = sanitizeText(typed)
			default:
				sanitizeTree(typed)
			}
		}
	case []any:
		for i, child := range node {
			switch typed := child.(type) {
			case string:
				node[i] = sanitizeText(typed)
			default:
				sanitizeTree(typed)
			}
		}
	}
}

func sanitizeText(s string) string {
	s = ansiPattern.ReplaceAllString(s, "")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\n' || r == '\t' {
			b.WriteRune(r)
			continue
		}
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// cappedKeys are the text fields whose middle may be elided. Identifiers,
// paths, and statuses are left whole.
var cappedKeys = map[string]bool{
	"text": true, "summary": true, "command": true, "rawCommand": true,
	"output": true, "terminalInput": true, "streamedText": true, "patch": true,
	"error": true, "errorMessage": true, "explanation": true, "rationale": true,
	"message": true, "workerReports": true,
}

// capTree elides known text fields. Truncation records are attached to record
// (the entry or turn) so a nested detail map is not given its own list.
// force caps every string, which is how tool arguments and results stay bounded
// without a fixed key list. Returns the number of fields cut.
func capTree(v any, path string, maxChars int, fullCmd string, record map[string]any) int {
	if maxChars == 0 || v == nil {
		return 0
	}
	sink := make([]fieldTruncation, 0)
	walkCap(v, path, maxChars, fullCmd, false, &sink)
	if len(sink) == 0 {
		return 0
	}
	if record != nil {
		existing, _ := record["truncations"].([]fieldTruncation)
		record["truncations"] = append(existing, sink...)
	}
	return len(sink)
}

func walkCap(v any, path string, maxChars int, fullCmd string, force bool, sink *[]fieldTruncation) {
	node, ok := v.(map[string]any)
	if !ok {
		items, ok := v.([]any)
		if !ok {
			return
		}
		for i, item := range items {
			walkCap(item, fmt.Sprintf("%s.%d", path, i), maxChars, fullCmd, force, sink)
		}
		return
	}
	for key, child := range node {
		if key == "truncations" {
			continue
		}
		childPath := key
		if path != "" {
			childPath = path + "." + key
		}
		childForce := force || key == "arguments" || key == "result"
		switch typed := child.(type) {
		case string:
			if !force && !cappedKeys[key] && key != "arguments" && key != "result" {
				continue
			}
			next, trunc := elideField(typed, maxChars, childPath, fullCmd)
			if trunc == nil {
				continue
			}
			node[key] = next
			*sink = append(*sink, *trunc)
		case map[string]any:
			walkCap(typed, childPath, maxChars, fullCmd, childForce, sink)
		case []any:
			for i, item := range typed {
				itemPath := fmt.Sprintf("%s.%d", childPath, i)
				switch itemTyped := item.(type) {
				case string:
					if !childForce {
						continue
					}
					next, trunc := elideField(itemTyped, maxChars, itemPath, fullCmd)
					if trunc == nil {
						continue
					}
					typed[i] = next
					*sink = append(*sink, *trunc)
				default:
					walkCap(itemTyped, itemPath, maxChars, fullCmd, childForce, sink)
				}
			}
		}
	}
}

func elideField(text string, maxChars int, field, fullCmd string) (string, *fieldTruncation) {
	original := utf8.RuneCountInString(text)
	if maxChars == 0 || original <= maxChars {
		return text, nil
	}
	next := elideWithCommand(text, maxChars, fullCmd)
	return next, &fieldTruncation{
		Field:         field,
		OriginalChars: original,
		KeptChars:     utf8.RuneCountInString(next),
		Strategy:      elisionStrategy,
	}
}

func elideWithCommand(text string, maxChars int, fullCmd string) string {
	runes := []rune(text)
	original := len(runes)
	if maxChars == 0 || original <= maxChars {
		return text
	}
	omitted := original - maxChars
	if omitted < 1 {
		omitted = 1
	}
	for attempt := 0; attempt < 8; attempt++ {
		marker := truncationMarker(omitted, fullCmd)
		markerRunes := []rune(marker)
		if len(markerRunes) >= maxChars {
			return marker
		}
		head := (maxChars - len(markerRunes)) / 2
		tail := maxChars - len(markerRunes) - head
		if head < 0 {
			head = 0
		}
		if tail < 0 {
			tail = 0
		}
		if head+tail > original {
			head = original / 2
			tail = original - head
		}
		nextOmitted := original - head - tail
		if nextOmitted < 1 {
			nextOmitted = original
		}
		if nextOmitted == omitted {
			var b strings.Builder
			b.WriteString(string(runes[:head]))
			b.WriteString(marker)
			if tail > 0 {
				b.WriteString(string(runes[len(runes)-tail:]))
			}
			return b.String()
		}
		omitted = nextOmitted
	}
	marker := truncationMarker(original, fullCmd)
	return marker
}

func truncationMarker(omitted int, fullCmd string) string {
	if fullCmd == "" {
		return fmt.Sprintf("[... %d chars omitted ...]", omitted)
	}
	return fmt.Sprintf("[... %d chars omitted; full entry: %s ...]", omitted, fullCmd)
}

func transcriptCommand(id string, n int, before int64, fieldCap *int, project string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ao session get %s --transcript %d", id, n)
	if before > 0 {
		fmt.Fprintf(&b, " --before %d", before)
	}
	if fieldCap != nil {
		fmt.Fprintf(&b, " --cap %d", *fieldCap)
	}
	if project != "" {
		fmt.Fprintf(&b, " -p %s", project)
	}
	return b.String()
}

func incompleteItems(turns, entries []map[string]any) []any {
	items := make([]any, 0)
	for _, turn := range turns {
		state, _ := turn["state"].(string)
		if state != "running" && state != "queued" {
			continue
		}
		id, _ := turn["id"].(string)
		items = append(items, map[string]any{"type": "turn", "id": id, "state": state})
	}
	for _, entry := range entries {
		if streaming, _ := entry["streaming"].(bool); streaming {
			items = append(items, map[string]any{
				"type": "entry", "sequence": json.Number(strconv.FormatInt(entrySequence(entry), 10)), "reason": "streaming",
			})
			continue
		}
		status, _ := entry["status"].(string)
		kind, _ := entry["activityKind"].(string)
		switch {
		case status == "running":
			items = append(items, map[string]any{
				"type": "entry", "sequence": json.Number(strconv.FormatInt(entrySequence(entry), 10)), "reason": "running",
			})
		case status == "pending" && (kind == "approval" || kind == "user_input"):
			item := map[string]any{
				"type": "entry", "sequence": json.Number(strconv.FormatInt(entrySequence(entry), 10)), "reason": "pending",
			}
			if requestID, _ := entry["requestId"].(string); requestID != "" {
				item["requestId"] = requestID
			}
			items = append(items, item)
		}
	}
	if len(items) == 0 {
		return nil
	}
	return items
}

func renderTranscriptHuman(doc *transcriptDocument) string {
	var b strings.Builder
	if doc.NoConversation {
		fmt.Fprintf(&b, "transcript: 0 entries; session has no conversation yet\n")
		return b.String()
	}
	writeTranscriptSummary(&b, doc)
	writeTranscriptState(&b, doc)
	if len(doc.Entries) > 0 {
		b.WriteByte('\n')
	}
	for _, raw := range doc.Entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		writeTranscriptEntry(&b, entry)
	}
	if doc.TruncatedFields > 0 {
		fieldWord := "fields"
		if doc.TruncatedFields == 1 {
			fieldWord = "field"
		}
		fmt.Fprintf(&b, "%d %s truncated (cap %d chars). Re-run with --cap 0 for full text.\n", doc.TruncatedFields, fieldWord, doc.Cap.Value)
	}
	return b.String()
}

func writeTranscriptSummary(b *strings.Builder, doc *transcriptDocument) {
	switch {
	case doc.Returned == 0:
		fmt.Fprintf(b, "transcript: 0 entries, oldest first\n")
	case doc.LatestSequence > 0 && len(doc.Entries) > 0:
		newestEntry, newestOK := doc.Entries[len(doc.Entries)-1].(map[string]any)
		oldestEntry, oldestOK := doc.Entries[0].(map[string]any)
		if !newestOK || !oldestOK {
			fmt.Fprintf(b, "transcript: %d entries, oldest first\n", doc.Returned)
			break
		}
		fmt.Fprintf(b, "transcript: %d of %d entries (sequence %d to %d), oldest first\n", doc.Returned, doc.LatestSequence, entrySequence(oldestEntry), entrySequence(newestEntry))
	default:
		fmt.Fprintf(b, "transcript: %d entries, oldest first\n", doc.Returned)
	}
	if doc.ConversationID == "" && doc.Controller == "" {
		return
	}
	fmt.Fprintf(b, "conversation: %s", emptyDash(doc.ConversationID))
	if doc.ActiveBranchID != "" {
		fmt.Fprintf(b, "  branch: %s", doc.ActiveBranchID)
	}
	if doc.Controller != "" {
		fmt.Fprintf(b, "  controller: %s", doc.Controller)
	}
	switch {
	case doc.CompactedAt != nil && *doc.CompactedAt != "":
		fmt.Fprintf(b, "  compacted: %s", displayStamp(*doc.CompactedAt))
	case doc.ConversationID != "":
		b.WriteString("  compacted: never")
	}
	b.WriteByte('\n')
}

func writeTranscriptState(b *strings.Builder, doc *transcriptDocument) {
	if line := formatUsageLine(doc.Usage, doc.RateLimits); line != "" {
		fmt.Fprintf(b, "%s\n", line)
	}
	for _, raw := range doc.Turns {
		turn, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if line := formatTurnLine(turn); line != "" {
			fmt.Fprintf(b, "%s\n", line)
		}
	}
	if len(doc.Incomplete) > 0 {
		fmt.Fprintf(b, "incomplete: %s\n", formatIncomplete(doc.Incomplete))
	}
	if doc.Next != nil {
		fmt.Fprintf(b, "older: %s\n", doc.Next.Command)
	}
}

func formatUsageLine(usage, limits any) string {
	var parts []string
	if obj, ok := usage.(map[string]any); ok {
		used, usedOK := asInt64(obj["contextUsed"])
		window, windowOK := asInt64(obj["contextWindow"])
		if usedOK && windowOK && window > 0 {
			pct := (used*100 + window/2) / window
			parts = append(parts, fmt.Sprintf("context %d%% (%d/%d)", pct, used, window))
		} else if usedOK {
			parts = append(parts, fmt.Sprintf("context %d tokens", used))
		}
	}
	if obj, ok := limits.(map[string]any); ok {
		if raw, ok := obj["primaryUsedPercent"]; ok {
			parts = append(parts, fmt.Sprintf("rate limit: %s%% used (primary)", formatPercent(raw)))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "usage: " + strings.Join(parts, "  ")
}

func formatPercent(v any) string {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return n.String()
		}
		if f == float64(int64(f)) {
			return strconv.FormatInt(int64(f), 10)
		}
		return strconv.FormatFloat(f, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}

func formatTurnLine(turn map[string]any) string {
	state, _ := turn["state"].(string)
	if state != "running" && state != "queued" && state != "failed" {
		return ""
	}
	id, _ := turn["id"].(string)
	var b strings.Builder
	fmt.Fprintf(&b, "turn %s %s", id, state)
	if state == "running" {
		if started, _ := turn["startedAt"].(string); started != "" {
			fmt.Fprintf(&b, " since %s", displayStamp(started))
		} else if requested, _ := turn["requestedAt"].(string); requested != "" {
			fmt.Fprintf(&b, " since %s", displayStamp(requested))
		}
	}
	if plan, ok := turn["plan"].(map[string]any); ok {
		done, total := planProgress(plan)
		if total > 0 {
			fmt.Fprintf(&b, "  plan %d/%d done", done, total)
		}
	}
	if diff, ok := turn["diff"].(map[string]any); ok {
		files, _ := diff["files"].([]any)
		if len(files) > 0 {
			add, del := diffTotals(files)
			fmt.Fprintf(&b, "  diff %d files (+%d -%d)", len(files), add, del)
		}
	}
	if errText, _ := turn["errorMessage"].(string); errText != "" {
		fmt.Fprintf(&b, "  error: %s", oneLine(errText))
	}
	return b.String()
}

func planProgress(plan map[string]any) (done, total int) {
	steps, _ := plan["steps"].([]any)
	total = len(steps)
	for _, raw := range steps {
		step, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if status, _ := step["status"].(string); status == "completed" {
			done++
		}
	}
	return done, total
}

func diffTotals(files []any) (add, del int) {
	for _, raw := range files {
		file, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if n, ok := asInt64(file["additions"]); ok {
			add += int(n)
		}
		if n, ok := asInt64(file["deletions"]); ok {
			del += int(n)
		}
	}
	return add, del
}

func formatIncomplete(items []any) string {
	parts := make([]string, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch item["type"] {
		case "turn":
			id, _ := item["id"].(string)
			state, _ := item["state"].(string)
			parts = append(parts, fmt.Sprintf("turn %s %s", id, state))
		case "entry":
			seq, _ := asInt64(item["sequence"])
			reason, _ := item["reason"].(string)
			parts = append(parts, fmt.Sprintf("entry %d %s", seq, reason))
		}
	}
	return strings.Join(parts, "; ")
}

func writeTranscriptEntry(b *strings.Builder, entry map[string]any) {
	seq := entrySequence(entry)
	created := displayStamp(stringField(entry, "createdAt"))
	kind, _ := entry["kind"].(string)
	fmt.Fprintf(b, "#%d %s %s\n", seq, created, entryHeadline(entry, kind))
	switch kind {
	case "message":
		writeIndentedLines(b, stringField(entry, "text"))
		writeAttachmentLines(b, entry)
		if reports, ok := entry["workerReports"].(string); ok {
			b.WriteString("  [worker reports delivered with this message]\n")
			writePrefixedLines(b, reports)
		}
	default:
		writeActivityBody(b, entry)
	}
}

func entryHeadline(entry map[string]any, kind string) string {
	var parts []string
	switch kind {
	case "message":
		role := stringField(entry, "role")
		origin := stringField(entry, "origin")
		parts = append(parts, strings.Trim(role+"/"+origin, "/"))
		if turn := stringField(entry, "turnId"); turn != "" {
			parts = append(parts, "turn "+turn)
		}
		if rev, ok := asInt64(entry["revision"]); ok && rev > 1 {
			parts = append(parts, fmt.Sprintf("rev %d", rev))
		}
		if from := senderClause(entry); from != "" {
			parts = append(parts, from)
		}
		if streaming, _ := entry["streaming"].(bool); streaming {
			parts = append(parts, "STREAMING")
		}
	default:
		activity := stringField(entry, "activityKind")
		if activity == "" {
			activity = kind
		}
		parts = append(parts, activity)
		if status := stringField(entry, "status"); status != "" {
			parts = append(parts, status)
		}
		if activity == "command" {
			if code, ok := detailInt(entry, "exitCode"); ok {
				parts = append(parts, fmt.Sprintf("exit %d", code))
			}
			if ms, ok := detailInt(entry, "durationMs"); ok {
				parts = append(parts, formatDurationMs(ms))
			}
		}
		if turn := stringField(entry, "turnId"); turn != "" {
			parts = append(parts, "turn "+turn)
		}
		if rev, ok := asInt64(entry["revision"]); ok && rev > 1 {
			parts = append(parts, fmt.Sprintf("rev %d", rev))
		}
		if status := stringField(entry, "status"); status == "running" {
			parts = append(parts, "RUNNING")
		}
		if requestID := stringField(entry, "requestId"); requestID != "" && (activityKind(entry) == "approval" || activityKind(entry) == "user_input") {
			parts = append(parts, "request "+requestID)
		}
	}
	return strings.Join(parts, " ")
}

func activityKind(entry map[string]any) string {
	return stringField(entry, "activityKind")
}

func senderClause(entry map[string]any) string {
	id := stringField(entry, "senderSessionId")
	name := stringField(entry, "senderDisplayName")
	switch {
	case id != "" && name != "":
		return fmt.Sprintf("from %s (%s)", id, name)
	case id != "":
		return "from " + id
	case name != "":
		return "from " + name
	default:
		return ""
	}
}

func writeActivityBody(b *strings.Builder, entry map[string]any) {
	detail, _ := entry["detail"].(map[string]any)
	kind := activityKind(entry)
	switch kind {
	case "command":
		if detail != nil {
			if cmd := stringField(detail, "command"); cmd != "" {
				writeIndentedLines(b, "$ "+cmd)
			}
			if input := stringField(detail, "terminalInput"); input != "" {
				writePrefixedLines(b, input)
			}
			writePrefixedLines(b, stringField(detail, "output"))
		}
	case "reasoning":
		if detail != nil {
			writeIndentedLines(b, stringField(detail, "text"))
		} else {
			writeIndentedLines(b, stringField(entry, "summary"))
		}
	case "file_change":
		writeIndentedLines(b, stringField(entry, "summary"))
		writeFileChange(b, detail)
	case "mcp_tool":
		writeIndentedLines(b, stringField(entry, "summary"))
		if detail != nil {
			writePayloadLine(b, "arguments", detail["arguments"])
			writePayloadLine(b, "result", detail["result"])
			if errText := stringField(detail, "error"); errText != "" {
				writeIndentedLines(b, errText)
			}
		}
	default:
		if summary := stringField(entry, "summary"); summary != "" {
			writeIndentedLines(b, summary)
		}
		if detail != nil {
			if text := stringField(detail, "text"); text != "" {
				writeIndentedLines(b, text)
			}
			if msg := stringField(detail, "message"); msg != "" {
				writeIndentedLines(b, msg)
			}
			writePrefixedLines(b, stringField(detail, "output"))
		}
	}
}

func writeFileChange(b *strings.Builder, detail map[string]any) {
	if detail == nil {
		return
	}
	files, _ := detail["files"].([]any)
	for _, raw := range files {
		file, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		path := stringField(file, "path")
		if path != "" {
			writeIndentedLines(b, path)
		}
		writePrefixedLines(b, stringField(file, "patch"))
	}
}

func writePayloadLine(b *strings.Builder, label string, value any) {
	if value == nil {
		return
	}
	switch typed := value.(type) {
	case string:
		if typed == "" {
			return
		}
		writeIndentedLines(b, label+": "+typed)
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return
		}
		writeIndentedLines(b, label+": "+string(encoded))
	}
}

func writeAttachmentLines(b *strings.Builder, entry map[string]any) {
	raw, _ := entry["content"].([]any)
	for _, item := range raw {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		desc := strings.TrimSpace(strings.Join([]string{stringField(obj, "type"), stringField(obj, "name"), stringField(obj, "mimeType")}, " "))
		if desc == "" {
			continue
		}
		writeIndentedLines(b, "attachment: "+desc)
	}
}

func writeIndentedLines(b *strings.Builder, text string) {
	if text == "" {
		return
	}
	for _, line := range strings.Split(text, "\n") {
		fmt.Fprintf(b, "  %s\n", line)
	}
}

func writePrefixedLines(b *strings.Builder, text string) {
	if text == "" {
		return
	}
	for _, line := range strings.Split(text, "\n") {
		fmt.Fprintf(b, "  | %s\n", line)
	}
}

func detailInt(entry map[string]any, key string) (int64, bool) {
	detail, ok := entry["detail"].(map[string]any)
	if !ok {
		return 0, false
	}
	return asInt64(detail[key])
}

func stringField(obj map[string]any, key string) string {
	value, _ := obj[key].(string)
	return value
}

func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func displayStamp(raw string) string {
	return raw
}

func formatDurationMs(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	seconds := ms / 1000
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	return fmt.Sprintf("%dm%ds", seconds/60, seconds%60)
}

func asInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i, true
		}
		f, err := n.Float64()
		if err != nil {
			return 0, false
		}
		return int64(f), true
	case float64:
		return int64(n), true
	case int:
		return int64(n), true
	case int64:
		return n, true
	case string:
		i, err := strconv.ParseInt(n, 10, 64)
		if err != nil {
			return 0, false
		}
		return i, true
	default:
		return 0, false
	}
}
