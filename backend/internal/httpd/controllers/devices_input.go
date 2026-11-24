package controllers

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/devices"
)

const (
	// A phone sends touches and buttons: small frames.
	pacedInputReadLimit = 64 << 10
	// The hub may answer on the same socket (Android video when asked for).
	pacedHubReadLimit = 16 << 20
)

// wantsPacedInput reports whether this request is a phone's input socket
// asking the daemon to replay its touches at the pace they were made
// (pace=1). Other clients keep the plain proxy.
func wantsPacedInput(r *http.Request, tail string) bool {
	if r.URL.Query().Get("pace") != "1" || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	return tail == "vendor/serve-emu/ws" || strings.HasSuffix(tail, "/ws") && strings.HasPrefix(tail, "vendor/serve-sim/helper/")
}

// pacedInput carries a phone's input socket to the hub itself instead of
// proxying bytes, so each stamped touch can be held until its turn
// (devices.InputPacer). What the hub sends back passes straight through.
func pacedInput(w http.ResponseWriter, r *http.Request, origin *url.URL, tail string, headers map[string]string) {
	hubURL := *origin
	switch hubURL.Scheme {
	case "https":
		hubURL.Scheme = "wss"
	default:
		hubURL.Scheme = "ws"
	}
	hubURL.Path = "/" + tail
	hubURL.RawPath = ""
	query := r.URL.Query()
	query.Del("pace")
	query.Del("repeat")
	hubURL.RawQuery = query.Encode()
	header := http.Header{}
	for name, value := range headers {
		header.Set(name, value)
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	hub, res, err := websocket.Dial(ctx, hubURL.String(), &websocket.DialOptions{
		HTTPHeader:      header,
		HTTPClient:      &http.Client{Transport: previewTransport},
		CompressionMode: websocket.CompressionDisabled,
	})
	if res != nil && res.Body != nil {
		_ = res.Body.Close()
	}
	if err != nil {
		http.Error(w, "Device stream unavailable", http.StatusBadGateway)
		return
	}
	defer hub.CloseNow() //nolint:errcheck // best-effort teardown
	hub.SetReadLimit(pacedHubReadLimit)

	// The session route already authenticated the caller, as for the proxy.
	phone, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer phone.CloseNow() //nolint:errcheck // best-effort teardown
	phone.SetReadLimit(pacedInputReadLimit)

	type input struct {
		kind websocket.MessageType
		data []byte
		due  time.Time
	}
	queue := make(chan input, 512)
	done := make(chan struct{}, 3)
	finish := func() {
		cancel()
		done <- struct{}{}
	}

	go func() { // phone → queue, stamped with when each touch is due
		defer finish()
		defer close(queue)
		var pacer devices.InputPacer
		for {
			kind, data, err := phone.Read(ctx)
			if err != nil {
				return
			}
			data, touchMS := devices.TakeInputTime(data, kind == websocket.MessageBinary)
			select {
			case queue <- input{kind: kind, data: data, due: pacer.Due(time.Now(), touchMS)}:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { // queue → hub, each at its time
		defer finish()
		timer := time.NewTimer(0)
		<-timer.C
		for msg := range queue {
			if wait := time.Until(msg.due); wait > 0 {
				timer.Reset(wait)
				select {
				case <-timer.C:
				case <-ctx.Done():
					return
				}
			}
			if err := hub.Write(ctx, msg.kind, msg.data); err != nil {
				return
			}
		}
	}()
	go func() { // hub → phone
		defer finish()
		for {
			kind, data, err := hub.Read(ctx)
			if err != nil {
				return
			}
			if err := phone.Write(ctx, kind, data); err != nil {
				return
			}
		}
	}()
	<-done
	_ = phone.Close(websocket.StatusNormalClosure, "")
	_ = hub.Close(websocket.StatusNormalClosure, "")
}
