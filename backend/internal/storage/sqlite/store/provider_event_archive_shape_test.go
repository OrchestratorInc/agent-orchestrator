package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const fullArchiveEnvelope = `{"account":null,"activityKind":"","activityStatus":"","clientMessageId":"",` +
	`"controllerState":"","decisions":null,"delta":"","detail":null,"diff":null,"kind":"",` +
	`"mcpServers":null,"plan":null,"providerConversationId":"","providerEventId":"",` +
	`"providerItemAliases":null,"providerItemId":"","providerTurnId":"","rateLimits":null,` +
	`"requestId":"","reroute":null,"summary":"","text":"","threadState":null,"title":"",` +
	`"turnState":"","usage":null}`

func archivePayload(t *testing.T, full bool, fields map[string]any) string {
	t.Helper()
	record := map[string]any{}
	if full {
		if err := json.Unmarshal([]byte(fullArchiveEnvelope), &record); err != nil {
			t.Fatalf("decode full envelope: %v", err)
		}
	}
	record["kind"] = "turn.completed"
	for key, value := range fields {
		record[key] = value
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("encode archive record: %v", err)
	}
	return string(encoded)
}

func TestArchiveRowsOfEitherShapeReconcileAuthenticationAlike(t *testing.T) {
	cases := []struct {
		name      string
		fields    map[string]any
		recovered bool
	}{
		{
			name:      "completed turn without a native thread id",
			fields:    map[string]any{"providerTurnId": "provider-turn", "turnState": "completed"},
			recovered: true,
		},
		{
			name: "completed turn of a child thread",
			fields: map[string]any{
				"providerTurnId": "provider-turn", "turnState": "completed",
				"providerConversationId": "child",
			},
		},
		{
			name: "completed with an error message",
			fields: map[string]any{
				"providerTurnId": "provider-turn", "turnState": "completed", "error": "boom",
			},
		},
		{
			name: "completed with an error that has an empty message",
			fields: map[string]any{
				"providerTurnId": "provider-turn", "turnState": "completed", "error": "",
			},
		},
		{
			name:   "completion of another provider turn",
			fields: map[string]any{"providerTurnId": "another-turn", "turnState": "completed"},
		},
		{
			name:   "failed turn",
			fields: map[string]any{"providerTurnId": "provider-turn", "turnState": "failed"},
		},
		{
			name:   "no turn state",
			fields: map[string]any{"providerTurnId": "provider-turn"},
		},
	}

	for _, tc := range cases {
		for _, shape := range []struct {
			name string
			full bool
		}{{"full", true}, {"slim", false}} {
			t.Run(fmt.Sprintf("%s/%s", tc.name, shape.name), func(t *testing.T) {
				s, session, conversation := conversationFixture(t)
				ctx := context.Background()
				failureAt := histClock.Add(time.Second)
				account := domain.ConversationAccount{ReauthRequiredAt: &failureAt, ReauthReason: "expired"}
				if err := s.RecordAccount(ctx, conversation, account, failureAt); err != nil {
					t.Fatal(err)
				}
				startAt := failureAt.Add(time.Second)
				if err := s.AdoptProviderTurn(ctx, conversation, session, "gen-1", "turn", "provider-turn", startAt); err != nil {
					t.Fatal(err)
				}
				completedAt := startAt.Add(3 * time.Second)
				payload := archivePayload(t, shape.full, tc.fields)
				if _, err := s.ProjectProviderEvent(ctx, conversation, session, "gen-1", "completion", "turn.completed",
					payload, completedAt, func(txCtx context.Context) error {
						return s.SettleTurn(txCtx, conversation, "provider-turn", domain.TurnStateCompleted, "", completedAt)
					}); err != nil {
					t.Fatal(err)
				}

				reconciled, err := s.ReconcileConversationAuthentication(ctx, conversation, "gen-1", "provider-turn", completedAt)
				if err != nil {
					t.Fatal(err)
				}
				if recovered := reconciled.ReauthRequiredAt == nil; recovered != tc.recovered {
					t.Fatalf("recovered = %v, want %v for archive row %s", recovered, tc.recovered, payload)
				}
			})
		}
	}
}
