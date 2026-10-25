package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	telemetryadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/telemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestNewTelemetrySink_DefaultsToNoopWhenDisabled(t *testing.T) {
	sink, _ := newTelemetrySink(config.Config{}, nil, slog.Default())
	if _, ok := sink.(telemetryadapter.NoopSink); !ok {
		t.Fatalf("sink type = %T, want telemetry.NoopSink", sink)
	}
}

func TestNewTelemetrySink_MetricsOnlyDoesNotEnableEvents(t *testing.T) {
	sink, _ := newTelemetrySink(config.Config{Telemetry: config.TelemetryConfig{Metrics: true}}, nil, slog.Default())
	if _, ok := sink.(telemetryadapter.NoopSink); !ok {
		t.Fatalf("sink type = %T, want telemetry.NoopSink when only metrics are enabled", sink)
	}
}

func TestNewTelemetrySink_UsesLocalSQLiteWhenEnabled(t *testing.T) {
	dataDir := t.TempDir()
	store, err := sqlitetest.Open(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	sink, _ := newTelemetrySink(config.Config{Telemetry: config.TelemetryConfig{Events: true}, DataDir: dataDir}, store, slog.Default())
	local, ok := sink.(*telemetryadapter.LocalSQLiteSink)
	if !ok {
		t.Fatalf("sink type = %T, want *telemetry.LocalSQLiteSink", sink)
	}
	t.Cleanup(func() { _ = local.Close(t.Context()) })
}

func TestNewTelemetrySink_FanoutIncludesPostHogWhenConfigured(t *testing.T) {
	dataDir := t.TempDir()
	store, err := sqlitetest.Open(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	sink, _ := newTelemetrySink(config.Config{
		DataDir: dataDir,
		Telemetry: config.TelemetryConfig{
			Events:      true,
			Remote:      config.TelemetryRemotePostHog,
			PostHogKey:  "phc_test",
			PostHogHost: "https://us.i.posthog.com",
		},
	}, store, slog.Default())
	fanout, ok := sink.(*telemetryadapter.FanoutSink)
	if !ok {
		t.Fatalf("sink type = %T, want *telemetry.FanoutSink", sink)
	}
	t.Cleanup(func() { _ = fanout.Close(t.Context()) })
}

type wiringTestRoundTripper func(*http.Request) (*http.Response, error)

func (f wiringTestRoundTripper) Do(req *http.Request) (*http.Response, error) { return f(req) }

// TestTelemetryWiringDailyUsageSummaryPreservesCountOnTheWire builds the
// daily summary in front of PostHog, the shape newTelemetrySink wires up, and
// asserts ao.cli.usage_errors becomes one event whose count survives the
// allowlist. Raw error text must not.
func TestTelemetryWiringDailyUsageSummaryPreservesCountOnTheWire(t *testing.T) {
	requests := make(chan map[string]any, 1)
	remote, err := telemetryadapter.NewPostHogSink(t.TempDir(), "phc_test", "https://us.i.posthog.com", "", "",
		wiringTestRoundTripper(func(req *http.Request) (*http.Response, error) {
			defer req.Body.Close()
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				return nil, err
			}
			requests <- body
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody}, nil
		}), slog.Default())
	if err != nil {
		t.Fatalf("NewPostHogSink: %v", err)
	}

	daily := telemetryadapter.NewDailySummarySink(remote, filepath.Join(t.TempDir(), "cli-usage.json"))
	day := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		daily.Emit(context.Background(), ports.TelemetryEvent{
			Name:       "ao.cli.usage_errors",
			Source:     "cli",
			OccurredAt: day,
			Level:      ports.TelemetryLevelWarn,
			Payload: map[string]any{
				"component":    "cli",
				"operation":    "command_parse",
				"error_kind":   "usage",
				"command_path": "ao session",
				"error":        "do not send raw text",
			},
		})
	}
	daily.FlushCompleted(context.Background(), day.Add(24*time.Hour))

	select {
	case req := <-requests:
		if got := req["event"]; got != "ao.cli.usage_errors" {
			t.Fatalf("event = %#v, want ao.cli.usage_errors (aggregatedEventNames entry must match the real emit site)", got)
		}
		props, ok := req["properties"].(map[string]any)
		if !ok {
			t.Fatalf("properties type = %T, want map[string]any", req["properties"])
		}
		count, ok := props["count"].(float64) // JSON numbers decode as float64
		if !ok || count != 3 {
			t.Fatalf("properties.count = %#v, want 3 (rollup count must survive the PostHog allowlist)", props["count"])
		}
		if props["component"] != "cli" || props["operation"] != "command_parse" {
			t.Fatalf("summary lost sample dims: %#v", props)
		}
		if props["command_path"] != "ao session" || props["error_kind"] != "usage" {
			t.Fatalf("command properties = %#v", props)
		}
		if props["error"] != nil {
			t.Fatalf("raw error reached PostHog: %#v", props["error"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PostHog sink did not send the aggregated rollup")
	}
}
