package local

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// A Face ID iPhone has no Home button: Home is a quick swipe up from the
// bottom edge and the app switcher the same swipe held still part way up.
// serve-sim injects touches as the simulator's own digitizer, and a touch
// flagged with the bottom edge starts the system gesture, so both buttons are
// pressed as the gesture a person makes. Nothing is launched on the device
// (the agent driver's runner app came to the front first).

// bottomEdge is serve-sim's edge flag for a touch that starts on the bottom
// bezel.
const bottomEdge = 3

// iosTouchFrame is one serve-sim touch: a 0x03 tag, then JSON.
const iosTouchFrame = 0x03

type gestureStep struct {
	phase string
	y     float64
	// wait is how long after the previous step this one is sent.
	wait time.Duration
}

// homeIndicatorGesture is the swipe for Home ("home") or the app switcher
// ("recents"), or nil for any other button. Points are 0..1 of the screen.
func homeIndicatorGesture(button string) []gestureStep {
	const start = 0.999
	var steps []gestureStep
	swipe := func(to float64, moves int, every time.Duration) {
		steps = append(steps, gestureStep{phase: "begin", y: start})
		for i := 1; i <= moves; i++ {
			steps = append(steps, gestureStep{phase: "move", y: start - (start-to)*float64(i)/float64(moves), wait: every})
		}
	}
	switch button {
	case "home":
		// Quick and long: a flick to the middle.
		swipe(0.5, 8, 12*time.Millisecond)
		steps = append(steps, gestureStep{phase: "end", y: 0.5, wait: 12 * time.Millisecond})
	case "recents":
		// Slow, then still: iOS opens the switcher when the finger rests.
		swipe(0.65, 30, 20*time.Millisecond)
		steps = append(steps, gestureStep{phase: "end", y: 0.65, wait: 700 * time.Millisecond})
	default:
		return nil
	}
	return steps
}

// encodeGestureStep is the serve-sim frame for one step.
func encodeGestureStep(step gestureStep) []byte {
	body, _ := json.Marshal(map[string]any{"type": step.phase, "x": 0.5, "y": step.y, "edge": bottomEdge})
	return append([]byte{iosTouchFrame}, body...)
}

// pressByGesture swipes the home indicator on an iOS simulator through the
// hub's touch socket.
func (p *Provider) pressByGesture(ctx context.Context, device ports.DeviceInfo, button string) error {
	steps := homeIndicatorGesture(button)
	if steps == nil {
		return ports.ErrDeviceButtonUnsupported
	}
	endpoint, err := p.Hub(ctx)
	if err != nil {
		return err
	}
	socket, err := url.Parse(endpoint.Origin)
	if err != nil {
		return err
	}
	socket.Scheme = strings.Replace(socket.Scheme, "http", "ws", 1)
	socket.Path = "/vendor/serve-sim/helper/ws"
	socket.RawQuery = url.Values{"device": {device.ID}}.Encode()
	header := http.Header{}
	for name, value := range endpoint.Headers {
		header.Set(name, value)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, res, err := websocket.Dial(ctx, socket.String(), &websocket.DialOptions{HTTPHeader: header})
	if res != nil && res.Body != nil {
		_ = res.Body.Close()
	}
	if err != nil {
		return fmt.Errorf("device touch socket: %w", err)
	}
	defer conn.CloseNow() //nolint:errcheck // best-effort teardown
	for _, step := range steps {
		if step.wait > 0 {
			select {
			case <-time.After(step.wait):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if err := conn.Write(ctx, websocket.MessageBinary, encodeGestureStep(step)); err != nil {
			return fmt.Errorf("device touch socket: %w", err)
		}
	}
	return conn.Close(websocket.StatusNormalClosure, "")
}
