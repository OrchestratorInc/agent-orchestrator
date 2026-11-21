package copilot

import (
	"os"
	"testing"
)

// TestMain keeps every test's Copilot trust writes out of the real ~/.copilot.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "copilot-home-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("COPILOT_HOME", home)
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
