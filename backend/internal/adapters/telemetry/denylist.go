package telemetry

import (
	"context"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type DenylistSink struct {
	next     ports.EventSink
	denied   map[string]struct{}
	prefixes []string
}

func NewDenylistSink(next ports.EventSink, names []string) ports.EventSink {
	if next == nil || len(names) == 0 {
		return next
	}
	s := &DenylistSink{next: next, denied: make(map[string]struct{}, len(names))}
	for _, raw := range names {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			continue
		}
		if strings.HasSuffix(name, "*") {
			if prefix := strings.TrimSuffix(name, "*"); prefix != "" {
				s.prefixes = append(s.prefixes, prefix)
			}
			continue
		}
		s.denied[name] = struct{}{}
	}
	if len(s.denied) == 0 && len(s.prefixes) == 0 {
		return next
	}
	return s
}

func (s *DenylistSink) Emit(ctx context.Context, ev ports.TelemetryEvent) {
	if s.blocks(ev.Name) {
		return
	}
	s.next.Emit(ctx, ev)
}

func (s *DenylistSink) Close(ctx context.Context) error {
	return s.next.Close(ctx)
}

func EventDenied(names []string, event string) bool {
	s, ok := NewDenylistSink(NoopSink{}, names).(*DenylistSink)
	return ok && s.blocks(event)
}

func (s *DenylistSink) blocks(name string) bool {
	for _, candidate := range append([]string{name}, legacyEventNames[name]...) {
		lowered := strings.ToLower(strings.TrimSpace(candidate))
		if lowered == "" {
			continue
		}
		if _, ok := s.denied[lowered]; ok {
			return true
		}
		for _, prefix := range s.prefixes {
			if strings.HasPrefix(lowered, prefix) {
				return true
			}
		}
	}
	return false
}
