package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestManagedChatHostAcceptsFencedRawProtocol(t *testing.T) {
	cmd := newChatHostCommand()
	err := cmd.RunE(cmd, []string{"synthetic-session", t.TempDir(), "relative-invalid-workspace", "managed-raw", strings.Repeat("a", 64), "--", "no-provider-launch"})
	var usage usageError
	if err == nil || errors.As(err, &usage) || !strings.Contains(err.Error(), "absolute workdir") {
		t.Fatal("managed raw host did not reach config validation", err)
	}
}
