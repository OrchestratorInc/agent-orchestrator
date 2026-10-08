package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
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

// TestTelemetryWiringAggregatesUsageErrorsAndPreservesCountOnTheWire builds
// the real aggregation -> rate-limit -> PostHog chain (the same shape
// newTelemetrySink wires up), using the actual aggregatedEventNames list, and
// asserts an event named exactly as httpd/router.go emits it
// ("ao.cli.usage_errors") both gets folded into one rollup AND that the
// rollup's count survives PostHog's remote payload allowlist onto the wire.
// This is the test that would have caught two real bugs during review: an
// aggregatedEventNames entry that didn't match the emitted event name
// (silently skipping aggregation for that name), and count/window_start/
// window_end being absent from the allowlist (silently stripped before
// reaching PostHog even once aggregation ran correctly).
func TestTelemetryWiringAggregatesUsageErrorsAndPreservesCountOnTheWire(t *testing.T) {
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

	rateLimited := telemetryadapter.NewRateLimitedSink(remote, aggregatedEventNames)
	// flushEvery is intentionally long: the test controls flushing via
	// Close(), not the background ticker, so it can't be flaky on timing.
	aggregated := telemetryadapter.NewAggregatingSink(rateLimited, aggregatedEventNames, time.Hour)

	for i := 0; i < 3; i++ {
		aggregated.Emit(context.Background(), ports.TelemetryEvent{
			Name:   "ao.cli.usage_errors",
			Source: "cli",
			Level:  ports.TelemetryLevelWarn,
			Payload: map[string]any{
				"component":  "cli",
				"operation":  "command_parse",
				"error_kind": "usage",
			},
		})
	}
	if err := aggregated.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

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
		if ws, ok := props["window_start"].(string); !ok || ws == "" {
			t.Fatalf("properties.window_start = %#v, want a non-empty timestamp string", props["window_start"])
		}
		if we, ok := props["window_end"].(string); !ok || we == "" {
			t.Fatalf("properties.window_end = %#v, want a non-empty timestamp string", props["window_end"])
		}
		if props["component"] != "cli" || props["operation"] != "command_parse" {
			t.Fatalf("rollup lost sample dims: %#v", props)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PostHog sink did not send the aggregated rollup")
	}
}
