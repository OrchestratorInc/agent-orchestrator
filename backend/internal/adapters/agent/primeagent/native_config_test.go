package primeagent

import (
	"context"
	"path/filepath"
	"testing"
)

func TestPrimeNativeConfigPinsImportedRootAndEmptyOverrideSemantics(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PRIME_AGENT_SESSION_DIR", "inherited-source")
	env := map[string]string{
		"HOME":                                home,
		"PRIME_AGENT_SESSION_DIR":             "~/selected",
		"PRIME_AGENT_CODING_AGENT_SESSION_DIR": "~/legacy",
		"PRIME_AGENT_CODING_AGENT_DIR":         filepath.Join(home, "managed"),
	}
	root, err := New().NativeSessionConfigDir(context.Background(), env)
	if err != nil || root != filepath.Join(home, "selected") {
		t.Fatalf("selected root = %q, %v", root, err)
	}
	env["PRIME_AGENT_SESSION_DIR"] = ""
	root, err = New().NativeSessionConfigDir(context.Background(), env)
	if err != nil || root != filepath.Join(home, "managed", "sessions") {
		t.Fatalf("empty primary must suppress legacy root: %q, %v", root, err)
	}
}
