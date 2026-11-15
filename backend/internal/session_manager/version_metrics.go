package sessionmanager

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const EventVersionSwitch = "ao.harnesses.version_switched"

func (m *Manager) SetVersionTelemetry(sink ports.EventSink) {
	m.versionTelemetryMu.Lock()
	defer m.versionTelemetryMu.Unlock()
	m.versionTelemetry = sink
}

func (m *Manager) emitVersionSwitch(rec domain.SessionRecord, mode, result string) {
	m.versionTelemetryMu.Lock()
	sink := m.versionTelemetry
	m.versionTelemetryMu.Unlock()
	if sink == nil {
		return
	}
	payload := map[string]any{"harness": string(rec.Harness), "mode": mode, "result": result}
	if rec.StaleBinary != nil && !rec.StaleBinary.Since.IsZero() {
		payload["latency_ms"] = time.Since(rec.StaleBinary.Since).Milliseconds()
	}
	sink.Emit(context.Background(), ports.TelemetryEvent{
		Name: EventVersionSwitch, Source: "session_manager", OccurredAt: time.Now().UTC(),
		Level: ports.TelemetryLevelInfo, ProjectID: &rec.ProjectID, SessionID: &rec.ID, Payload: payload,
	})
}

func (m *Manager) ObserveChatVersionSwitch(id domain.SessionID, _, result string) {
	rec, err := m.getRecord(context.Background(), id)
	if err != nil {
		rec = domain.SessionRecord{ID: id}
	}
	m.emitVersionSwitch(rec, "chat", result)
	if result == "failed" {
		m.versionRetries.note(id)
	}
}
