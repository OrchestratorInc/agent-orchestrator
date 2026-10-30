package codexappserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func testSearchItem() codexproto.ThreadItem {
	return codexproto.ThreadItem{
		Type: itemWebSearch,
		Results: []json.RawMessage{
			json.RawMessage(`{"ref_id":"turn0search0","title":"First source","url":"https://example.com/first"}`),
			json.RawMessage(`{"ref_id":"turn0search12","title":"Second source","url":"https://example.com/second"}`),
		},
	}
}

func searchItemWithURL(url string) codexproto.ThreadItem {
	return codexproto.ThreadItem{
		Type: itemWebSearch,
		Results: []json.RawMessage{
			json.RawMessage(`{"ref_id":"same-source","title":"Source","url":"` + url + `"}`),
		},
	}
}

func TestCitationFormatterSeparatesNativeThreads(t *testing.T) {
	f := newCitationFormatter()
	rootParams, _ := json.Marshal(struct {
		ThreadID string                `json:"threadId"`
		TurnID   string                `json:"turnId"`
		Item     codexproto.ThreadItem `json:"item"`
	}{"root-thread", "same-turn", searchItemWithURL("https://example.com/root")})
	childParams, _ := json.Marshal(struct {
		ThreadID string                `json:"threadId"`
		TurnID   string                `json:"turnId"`
		Item     codexproto.ThreadItem `json:"item"`
	}{"child-thread", "same-turn", searchItemWithURL("https://example.com/child")})
	f.observeNotification(notification{Method: codexproto.MethodItemCompleted, Params: rootParams}, "")
	f.observeNotification(notification{Method: codexproto.MethodItemCompleted, Params: childParams}, "")
	marker := citationStart + "cite" + citationField + "same-source" + citationStop
	if got := f.markdown("Root "+marker, "root-thread", "same-turn"); !strings.Contains(got, "https://example.com/root") {
		t.Fatalf("root source crossed threads: %q", got)
	}
	if got := f.markdown("Child "+marker, "child-thread", "same-turn"); !strings.Contains(got, "https://example.com/child") {
		t.Fatalf("child source crossed threads: %q", got)
	}

	start := citationStart + "cite" + citationField + "same-source"
	if ev, visible := f.formatEvent("root-thread", ports.ChatEvent{
		Kind: ports.ChatEventMessageDelta, ProviderTurnID: "same-turn", ProviderItemID: "same-item", Delta: "Root " + start,
	}); !visible || ev.Delta != "Root " {
		t.Fatalf("root pending delta = %q, visible %t", ev.Delta, visible)
	}
	if ev, visible := f.formatEvent("child-thread", ports.ChatEvent{
		Kind: ports.ChatEventMessageDelta, ProviderTurnID: "same-turn", ProviderItemID: "same-item", Delta: "Child " + start,
	}); !visible || ev.Delta != "Child " {
		t.Fatalf("child pending delta = %q, visible %t", ev.Delta, visible)
	}
	f.formatEvent("root-thread", ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "same-turn",
	})
	if ev, visible := f.formatEvent("child-thread", ports.ChatEvent{
		Kind: ports.ChatEventMessageDelta, ProviderTurnID: "same-turn", ProviderItemID: "same-item", Delta: citationStop,
	}); !visible || ev.Delta != "[1](<https://example.com/child>)" {
		t.Fatalf("child pending state was lost: %q, visible %t", ev.Delta, visible)
	}
}

func TestCitationFormatterStreamsNativeMarkersAsLinks(t *testing.T) {
	f := newCitationFormatter()
	f.observeItem("thread-1", "turn-1", testSearchItem())
	parts := []string{
		"The claim. " + citationStart + "ci",
		"te" + citationField + "turn0search0",
		citationField + "turn0search12" + citationStop + " Done.",
	}
	var streamed strings.Builder
	for _, part := range parts {
		ev, visible := f.formatEvent("thread-1", ports.ChatEvent{
			Kind: ports.ChatEventMessageDelta, ProviderTurnID: "turn-1",
			ProviderItemID: "answer-1", Delta: part,
		})
		if visible {
			streamed.WriteString(ev.Delta)
		}
		if strings.Contains(streamed.String(), citationStart) || strings.Contains(streamed.String(), "turn0search") {
			t.Fatalf("native citation leaked while streaming: %q", streamed.String())
		}
	}
	want := "The claim. [1](<https://example.com/first>), [2](<https://example.com/second>) Done."
	if got := streamed.String(); got != want {
		t.Fatalf("streamed = %q, want %q", got, want)
	}
	completed, _ := f.formatEvent("thread-1", ports.ChatEvent{
		Kind: ports.ChatEventMessageCompleted, ProviderTurnID: "turn-1",
		ProviderItemID: "answer-1", Text: strings.Join(parts, ""),
	})
	if completed.Text != want {
		t.Fatalf("settled = %q, want %q", completed.Text, want)
	}
}

