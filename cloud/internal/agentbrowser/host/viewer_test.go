package host

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// A page can call the shims' binding itself, from any frame and whenever it
// likes: what it puts on the person's clipboard gets through only as a tab's
// write would, from its main frame and just after the person's click or key.
func TestClipboardReports(t *testing.T) {
	v := &viewer{h: &host{}, session: "S", targetID: "T", shimQ: make(chan func(), 1),
		ctxFrame: map[string]string{"S/1": "T", "S/2": "F"}, children: map[string]string{"C": "S"}}
	relayed := func(sid string, ctx int64, payload string) bool {
		params, _ := json.Marshal(map[string]any{"name": "__rbUI", "payload": payload, "executionContextId": ctx})
		v.onEvent(sid, cdpMsg{Method: "Runtime.bindingCalled", Params: params, SessionID: sid})
		select {
		case <-v.shimQ:
			return true
		default:
			return false
		}
	}
	copied := `{"kind":"clipboard","text":"x"}`
	if relayed("S", 1, copied) {
		t.Error("relayed with no click or key from the person")
	}
	v.lastGesture = time.Now()
	for _, c := range []struct {
		name, sid string
		ctx       int64
		payload   string
		want      bool
	}{
		{"main frame", "S", 1, copied, true},
		{"iframe", "S", 2, copied, false},
		{"cross-site iframe", "C", 1, copied, false},
		{"kind hidden from a Go struct", "S", 2, `{"kind":"clipboard","Kind":"tooltip","text":"x"}`, false},
		{"another report from an iframe", "S", 2, `{"kind":"tooltip","text":"x"}`, true},
	} {
		if got := relayed(c.sid, c.ctx, c.payload); got != c.want {
			t.Errorf("%s: relayed %v, want %v", c.name, got, c.want)
		}
	}
	v.lastGesture = time.Now().Add(-activation)
	if relayed("S", 1, copied) {
		t.Error("relayed after the person's click or key had expired")
	}
}

// During a cross-site navigation Chromium briefly refuses the tab's history.
// Back and Forward must still end up right: the view asks again, and asks
// once more when the load stops.
func TestHistoryAfterCrossSiteNavigation(t *testing.T) {
	var refusals atomic.Int32 // history requests still to refuse
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			_, data, err := c.Read(r.Context())
			var m cdpMsg
			if err != nil || json.Unmarshal(data, &m) != nil {
				return
			}
			reply := fmt.Sprintf(`{"id":%d,"result":{"currentIndex":1,"entries":[{},{}]}}`, m.ID)
			if refusals.Add(-1) >= 0 {
				reply = fmt.Sprintf(`{"id":%d,"error":{"code":-32000,"message":"Not attached to an active page"}}`, m.ID)
			}
			_ = c.Write(r.Context(), websocket.MessageText, []byte(reply))
		}
	}))
	defer srv.Close()
	cdp, err := dialCDP(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	v := &viewer{h: &host{cdp: cdp}, ctx: ctx, cancel: cancel, session: "S", targetID: "T", out: make(chan []byte, 8)}
	history := func(name string) {
		t.Helper()
		deadline := time.After(3 * time.Second)
		for {
			select {
			case b := <-v.out:
				if strings.Contains(string(b), `"t":"history"`) {
					if want := `{"back":true,"fwd":false,"t":"history"}`; string(b) != want {
						t.Errorf("%s: sent %s, want %s", name, b, want)
					}
					return
				}
			case <-deadline:
				t.Errorf("%s: Back and Forward were never sent", name)
				return
			}
		}
	}
	navigated := cdpMsg{Method: "Page.frameNavigated", Params: json.RawMessage(`{"frame":{"id":"T","url":"https://b.test/"}}`), SessionID: "S"}

	refusals.Store(1) // refused at first, answered on a retry
	v.onEvent("S", navigated)
	history("a retry after the navigation")

	refusals.Store(1 << 20) // refused until the load stops
	v.onEvent("S", navigated)
	time.Sleep(historyTries * historyRetry * 2)
	refusals.Store(0)
	v.onEvent("S", cdpMsg{Method: "Page.frameStoppedLoading", Params: json.RawMessage(`{"frameId":"T"}`), SessionID: "S"})
	history("the load stopping")
}

// History reads run beside each other: one that answers after a newer one
// was sent (here the server holds the first until then) must not undo it.
func TestHistoryDropsAnOlderReadThatAnswersLate(t *testing.T) {
	received := make(chan struct{}, 2)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		var held int64
		for {
			_, data, err := c.Read(r.Context())
			var m cdpMsg
			if err != nil || json.Unmarshal(data, &m) != nil {
				return
			}
			received <- struct{}{}
			if held == 0 {
				held = m.ID // the older page: no Back yet
				continue
			}
			_ = c.Write(r.Context(), websocket.MessageText, fmt.Appendf(nil, `{"id":%d,"result":{"currentIndex":1,"entries":[{},{}]}}`, m.ID))
			<-release
			_ = c.Write(r.Context(), websocket.MessageText, fmt.Appendf(nil, `{"id":%d,"result":{"currentIndex":0,"entries":[{},{}]}}`, held))
		}
	}))
	defer srv.Close()
	cdp, err := dialCDP(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	v := &viewer{h: &host{cdp: cdp}, ctx: ctx, cancel: cancel, session: "S", targetID: "T", out: make(chan []byte, 8)}
	go v.sendHistory()
	<-received
	go v.sendHistory()
	<-received
	newer := `{"back":true,"fwd":false,"t":"history"}`
	select {
	case b := <-v.out:
		if string(b) != newer {
			t.Fatalf("sent %s first, want the newer read: %s", b, newer)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the newer read was never sent")
	}
	close(release)
	select {
	case b := <-v.out:
		t.Fatalf("the older read was sent after the newer: %s", b)
	case <-time.After(500 * time.Millisecond):
	}
}
