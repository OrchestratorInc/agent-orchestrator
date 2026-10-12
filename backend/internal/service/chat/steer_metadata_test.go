package chat

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type steerSessionReaderStub struct {
	record domain.SessionRecord
	found  bool
	err    error
}

func (s steerSessionReaderStub) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	return s.record, s.found, s.err
}

func TestResolveSteerSender(t *testing.T) {
	base := ports.ChatUserMessage{Text: "guidance", SenderSessionID: "worker-1"}
	tests := []struct {
		name       string
		reader     steerSessionReaderStub
		project    string
		display    string
		wantSameID bool
	}{
		{
			name:       "session found",
			reader:     steerSessionReaderStub{record: domain.SessionRecord{ProjectID: "project-1", DisplayName: "Backend worker"}, found: true},
			project:    "project-1",
			display:    "Backend worker",
			wantSameID: true,
		},
		{
			name:       "session missing",
			reader:     steerSessionReaderStub{},
			wantSameID: true,
		},
		{
			name:       "lookup error",
			reader:     steerSessionReaderStub{err: errors.New("database unavailable")},
			wantSameID: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := (&Service{sessions: tt.reader}).resolveSteerSender(context.Background(), base)
			if tt.wantSameID && got.SenderSessionID != base.SenderSessionID {
				t.Fatalf("sender session id = %q, want %q", got.SenderSessionID, base.SenderSessionID)
			}
			if got.SenderProjectID != tt.project || got.SenderDisplayName != tt.display {
				t.Fatalf("resolved metadata = (%q, %q), want (%q, %q)", got.SenderProjectID, got.SenderDisplayName, tt.project, tt.display)
			}
		})
	}
}

func TestMakeSteerActivitySenderMetadata(t *testing.T) {
	withSender, err := makeSteerActivity("activity-1", ports.ChatUserMessage{
		Text: "[from worker-1] guidance", Origin: domain.MessageOriginHuman,
		SenderSessionID: "worker-1", SenderProjectID: "project-1", SenderDisplayName: "Backend worker",
	}, "")
	if err != nil {
		t.Fatalf("makeSteerActivity with sender: %v", err)
	}
	var detail map[string]any
	if err := json.Unmarshal(withSender.Detail, &detail); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"senderSessionId": "worker-1", "senderProjectId": "project-1", "senderDisplayName": "Backend worker",
	} {
		if detail[key] != want {
			t.Errorf("detail[%q] = %v, want %q", key, detail[key], want)
		}
	}

	withoutSender, err := makeSteerActivity("activity-2", ports.ChatUserMessage{Text: "guidance"}, "")
	if err != nil {
		t.Fatalf("makeSteerActivity without sender: %v", err)
	}
	detail = nil
	if err := json.Unmarshal(withoutSender.Detail, &detail); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"senderSessionId", "senderProjectId", "senderDisplayName"} {
		if _, ok := detail[key]; ok {
			t.Errorf("detail unexpectedly contains %q without sender", key)
		}
	}
}
