package codexappserver

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	gmtext "github.com/yuin/goldmark/text"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/citation"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	citationStart = "\ue200"
	citationField = "\ue202"
	citationStop  = "\ue201"
)

var (
	citationID      = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	citationLocator = regexp.MustCompile(`^L\d+(?:-L\d+)?$`)
)

// citationFormatter is confined to one native conversation. Codex owns the
// marker grammar and reference IDs; the events it emits contain ordinary
// Markdown, which every Chat provider and renderer already understands.
type citationFormatter struct {
	sources  map[string]map[string]citation.Source
	messages map[string]*citationMessage
}

type citationMessage struct {
	raw      string
	rendered string
	pending  bool
}

func newCitationFormatter() *citationFormatter {
	return &citationFormatter{
		sources:  make(map[string]map[string]citation.Source),
		messages: make(map[string]*citationMessage),
	}
}

func (f *citationFormatter) observeNotification(n notification) {
	if n.Method != codexproto.MethodItemCompleted {
		return
	}
	var p struct {
		TurnID string                `json:"turnId"`
		Item   codexproto.ThreadItem `json:"item"`
	}
	if json.Unmarshal(n.Params, &p) == nil {
		f.observeItem(p.TurnID, p.Item)
	}
}

func (f *citationFormatter) observeItem(turnID string, item codexproto.ThreadItem) {
	if item.Type != itemWebSearch || len(item.Results) == 0 {
		return
	}
	if f.sources[turnID] == nil {
		f.sources[turnID] = make(map[string]citation.Source)
	}
	for _, raw := range item.Results {
		var result struct {
			ID    string `json:"ref_id"`
			Title string `json:"title"`
			URL   string `json:"url"`
		}
		if json.Unmarshal(raw, &result) != nil || !citationID.MatchString(result.ID) {
			continue
		}
		if previous, exists := f.sources[turnID][result.ID]; exists && previous.URL != result.URL {
			f.sources[turnID][result.ID] = citation.Source{ID: result.ID}
			continue
		}
		f.sources[turnID][result.ID] = citation.Source{
			ID: result.ID, Title: result.Title, URL: result.URL,
		}
	}
}

// formatEvent keeps streamed output append-only. A marker split across deltas,
// or one whose source has not arrived yet, stays buffered until it can be shown
// as a link. The settled event always replaces the stream with the full answer.
func (f *citationFormatter) formatEvent(ev ports.ChatEvent) (ports.ChatEvent, bool) {
	key := ev.ProviderTurnID + "\x00" + ev.ProviderItemID
	switch ev.Kind {
	case ports.ChatEventMessageDelta:
		message := f.messages[key]
		if message == nil {
			message = &citationMessage{}
			f.messages[key] = message
		}
		message.raw += ev.Delta
		if !message.pending && !strings.Contains(ev.Delta, citationStart) {
			message.rendered += ev.Delta
			return ev, true
		}
		rendered, pending := f.markdownWithPending(message.raw, ev.ProviderTurnID, false)
		if !strings.HasPrefix(rendered, message.rendered) {
			// A late source or an unfinished Markdown construct changed an already
			// emitted prefix. Completion will settle the authoritative full text.
			return ev, false
		}
		ev.Delta = strings.TrimPrefix(rendered, message.rendered)
		message.rendered = rendered
		message.pending = pending
		return ev, ev.Delta != ""
	case ports.ChatEventMessageCompleted:
		delete(f.messages, key)
		ev.Text = f.markdown(ev.Text, ev.ProviderTurnID)
	case ports.ChatEventTurnCompleted:
		// A failed or cancelled turn may never complete its last message.
		prefix := ev.ProviderTurnID + "\x00"
		for key := range f.messages {
			if strings.HasPrefix(key, prefix) {
				delete(f.messages, key)
			}
		}
	}
	return ev, true
}

func (f *citationFormatter) markdown(raw, turnID string) string {
	rendered, _ := f.markdownWithPending(raw, turnID, true)
	return rendered
}

