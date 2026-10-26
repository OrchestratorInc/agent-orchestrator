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

// A message Chrome would close the connection for is refused here, and the
// connection, which every view shares, keeps working. When the connection
// does go, done says so: rbhost then starts over.
func TestCDPMessageLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			_, data, err := c.Read(r.Context())
			var m cdpMsg
			if err != nil || json.Unmarshal(data, &m) != nil || m.Method == "Test.hangUp" {
				return
			}
			_ = c.Write(r.Context(), websocket.MessageText, fmt.Appendf(nil, `{"id":%d,"result":{}}`, m.ID))
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	c, err := dialCDP(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"))
	if err != nil {
		t.Fatal(err)
	}
	// Pasted text of '<' grows sixfold as JSON (<).
	if _, err := c.Call(ctx, "", "Input.insertText", map[string]any{"text": strings.Repeat("<", maxMessage/6+1)}); err == nil {
		t.Error("a message over the limit was sent")
	}
	if _, err := c.Call(ctx, "", "Browser.getVersion", nil); err != nil {
		t.Errorf("the connection broke: %v", err)
	}
	_, _ = c.Call(ctx, "", "Test.hangUp", nil)
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		t.Error("done stayed open after the connection closed")
	}
}
