package systeminstall

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Telemetry event names for updates that meet running sessions.
const (
	// EventHarnessUpdate records an update request's outcome: refused for
	// consent or for sessions that must end, waiting, succeeded or failed.
	EventHarnessUpdate = "ao.harness.update"
	// EventHarnessIdleWait records how long a Windows update waited for idle sessions.
	EventHarnessIdleWait = "ao.harness.update_idle_wait"
)

// SetTelemetry late-binds the daemon's telemetry sink.
func (s *Service) SetTelemetry(sink ports.EventSink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.telemetry = sink
}

func (s *Service) emit(name string, payload map[string]any) {
	s.mu.Lock()
	sink := s.telemetry
	s.mu.Unlock()
	if sink == nil {
		return
	}
	sink.Emit(context.Background(), ports.TelemetryEvent{
		Name: name, Source: "systeminstall", OccurredAt: time.Now().UTC(),
		Level: ports.TelemetryLevelInfo, Payload: payload,
	})
}

func (s *Service) emitHarnessUpdate(target Target, level UpdateWhileRunning, result string, liveSessions int) {
	if level == "" {
		level = UpdateWhileRunningBlocked
	}
	s.emit(EventHarnessUpdate, map[string]any{
		"harness": string(target), "level": string(level), "result": result, "live_sessions": liveSessions,
	})
}