func TestCitationFormatterPreservesCodeExamplesAndUnknownSources(t *testing.T) {
	f := newCitationFormatter()
	f.observeItem("thread-1", "turn-1", testSearchItem())
	marker := citationStart + "cite" + citationField + "turn0search0" + citationStop
	unknown := citationStart + "cite" + citationField + "turn9search0" + citationStop
	malformed := citationStart + "cite" + citationField + "bad!" + citationStop
	raw := "Inline `" + marker + "` stays.\n\n```text\n" + marker + "\n```\n\nKnown " + marker + " unknown " + unknown + " malformed " + malformed
	got := f.markdown(raw, "thread-1", "turn-1")
	if strings.Count(got, marker) != 2 {
		t.Fatalf("code examples changed: %q", got)
	}
	if !strings.Contains(got, "Known [1](<https://example.com/first>) unknown [Source unavailable] malformed [Source unavailable]") {
		t.Fatalf("prose citations not normalized: %q", got)
	}
	if trailing := f.markdown("Broken "+citationStart+"cite"+citationField+"turn0search0", "thread-1", "turn-1"); trailing != "Broken [Source unavailable]" {
		t.Fatalf("incomplete citation = %q", trailing)
	}
	streamed, visible := f.formatEvent("thread-1", ports.ChatEvent{
		Kind: ports.ChatEventMessageDelta, ProviderTurnID: "turn-1", ProviderItemID: "bad-answer", Delta: "Broken " + malformed,
	})
	if !visible || streamed.Delta != "Broken " {
		t.Fatalf("malformed citation stream = %q, visible %t", streamed.Delta, visible)
	}
}

func TestCitationFormatterLeavesOrdinaryMarkdownAlone(t *testing.T) {
	f := newCitationFormatter()
	raw := "See [source](https://example.com)."
	if got := f.markdown(raw, "thread-1", "turn-1"); got != raw {
		t.Fatalf("ordinary Markdown changed: %q", got)
	}
}

func TestLiveCodexCitationEventsBecomeMarkdown(t *testing.T) {
	conv, srv := openConversation(t)
	marker := citationStart + "cite" + citationField + "turn0search0" + citationStop
	srv.push(`{"method":"item/completed","params":{"threadId":"thread-1","turnId":"turn-1","item":` +
		`{"type":"webSearch","id":"search-1","query":"source","results":[` +
		`{"ref_id":"turn0search0","title":"First source","url":"https://example.com/first"}]}}}`)
	srv.push(`{"method":"item/agentMessage/delta","params":{"threadId":"thread-1","turnId":"turn-1",` +
		`"itemId":"answer-1","delta":` + quotedJSON("Answer "+marker[:len(marker)-len(citationStop)]) + `}}`)
	srv.push(`{"method":"item/agentMessage/delta","params":{"threadId":"thread-1","turnId":"turn-1",` +
		`"itemId":"answer-1","delta":` + quotedJSON(citationStop) + `}}`)
	srv.push(`{"method":"item/completed","params":{"threadId":"thread-1","turnId":"turn-1","item":` +
		`{"type":"agentMessage","id":"answer-1","text":` + quotedJSON("Answer "+marker) + `}}}`)

	var streamed strings.Builder
	for {
		select {
		case event := <-conv.Events():
			switch event.Kind {
			case ports.ChatEventMessageDelta:
				streamed.WriteString(event.Delta)
				if strings.Contains(streamed.String(), citationStart) {
					t.Fatalf("raw citation streamed: %q", streamed.String())
				}
			case ports.ChatEventMessageCompleted:
				want := "Answer [1](<https://example.com/first>)"
				if streamed.String() != want || event.Text != want {
					t.Fatalf("streamed %q, settled %q; want %q", streamed.String(), event.Text, want)
				}
				return
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for the cited answer")
		}
	}
}

func TestReadHistoryResolvesCodexCitationSources(t *testing.T) {
	conv, srv := openConversation(t)
	marker := citationStart + "cite" + citationField + "turn0search0" + citationStop
	srv.reply("thread/read", `{"thread":{"id":"thread-1","turns":[{"id":"turn-1","status":"completed","items":[`+
		`{"type":"agentMessage","id":"answer-1","text":`+quotedJSON("Answer "+marker)+`}`+
		`,{"type":"webSearch","id":"search-1","results":[{"ref_id":"turn0search0",`+
		`"title":"First source","url":"https://example.com/first"}]}`+
		`] }]}}`)
	events, err := conv.ReadHistory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Kind == ports.ChatEventMessageCompleted {
			if want := "Answer [1](<https://example.com/first>)"; event.Text != want {
				t.Fatalf("history answer = %q, want %q", event.Text, want)
			}
			return
		}
	}
	t.Fatal("history contained no assistant answer")
}

func quotedJSON(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
