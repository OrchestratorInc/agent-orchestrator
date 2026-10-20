package telemetry

import (
	"context"
	"encoding/binary"
	"hash/fnv"
	"math"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// SamplingSink reduces remote error volume while leaving the local event sink
// untouched. A stable hash makes a retried occurrence take the same decision.
type SamplingSink struct {
	next  ports.EventSink
	rates map[string]float64
	hash  func(ports.TelemetryEvent) uint64
}

// NewSamplingSink wraps a remote sink with bounded per-name sampling rates.
func NewSamplingSink(next ports.EventSink, rates map[string]float64) *SamplingSink {
	bounded := make(map[string]float64, len(rates))
	for name, rate := range rates {
		switch {
		case math.IsNaN(rate) || rate <= 0:
			bounded[name] = 0
		case rate >= 1:
			bounded[name] = 1
		default:
			bounded[name] = rate
		}
	}
	return &SamplingSink{next: next, rates: bounded, hash: eventSampleHash}
}

// Emit forwards a selected occurrence with its sampling rate.
func (s *SamplingSink) Emit(ctx context.Context, ev ports.TelemetryEvent) {
	rate, sampled := s.rates[ev.Name]
	if !sampled {
		s.next.Emit(ctx, ev)
		return
	}
	if rate <= 0 || (rate < 1 && float64(s.hash(ev))/float64(^uint64(0)) >= rate) {
		return
	}
	sampledEvent := ev
	sampledEvent.Payload = make(map[string]any, len(ev.Payload)+1)
	for key, value := range ev.Payload {
		sampledEvent.Payload[key] = value
	}
	sampledEvent.Payload["sample_rate"] = rate
	s.next.Emit(ctx, sampledEvent)
}

// Close forwards shutdown to the wrapped sink.
func (s *SamplingSink) Close(ctx context.Context) error { return s.next.Close(ctx) }

func eventSampleHash(ev ports.TelemetryEvent) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(ev.Name))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(ev.OccurredAt.UTC().Format(time.RFC3339Nano)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(ev.RequestID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(ev.Source))
	return binary.BigEndian.Uint64(h.Sum(nil))
}
