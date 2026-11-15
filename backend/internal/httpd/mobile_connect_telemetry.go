package httpd

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const mobileDeviceConnectedEvent = "ao.mobile.device_connected"

const connectedReportWindow = 24 * time.Hour

var tailscaleCGNAT = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

func transportForSource(host string) string {
	ip := net.ParseIP(host)
	if ip == nil {
		return "other"
	}
	switch {
	case ip.IsLoopback():
		return "loopback"
	case tailscaleCGNAT.Contains(ip):
		return "tailscale"
	case ip.IsPrivate() || ip.IsLinkLocalUnicast():
		return "lan"
	default:
		return "other"
	}
}

type mobileConnectReporter struct {
	sink ports.EventSink
	now  func() time.Time

	mu   sync.Mutex
	last map[string]time.Time
}

func newMobileConnectReporter(sink ports.EventSink, now func() time.Time) *mobileConnectReporter {
	if sink == nil {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	return &mobileConnectReporter{sink: sink, now: now, last: map[string]time.Time{}}
}

func (r *mobileConnectReporter) report(host string) {
	if r == nil {
		return
	}
	transport := transportForSource(host)
	now := r.now()

	r.mu.Lock()
	if last, ok := r.last[transport]; ok && now.Sub(last) < connectedReportWindow {
		r.mu.Unlock()
		return
	}
	r.last[transport] = now
	r.mu.Unlock()

	r.sink.Emit(context.Background(), ports.TelemetryEvent{
		Name:       mobileDeviceConnectedEvent,
		Source:     "daemon",
		OccurredAt: now.UTC(),
		Level:      ports.TelemetryLevelInfo,
		Payload:    map[string]any{"transport": transport},
	})
}
