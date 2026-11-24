package httpd

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// A phone that has paired is indistinguishable from one that has not, from the
// desktop's point of view: pairing mints no device record, no handshake, and no
// session. The phone simply starts sending password-authenticated requests to
// the LAN listener. So the only honest signal for "a phone actually connected"
// is the first request that passes authMiddleware on that listener, which is
// what this reports.
//
// Everything the renderer can see stops at intent — the modal was opened, the
// bridge was switched on. Both happen whether or not anyone ever scans the QR.
const mobileDeviceConnectedEvent = "ao.mobile.device_connected"

// connectedReportWindow is why this type exists rather than an Emit call inside
// authMiddleware. Every request from a paired phone passes that middleware,
// including each image, stylesheet, and script of a preview page (those
// authenticate by cookie). Reporting per request would emit thousands of events
// per phone per minute, which is the exact shape of stream that produced AO's
// PostHog bill.
//
// One event per transport per day answers the question that was asked — how
// many installs have a phone connected, and over which transport — at a volume
// of at most a handful of events per install per day.
const connectedReportWindow = 24 * time.Hour

// tailscaleCGNAT is the 100.64.0.0/10 range Tailscale assigns to its nodes.
// Distinguishing it from an ordinary private address is worth the few lines
// because the Connect Mobile UI asks the user to choose between LAN and
// Tailscale, and nothing currently shows which choice actually works for them.
var tailscaleCGNAT = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// transportForSource buckets a source address into a fixed vocabulary. The
// vocabulary is closed on purpose: the value is a low-cardinality dimension,
// never an address, so no part of a user's network layout is reported.
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

// mobileConnectReporter reports the first authenticated LAN request per
// transport per window.
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

// report records a successful authentication from host. Safe on a nil receiver
// so callers with no sink (tests, telemetry disabled) need no branch, and safe
// for concurrent use because the LAN listener serves requests in parallel.
func (r *mobileConnectReporter) report(host string) {
	if r == nil {
		return
	}
	transport := transportForSource(host)
	now := r.now()

	r.mu.Lock()
	// The map is bounded by the closed transport vocabulary, so it cannot grow
	// with the number of phones or addresses seen.
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
