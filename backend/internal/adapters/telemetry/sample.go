package telemetry

import (
	"context"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// SamplingSink reduces remote error volume while leaving the local event sink
// untouched. The first occurrence of an error per UTC day is always sent so
// every affected install stays visible. Later occurrences of that same error
// are sent 1 in 100. Same error means the event name, operation, and error
// code or status.
type SamplingSink struct {
	next   ports.EventSink
	rates  map[string]float64
	mu     sync.Mutex
	counts map[string]int
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
	return &SamplingSink{next: next, rates: bounded, counts: map[string]int{}}
}

// Emit forwards a selected occurrence with its sampling rate.
func (s *SamplingSink) Emit(ctx context.Context, ev ports.TelemetryEvent) {
	rate, sampled := s.rates[ev.Name]
	if !sampled || s.next == nil {
		if s.next != nil {
			s.next.Emit(ctx, ev)
		}
		return
	}
	if rate <= 0 {
		return
	}
	keep, reportedRate := true, rate
	if rate < 1 {
		s.mu.Lock()
		key := sampleKey(ev)
		s.counts[key]++
		n := s.counts[key]
		s.mu.Unlock()
		keep = n == 1 || (n-1)%100 == 0
		reportedRate = 0.01
		if n == 1 {
			reportedRate = 1
		}
	}
	if !keep {
		return
	}
	sampledEvent := ev
	sampledEvent.Payload = make(map[string]any, len(ev.Payload)+1)
	for key, value := range ev.Payload {
		sampledEvent.Payload[key] = value
	}
	sampledEvent.Payload["sample_rate"] = reportedRate
	s.next.Emit(ctx, sampledEvent)
}

// Close forwards shutdown to the wrapped sink.
func (s *SamplingSink) Close(ctx context.Context) error { return s.next.Close(ctx) }

func sampleKey(ev ports.TelemetryEvent) string {
	operation, _ := ev.Payload["operation"].(string)
	code, _ := ev.Payload["error_code"].(string)
	if code == "" {
		switch status := ev.Payload["status"].(type) {
		case int:
			code = strconv.Itoa(status)
		case int64:
			code = strconv.FormatInt(status, 10)
		case float64:
			code = strconv.FormatInt(int64(status), 10)
		case string:
			code = status
		}
	}
	occurred := ev.OccurredAt
	if occurred.IsZero() {
		occurred = time.Now()
	}
	return ev.Name + "\x00" + operation + "\x00" + code + "\x00" + occurred.UTC().Format("2006-01-02")
}
