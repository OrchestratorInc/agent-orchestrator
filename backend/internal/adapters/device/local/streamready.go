package local

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A simulator that has just booted has no display yet when the hub's stream
// helper attaches: the helper sends one frame, then nothing for ~10 s while it
// re-wires its capture. A viewer that connected in that window stayed black, so
// a boot waits until the stream is live. A live stream sends at least 5 frames
// a second, even for a still screen (the helper's idle floor).

const (
	streamLiveTimeout = 25 * time.Second
	// liveFrames frames within liveWindow mean the capture is running.
	liveFrames = 3
	liveWindow = time.Second
)

var mjpegBoundary = []byte("--frame")

// liveRhythm reports whether frames arriving at these times show a running
// capture: liveFrames of them within liveWindow.
func liveRhythm(arrivals []time.Time) bool {
	for i := liveFrames - 1; i < len(arrivals); i++ {
		if arrivals[i].Sub(arrivals[i-liveFrames+1]) <= liveWindow {
			return true
		}
	}
	return false
}

// awaitLiveStream reads the simulator's MJPEG stream until it is live, or
// gives up after streamLiveTimeout. Best effort: a failure only costs the
// viewer the old wait.
func (p *Provider) awaitLiveStream(ctx context.Context, device ports.DeviceInfo) {
	if device.Platform != ports.DevicePlatformIOS {
		return
	}
	endpoint, err := p.Hub(ctx)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, streamLiveTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.Origin+"/vendor/serve-sim/helper/"+url.PathEscape(device.ID)+"/stream.mjpeg", http.NoBody)
	if err != nil {
		return
	}
	for name, value := range endpoint.Headers {
		req.Header.Set(name, value)
	}
	res, err := (&http.Client{}).Do(req) //nolint:gosec // G704: the hub's own loopback origin
	if err != nil {
		return
	}
	defer func() { _ = res.Body.Close() }()
	start := time.Now()
	var arrivals []time.Time
	var tail []byte
	buf := make([]byte, 64<<10)
	for {
		n, readErr := res.Body.Read(buf)
		if n > 0 {
			chunk := make([]byte, 0, len(tail)+n)
			chunk = append(chunk, tail...)
			chunk = append(chunk, buf[:n]...)
			now := time.Now()
			for i := 0; ; {
				j := bytes.Index(chunk[i:], mjpegBoundary)
				if j < 0 {
					break
				}
				arrivals = append(arrivals, now)
				i += j + len(mjpegBoundary)
			}
			if keep := len(mjpegBoundary) - 1; len(chunk) > keep {
				tail = append([]byte(nil), chunk[len(chunk)-keep:]...)
			} else {
				tail = chunk
			}
			if liveRhythm(arrivals) {
				p.log.Debug("device stream is live", "device", device.ID, "after", time.Since(start))
				return
			}
		}
		if readErr != nil {
			return
		}
	}
}
