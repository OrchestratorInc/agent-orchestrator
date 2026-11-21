//go:build e2e

package cli_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type serveJSON struct {
	Status string `json:"status"`
	Port   int    `json:"port"`
	PID    int    `json:"pid"`
	URL    string `json:"url"`
	Reused bool   `json:"reused"`
}

// TestE2E_ServeStartsReusesAndStops drives the real binary: serve starts a
// detached daemon and waits for it, a second serve reuses it instead of
// blocking on the data-dir lock, and `ao stop` stops it.
func TestE2E_ServeStartsReusesAndStops(t *testing.T) {
	e := newEnv(t)
	t.Cleanup(func() { e.run(t, "stop") })

	out, code := e.run(t, "serve", "--json")
	if code != 0 {
		t.Fatalf("serve exit %d:\n%s", code, out)
	}
	var first serveJSON
	if err := json.Unmarshal([]byte(out), &first); err != nil {
		t.Fatalf("decode serve output: %v\n%s", err, out)
	}
	if first.Status != "ready" || first.Port != e.port || first.PID == 0 || first.Reused {
		t.Fatalf("first serve = %#v", first)
	}

	out, code = e.run(t, "serve", "--json")
	if code != 0 {
		t.Fatalf("second serve exit %d:\n%s", code, out)
	}
	var second serveJSON
	if err := json.Unmarshal([]byte(out), &second); err != nil {
		t.Fatalf("decode serve output: %v\n%s", err, out)
	}
	if !second.Reused || second.PID != first.PID {
		t.Fatalf("second serve did not reuse the daemon: %#v", second)
	}

	out, _ = e.run(t, "status", "--json")
	mustContain(t, out, `"state": "ready"`)
	mustContain(t, out, fmt.Sprintf(`"pid": %d`, first.PID))

	out, code = e.run(t, "stop")
	if code != 0 || !strings.Contains(out, "stopped") {
		t.Fatalf("stop exit %d:\n%s", code, out)
	}
	out, _ = e.run(t, "status", "--json")
	mustContain(t, out, `"state": "stopped"`)
}