func (f *citationFormatter) markdownWithPending(raw, turnID string, complete bool) (string, bool) {
	if !strings.Contains(raw, citationStart) {
		return raw, false
	}
	protected := markdownCodeRanges(raw)
	references := make([]citation.Reference, 0, 2)
	limit := len(raw)
	for from := 0; from < len(raw); {
		start := strings.Index(raw[from:], citationStart)
		if start < 0 {
			break
		}
		start += from
		if inRange(protected, start) {
			from = start + len(citationStart)
			continue
		}
		bodyStart := start + len(citationStart)
		stop := strings.Index(raw[bodyStart:], citationStop)
		if stop < 0 {
			if !complete {
				limit = start
			} else if strings.HasPrefix(raw[bodyStart:], "cite") {
				references = append(references, citation.Reference{
					Start: start, End: len(raw), Sources: []citation.Source{{}},
				})
			}
			break
		}
		stop += bodyStart
		end := stop + len(citationStop)
		ids := citationIDs(raw[bodyStart:stop])
		if len(ids) == 0 {
			if strings.HasPrefix(raw[bodyStart:stop], "cite") {
				if !complete {
					limit = start
					break
				}
				references = append(references, citation.Reference{
					Start: start, End: end, Sources: []citation.Source{{}},
				})
			}
			from = end
			continue
		}
		ref := citation.Reference{Start: start, End: end}
		for _, id := range ids {
			source := f.source(turnID, id)
			if source.URL == "" && !complete {
				limit = start
				break
			}
			ref.Sources = append(ref.Sources, source)
		}
		if limit != len(raw) {
			break
		}
		references = append(references, ref)
		from = end
	}
	return citation.Markdown(raw[:limit], references), limit != len(raw)
}

func citationIDs(body string) []string {
	fields := strings.Split(body, citationField)
	if len(fields) < 2 || fields[0] != "cite" {
		return nil
	}
	ids := make([]string, 0, len(fields)-1)
	for _, field := range fields[1:] {
		if citationLocator.MatchString(field) {
			continue
		}
		if !citationID.MatchString(field) {
			return nil
		}
		ids = append(ids, field)
	}
	return ids
}

func (f *citationFormatter) source(turnID, id string) citation.Source {
	if source, ok := f.sources[turnID][id]; ok {
		return source
	}
	// A later answer can cite an earlier search. Use it only when its native ID
	// has exactly one destination across the conversation.
	var found citation.Source
	for _, sources := range f.sources {
		if source, ok := sources[id]; ok {
			if found.ID != "" && found.URL != source.URL {
				return citation.Source{ID: id}
			}
			found = source
		}
	}
	if found.ID == "" {
		return citation.Source{ID: id}
	}
	return found
}

type byteRange struct{ start, end int }

func inRange(ranges []byteRange, offset int) bool {
	for _, r := range ranges {
		if offset >= r.start && offset < r.end {
			return true
		}
	}
	return false
}

// Ask the same Markdown parser used elsewhere in the Go backend which bytes
// are literal code. Citation-looking examples in code remain untouched.
func markdownCodeRanges(raw string) []byteRange {
	source := []byte(raw)
	doc := goldmark.DefaultParser().Parse(gmtext.NewReader(source))
	ranges := make([]byteRange, 0)
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := node.(type) {
		case *ast.CodeSpan:
			for child := n.FirstChild(); child != nil; child = child.NextSibling() {
				if textNode, ok := child.(*ast.Text); ok {
					ranges = append(ranges, byteRange{textNode.Segment.Start, textNode.Segment.Stop})
				}
			}
		case *ast.CodeBlock:
			for i := 0; i < n.Lines().Len(); i++ {
				line := n.Lines().At(i)
				ranges = append(ranges, byteRange{line.Start, line.Stop})
			}
		case *ast.FencedCodeBlock:
			for i := 0; i < n.Lines().Len(); i++ {
				line := n.Lines().At(i)
				ranges = append(ranges, byteRange{line.Start, line.Stop})
			}
		}
		return ast.WalkContinue, nil
	})
	return ranges
}
