package chat_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// SQLite's capped appends discard text after a NUL in that delta. Combining
// deltas must not also discard later chunks or change when the cap is reached.
func TestNULDeltasPreserveUnbatchedActivityStreams(t *testing.T) {
	for _, stream := range []struct {
		name     string
		kind     ports.ChatEventKind
		activity domain.ActivityKind
		limit    int
	}{
		{"command output", ports.ChatEventCommandOutputDelta, domain.ActivityKindCommand, store.MaxCommandOutputChars},
		{"reasoning", ports.ChatEventReasoningDelta, domain.ActivityKindReasoning, store.MaxStreamedTextChars},
		{"terminal input", ports.ChatEventCommandInput, domain.ActivityKindCommand, store.MaxStreamedTextChars},
		{"activity text", ports.ChatEventActivityText, domain.ActivityKindMCPTool, store.MaxStreamedTextChars},
	} {
		for _, identified := range []bool{false, true} {
			identity := "anonymous"
			if identified {
				identity = "native IDs"
			}
			for _, capped := range []bool{false, true} {
				name := "suffix survives"
				if capped {
					name = "suffix reaches cap"
				}
				t.Run(stream.name+"/"+identity+"/"+name, func(t *testing.T) {
					const turn, item = "nul-turn", "nul-item"
					prefix := "before|"
					want := prefix + "prefixvisible|after"
					if capped {
						prefix = strings.Repeat("x", stream.limit-len("prefix")-3)
						want = prefix + "prefixvis"
					}
					script := []ports.ChatEvent{
						{Kind: ports.ChatEventTurnStarted, ProviderTurnID: turn},
						{Kind: ports.ChatEventActivityStarted, ProviderTurnID: turn, ProviderItemID: item,
							ActivityKind: stream.activity, ActivityStatus: domain.ActivityStatusRunning, Summary: stream.name},
						{Kind: stream.kind, ProviderTurnID: turn, ProviderItemID: item, Delta: prefix},
						{Kind: stream.kind, ProviderTurnID: turn, ProviderItemID: item, Delta: "prefix\x00hidden"},
						{Kind: stream.kind, ProviderTurnID: turn, ProviderItemID: item, Delta: "visible"},
						{Kind: stream.kind, ProviderTurnID: turn, ProviderItemID: item, Delta: "|after"},
						{Kind: ports.ChatEventActivityCompleted, ProviderTurnID: turn, ProviderItemID: item,
							ActivityKind: stream.activity, ActivityStatus: domain.ActivityStatusCompleted, Summary: stream.name},
						{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn, TurnState: domain.TurnStateCompleted},
					}
					if identified {
						for i := range script {
							script[i].ProviderEventID = fmt.Sprintf("nul-native-%d", i)
						}
					}
					unbatched := projectScript(t, 0, script, !identified)
					batched := projectScript(t, time.Hour, script, !identified)
					for _, run := range []struct {
						name string
						data scriptRun
					}{{"unbatched", unbatched}, {"batched", batched}} {
						if len(run.data.transcript) != 1 {
							t.Fatalf("%s timeline has %d rows, want one activity", run.name, len(run.data.transcript))
						}
						row := run.data.transcript[0]
						text, truncated := row.StreamedText, row.StreamedTextTruncated
						if stream.kind == ports.ChatEventCommandOutputDelta {
							text, truncated = row.CommandOutput, row.CommandOutputTruncated
						}
						if text != want || truncated != capped {
							t.Errorf("%s stream: length=%d suffix=%q truncated=%v, want length=%d suffix=%q truncated=%v",
								run.name, len(text), text[max(0, len(text)-30):], truncated, len(want), want[max(0, len(want)-30):], capped)
						}
					}
					if !slices.Equal(batched.turns, unbatched.turns) || !slices.Equal(batched.transcript, unbatched.transcript) {
						t.Error("batched turns or transcript differ from unbatched projection")
					}
					if !slices.Equal(batched.archive, unbatched.archive) {
						t.Error("batched archive differs from unbatched projection")
					}
				})
			}
		}
	}
}
