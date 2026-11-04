package agentaccount

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestClaudeLimitsAutoResumeResetUsesOnlyUnambiguousRollingWindows(t *testing.T) {
	now := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	const first = `{"utilization":100,"resets_at":"2026-08-02T10:01:00Z"}`
	for _, tc := range []struct {
		name  string
		body  string
		reset int64
	}{
		{"global session", `{"five_hour":` + first + `}`, 1785664860},
		{"latest saturated global window", `{"five_hour":` + first + `,"seven_day":{"utilization":100,"resets_at":"2026-08-02T10:02:00Z"}}`, 1785664920},
		{"unsaturated model is not blocking", `{"five_hour":` + first + `,"seven_day_opus":{"utilization":20}}`, 1785664860},
		{"saturated model cannot identify failed scope", `{"five_hour":` + first + `,"seven_day_opus":` + first + `}`, 0},
		{"unknown model scope", `{"five_hour":` + first + `,"model_scoped":[{"display_name":"other","utilization":100,"resets_at":"2026-08-02T10:02:00Z"}]}`, 0},
		{"incomplete model meter", `{"five_hour":` + first + `,"seven_day_sonnet":{}}`, 0},
		{"paid spend is ambiguous", `{"five_hour":` + first + `,"spend":{"enabled":true}}`, 0},
		{"disabled extra usage", `{"five_hour":` + first + `,"spend":{"enabled":false}}`, 1785664860},
		{"other saturated window missing reset", `{"five_hour":` + first + `,"seven_day":{"utilization":100}}`, 0},
		{"missing utilization", `{"five_hour":` + first + `,"seven_day":{"resets_at":"2026-08-02T10:02:00Z"}}`, 0},
		{"invalid utilization", `{"five_hour":{"utilization":101,"resets_at":"2026-08-02T10:01:00Z"}}`, 0},
		{"elapsed reset", `{"five_hour":{"utilization":100,"resets_at":"2026-08-02T10:00:00Z"}}`, 0},
		{"display date has no timezone", `{"five_hour":{"utilization":100,"resets_at":"10:01am"}}`, 0},
		{"not saturated", `{"five_hour":{"utilization":99,"resets_at":"2026-08-02T10:01:00Z"}}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body claudeUsageBody
			if err := json.Unmarshal([]byte(`{"rate_limits":`+tc.body+`}`), &body); err != nil {
				t.Fatal(err)
			}
			got := body.limits(now)
			if (tc.reset == 0 && got.AutoResumeResetsAt != nil) ||
				(tc.reset != 0 && (got.AutoResumeResetsAt == nil || got.AutoResumeResetsAt.Unix() != tc.reset)) {
				t.Fatalf("safe account reset = %v, want Unix %d", got.AutoResumeResetsAt, tc.reset)
			}
		})
	}
}

type limitsAccountFactory struct {
	ports.CodexAccountClientFactory
	client *limitsAccountClient
	home   string
}

func (f *limitsAccountFactory) Open(_ context.Context, account ports.CodexAccountContext) (ports.CodexAccountClient, error) {
	f.home = account.Home
	return f.client, nil
}

type limitsAccountClient struct {
	ports.CodexAccountClient
	capacity ports.CodexCapacityObservation
}

func (c *limitsAccountClient) ReadCapacity(context.Context) (ports.CodexCapacityObservation, error) {
	return c.capacity, nil
}

func (*limitsAccountClient) ReadUsage(context.Context) (ports.CodexUsageObservation, error) {
	return ports.CodexUsageObservation{}, errors.New("activity unavailable")
}

func (*limitsAccountClient) Close() error { return nil }

func TestCodexLimitsPreservesPrivateSafeReset(t *testing.T) {
	reset := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	factory := &limitsAccountFactory{client: &limitsAccountClient{capacity: ports.CodexCapacityObservation{
		AutoResumeResetsAt: &reset,
		Overall: &domain.CodexCapacityBucket{LimitID: "codex", Primary: &domain.CodexCapacityWindow{
			UsedPercent: 100, ResetsAt: &reset,
		}},
	}}}
	cli := &Codex{Accounts: factory}
	for _, known := range []bool{true, false} {
		if !known {
			factory.client.capacity.AutoResumeResetsAt = nil
		}
		limits, err := cli.Limits(context.Background(), "/bound-codex")
		if err != nil || factory.home != "/bound-codex" || len(limits.Windows) != 1 {
			t.Fatalf("limits = %+v, home = %q, error = %v", limits, factory.home, err)
		}
		if known != (limits.AutoResumeResetsAt != nil) || (known && !limits.AutoResumeResetsAt.Equal(reset)) {
			t.Fatalf("native safe deadline was lost or inferred from display: %+v", limits)
		}
		encoded, err := json.Marshal(limits)
		if err != nil || strings.Contains(string(encoded), "AutoResume") || strings.Contains(string(encoded), "autoResume") {
			t.Fatalf("private reset leaked into account JSON: %s, %v", encoded, err)
		}
	}
}
