package cli

import (
	"os"
	"testing"
	"time"
)

// waitForFile polls until path has content, because startProcess returns as
// soon as the detached child starts.
func waitForFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			return string(data)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s (last error: %v)", path, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
