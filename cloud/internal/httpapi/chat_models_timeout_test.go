package httpapi

import (
	"testing"
	"time"
)

// A cold worker can spend most of its 20 s discovery budget starting the
// Claude ACP adapter and probing approval modes, so a model catalog request
// waits longer than other worker requests, and never less than a configured
// longer default.
func TestChatModelsRequestWaitsForColdDiscovery(t *testing.T) {
	if got := (&Server{workerRequestTimeout: 12 * time.Second}).chatModelsRequestTimeout(); got != 25*time.Second {
		t.Fatalf("catalog wait with the 12 s default = %v, want 25s", got)
	}
	if got := (&Server{workerRequestTimeout: 40 * time.Second}).chatModelsRequestTimeout(); got != 40*time.Second {
		t.Fatalf("catalog wait with a 40 s default = %v, want 40s", got)
	}
}
