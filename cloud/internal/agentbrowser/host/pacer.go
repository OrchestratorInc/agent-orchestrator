package host

import "time"

// pacer sends screencast frames the way TCP BBR sends packets: it estimates
// the path's bottleneck bandwidth from per-frame delivery rates and its base
// round trip from the fastest recent frame, and keeps about one
// bandwidth-delay product of bytes in flight. A fixed frame count cannot do
// both: 4 starves a long path, while 8 queues an animating page's frames on a
// slow one and a click's frame waits behind them. Every 10 s it lets the
// frames in flight drain so the base round trip is measured without a queue.
type pacer struct {
	sent      map[int64]sentFrame
	inflight  int64 // bytes sent, not yet acknowledged
	delivered int64 // bytes acknowledged so far
	rates     []rateSample
	minRTT    time.Duration
	minRTTAt  time.Time
	draining  bool
	produced  int64 // bytes Chrome produced since the last quality decision
	// Pacing spreads frames at the estimated bandwidth so no standing queue
	// builds in front of the frame a click produces. The gain cycles like
	// BBR's: probe above the estimate for one round trip, drain below it for
	// one, then hold for six.
	nextSendAt time.Time
	cycleAt    time.Time
	cycle      int
}

type sentFrame struct {
	at              time.Time
	size            int64
	deliveredAtSend int64
	appLimited      bool // sent with the path far from full: its rate understates the path
}

type rateSample struct {
	at  time.Time
	bps float64 // bytes per second
}

func newPacer() *pacer { return &pacer{sent: map[int64]sentFrame{}} }

func (p *pacer) onSend(seq, size int64) {
	now := time.Now()
	limit := p.byteLimit()
	p.sent[seq] = sentFrame{at: now, size: size, deliveredAtSend: p.delivered, appLimited: limit == 0 || p.inflight+size < limit/2}
	p.inflight += size
	if bw := p.bandwidth(); bw > 0 {
		gap := time.Duration(float64(size) / (bw * p.pacingGain(now)) * float64(time.Second))
		p.nextSendAt = later(now, p.nextSendAt).Add(gap)
	}
}

func (p *pacer) pacingGain(now time.Time) float64 {
	if p.minRTT == 0 {
		return 2
	}
	if now.Sub(p.cycleAt) >= p.minRTT {
		p.cycle, p.cycleAt = (p.cycle+1)%8, now
	}
	switch p.cycle {
	case 0:
		return 1.25
	case 1:
		return 0.75
	default:
		return 1
	}
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// onAck takes a cumulative acknowledgement: the viewer acks the newest frame it drew.
func (p *pacer) onAck(seq int64) {
	now := time.Now()
	acked, ok := p.sent[seq]
	for s, f := range p.sent {
		if s <= seq {
			p.inflight -= f.size
			p.delivered += f.size
			delete(p.sent, s)
		}
	}
	if !ok {
		return
	}
	rtt := now.Sub(acked.at)
	if rtt > 0 {
		bps := float64(p.delivered-acked.deliveredAtSend) / rtt.Seconds()
		if !acked.appLimited || bps > p.bandwidth() {
			p.rates = append(p.rates, rateSample{at: now, bps: bps})
		}
	}
	if p.minRTT == 0 || rtt < p.minRTT || (!p.draining && now.Sub(p.minRTTAt) > 10*time.Second && acked.at.After(p.minRTTAt)) {
		p.minRTT, p.minRTTAt = rtt, now
	}
	if p.draining && p.inflight <= 0 {
		// The next frame goes out on an empty path; its round trip is the base.
		p.draining = false
		p.minRTT = 0
	}
}

// bandwidth is the highest delivery rate of the last 10 s. With no samples
// in that window (an idle page) it keeps the last estimate rather than
// forgetting the path.
func (p *pacer) bandwidth() float64 {
	cutoff := time.Now().Add(-10 * time.Second)
	keep := p.rates[:0]
	best := 0.0
	for _, r := range p.rates {
		if r.at.After(cutoff) {
			keep = append(keep, r)
			best = max(best, r.bps)
		}
	}
	if len(keep) == 0 && len(p.rates) > 0 {
		keep = append(keep, p.rates[len(p.rates)-1])
		best = keep[0].bps
	}
	p.rates = keep
	return best
}

// byteLimit is twice the bandwidth-delay product (BBR's congestion-window
// gain), or 0 while there is no estimate yet.
func (p *pacer) byteLimit() int64 {
	bw := p.bandwidth()
	if bw == 0 || p.minRTT == 0 {
		return 0
	}
	return int64(2 * bw * p.minRTT.Seconds())
}

// canSend reports whether a frame of size bytes may go out now, and if not
// only because of pacing, how long until it may.
func (p *pacer) canSend(size int64) (bool, time.Duration) {
	if len(p.sent) == 0 {
		return true, 0 // never stall: one frame is always allowed
	}
	if wait := time.Until(p.nextSendAt); wait > 0 {
		return false, wait
	}
	if !p.draining && p.minRTT > 0 && time.Since(p.minRTTAt) > 10*time.Second {
		p.draining = true
	}
	if p.draining {
		return false, 0
	}
	// Enough frames for 30 fps at the base round trip; bytes capped only once
	// the path's bandwidth binds (a slow link), so frames don't queue there.
	frames := 4
	if p.minRTT > 0 {
		frames = max(4, min(int(p.minRTT/(33*time.Millisecond))+2, 12))
	}
	if len(p.sent) >= frames {
		return false, 0
	}
	limit := p.byteLimit()
	return limit == 0 || p.inflight+size <= max(limit, size), 0
}

func paceStats(p *pacer) map[string]any {
	if p == nil {
		return nil
	}
	return map[string]any{"inflight": p.inflight, "mbps": p.bandwidth() * 8 / 1e6, "minRTTms": p.minRTT.Milliseconds(), "draining": p.draining}
}
