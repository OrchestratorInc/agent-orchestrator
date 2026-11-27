package telemetry

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// ExportedSourcePrefix marks a local telemetry row that records an event as it
// was exported to PostHog: source "exported:daemon" for the daemon and CLI,
// "exported:desktop" for the desktop renderer. These rows share the local
// table's retention (AO_TELEMETRY_RETENTION, 30 days by default) and back
// `ao telemetry log`. They never leave the machine.
const ExportedSourcePrefix = "exported:"

// ExportedEvent is one event as it was sent.
type ExportedEvent struct {
	OccurredAt time.Time      `json:"occurredAt"`
	Event      string         `json:"event"`
	Surface    string         `json:"surface"`
	DistinctID string         `json:"distinctId,omitempty"`
	Properties map[string]any `json:"properties"`
}

// RecordExported writes one exported event to the local store through sink.
func RecordExported(ctx context.Context, sink ports.EventSink, ev ExportedEvent) {
	if sink == nil || ev.Event == "" || ev.Surface == "" {
		return
	}
	occurred := ev.OccurredAt
	if occurred.IsZero() {
		occurred = time.Now()
	}
	sink.Emit(ctx, ports.TelemetryEvent{
		Name:       ev.Event,
		Source:     ExportedSourcePrefix + ev.Surface,
		OccurredAt: occurred.UTC(),
		Level:      ports.TelemetryLevelInfo,
		Payload:    map[string]any{"distinct_id": ev.DistinctID, "properties": ev.Properties},
	})
}

// DecodeExported turns stored rows back into exported events. A row whose
// payload cannot be read is skipped rather than shown wrong.
func DecodeExported(rows []gen.TelemetryEvent) []ExportedEvent {
	out := make([]ExportedEvent, 0, len(rows))
	for _, row := range rows {
		surface, ok := strings.CutPrefix(row.Source, ExportedSourcePrefix)
		if !ok {
			continue
		}
		var payload struct {
			DistinctID string         `json:"distinct_id"`
			Properties map[string]any `json:"properties"`
		}
		if err := json.Unmarshal([]byte(row.PayloadJson), &payload); err != nil {
			continue
		}
		out = append(out, ExportedEvent{OccurredAt: row.OccurredAt.UTC(), Event: row.Name, Surface: surface, DistinctID: payload.DistinctID, Properties: payload.Properties})
	}
	return out
}
