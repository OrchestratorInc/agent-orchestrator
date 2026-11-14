package copilot

import (
	"os"
	"testing"
)

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
