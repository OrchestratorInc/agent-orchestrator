package host

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Every paused request and response goes on at once, but a document's Basic
// challenge: it opens the sign-in card (saying so when the password was
// refused), and after Cancel it is the server's own 401 page, without the
// challenge Chrome would show an empty error page for.
func TestBasicChallengePaused(t *testing.T) {
	sent := make(chan cdpMsg, 16)
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
			sent <- m
			_ = c.Write(r.Context(), websocket.MessageText, fmt.Appendf(nil, `{"id":%d,"result":{"body":"401 page"}}`, m.ID))
		}
	}))
	defer srv.Close()
	cdp, err := dialCDP(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	v := &viewer{h: &host{cdp: cdp}, ctx: ctx, cancel: cancel, session: "S", out: make(chan []byte, 8)}
	event := func(method, params string) {
		v.onEvent("S", cdpMsg{Method: method, Params: json.RawMessage(params), SessionID: "S"})
	}
	paused := func(status int, challenge string) {
		event("Fetch.requestPaused", fmt.Sprintf(`{"requestId":"R","request":{"url":"https://a.test/x"},"responseStatusCode":%d,
			"responseHeaders":[{"name":"Content-Type","value":"text/html"},{"name":"WWW-Authenticate","value":%q}]}`, status, challenge))
	}
	check := func(name, card string, cmds ...string) {
		t.Helper()
		got := ""
		select {
		case b := <-v.out:
			got = string(b)
		default:
		}
		if got != card {
			t.Errorf("%s: card %s, want %s", name, got, card)
		}
		for _, want := range cmds {
			select {
			case m := <-sent:
				if got := m.Method + " " + string(m.Params); got != want {
					t.Errorf("%s: sent %s, want %s", name, got, want)
				}
			case <-time.After(5 * time.Second):
				t.Errorf("%s: never sent %s", name, want)
			}
		}
	}
	const (
		next  = `Fetch.continueRequest {"requestId":"R"}`
		card  = `{"failed":false,"origin":"https://a.test","realm":"rb","t":"auth","url":"https://a.test/x"}`
		basic = `Basic realm="rb"`
	)
	paused(0, "")
	check("a signed-in origin's request", "", next)
	paused(200, basic)
	check("a page", "", next)
	paused(401, `Bearer realm="api"`)
	check("another scheme's challenge", "", next)
	paused(401, basic)
	check("a Basic challenge", card, next)

	v.auth, v.authTried, v.authFailed = map[string]*authEntry{"https://a.test": {user: "u", pass: "p"}}, map[string]bool{}, map[string]bool{}
	for _, answer := range []string{`{"password":"p","response":"ProvideCredentials","username":"u"}`, `{"response":"CancelAuth"}`} {
		event("Fetch.authRequired", `{"requestId":"R","authChallenge":{"origin":"https://a.test","scheme":"basic"}}`)
		check("signing in", "", `Fetch.continueWithAuth {"authChallengeResponse":`+answer+`,"requestId":"R"}`)
	}
	paused(401, basic)
	check("a refused password", strings.Replace(card, "false", "true", 1), next)

	v.auth["https://a.test"] = &authEntry{cancel: true}
	paused(401, basic)
	check("Cancel", "", `Fetch.getResponseBody {"requestId":"R"}`,
		`Fetch.fulfillRequest {"body":"NDAxIHBhZ2U=","requestId":"R","responseCode":401,"responseHeaders":[{"name":"Content-Type","value":"text/html"}]}`)
	paused(401, basic)
	check("a challenge after Cancel", card, next)
}

// A page on the internet may not name a local address as its icon, in any
// form Chrome reads as one.
func TestLocalHost(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "app.localhost": true, "127.0.0.1": true, "::1": true,
		"10.0.0.8": true, "192.168.1.1": true, "169.254.169.254": true, "0.0.0.0": true,
		"example.com": false, "8.8.8.8": false, "": false, "localhost.example.com": false,
		// What the URL standard turns into 127.0.0.1, 0.0.0.0 or localhost.
		"2130706433": true, "127.1": true, "0x7f000001": true, "0177.0.0.1": true, "127.0.0.1.": true,
		"0": true, "0x": true, "0x7f.1": true, "10.1": true, "LOCALHOST": true, "localhost.": true,
		"App.LocalHost.": true, "::ffff:127.0.0.1": true, "１２７.０.０.１": true,
		// ...and what it does not: 8.8.8.8 as one number, too many parts, out of range.
		"134744072": false, "0x8.0x8.0x8.0x8": false, "1.2.3.4.5": false, "256.0.0.1": false,
		"4294967296": false, "08.0.0.1": false, "127..1": false, "example.1": false, "localhost..": false,
	} {
		if got := localHost(host); got != want {
			t.Errorf("localHost(%q) = %v, want %v", host, got, want)
		}
	}
}

// A Basic challenge opens the sign-in card wherever it is among the header's challenges.
func TestBasicRealm(t *testing.T) {
	for value, want := range map[string]string{
		`Basic realm="x"`: "x", `basic`: "", "Negotiate\nBasic realm=\"x\"": "x",
		`Negotiate, Basic realm="x"`: "x", `Bearer realm="api", Basic realm="x"`: "x",
		`Bearer realm="api"`: "-", `Negotiate`: "-", `Basicx realm="x"`: "-", `Bearer realm="basic"`: "-",
	} {
		got := "-"
		if m := basicRealm.FindStringSubmatch(value); m != nil {
			got = m[1]
		}
		if got != want {
			t.Errorf("realm of %q = %q, want %q", value, got, want)
		}
	}
}
