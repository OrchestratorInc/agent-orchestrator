package daemon

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type recordingActivity struct{ err error }

func (r recordingActivity) ApplyActivitySignal(context.Context, domain.SessionID, ports.ActivitySignal) error {
	return r.err
}

func TestCardSignalsFromHook(t *testing.T) {
	activity := func(text string) []domain.CardSignal {
		return []domain.CardSignal{{Kind: domain.CardSignalActivity, Text: text}}
	}
	for _, tt := range []struct {
		name string
		in   ports.ActivitySignal
		want []domain.CardSignal
	}{
		{"prompt submit starts a turn", ports.ActivitySignal{Event: "user-prompt-submit"}, []domain.CardSignal{{Kind: domain.CardSignalTurnStarted}}},
		{"tool use", ports.ActivitySignal{Event: "post-tool-use", ToolName: "Bash"}, activity("Used Bash")},
		{"tool failure", ports.ActivitySignal{Event: "post-tool-use-failure", ToolName: "Bash"}, activity("Failed: Bash")},
		{"tool use without a name", ports.ActivitySignal{Event: "post-tool-use"}, nil},
		{"stop carries the last message", ports.ActivitySignal{Event: "stop", LatestAssistantUpdate: "All done."},
			[]domain.CardSignal{{Kind: domain.CardSignalAssistant, Text: "All done."}, {Kind: domain.CardSignalTurnCompleted}}},
		{"stop without a message", ports.ActivitySignal{Event: "stop"}, []domain.CardSignal{{Kind: domain.CardSignalTurnCompleted}}},
		{"subagent work is ignored", ports.ActivitySignal{Event: "post-tool-use", ToolName: "Edit", SubagentID: "sub-1"}, nil},
		{"unrelated event", ports.ActivitySignal{Event: "notification"}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := cardSignalsFromHook(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("signals = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestCardSignalActivityObservesAcceptedAndContendedSignalsOnly(t *testing.T) {
	var observed int
	a := cardSignalActivity{
		ActivityRecorder: recordingActivity{},
		observe:          func(context.Context, domain.SessionID, domain.CardSignal) { observed++ },
	}
	apply := func(err error) error {
		a.ActivityRecorder = recordingActivity{err: err}
		return a.ApplyActivitySignal(context.Background(), "s", ports.ActivitySignal{Event: "stop"})
	}
	if err := apply(nil); err != nil || observed != 1 {
		t.Fatalf("accepted: err=%v observed=%d", err, observed)
	}
	if err := apply(ports.ErrActivityProjectionContention); !errors.Is(err, ports.ErrActivityProjectionContention) || observed != 2 {
		t.Fatalf("contention must pass through and still report the turn end: err=%v observed=%d", err, observed)
	}
	rejected := errors.New("rejected")
	if err := apply(rejected); !errors.Is(err, rejected) || observed != 2 {
		t.Fatalf("a rejected signal must not be observed: err=%v observed=%d", err, observed)
	}
}
