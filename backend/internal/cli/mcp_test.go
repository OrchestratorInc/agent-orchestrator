package cli

import (
	"strings"
	"testing"
)

// `ao mcp` is the fallback for an install without the ao-mcp executable.
func TestMCPCommandServesTheTools(t *testing.T) {
	stdout, stderr, err := executeCLI(t, Deps{In: strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n")}, "mcp")
	if err != nil || stderr != "" || !strings.Contains(stdout, `"html_render"`) {
		t.Fatalf("ao mcp: err=%v stderr=%s stdout=%s", err, stderr, stdout)
	}
}
