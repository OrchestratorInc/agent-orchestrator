package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInstalledCleanupCommandsAreReadAndBounded(t *testing.T) {
	t.Parallel()
	if commands, ok := InstalledCleanupCommands(json.RawMessage(`{"installedCleanup":["make down"," ","echo ok"],"providerFailureId":"x"}`)); !ok ||
		strings.Join(commands, "|") != "make down|echo ok" {
		t.Fatalf("commands = %q (%v)", commands, ok)
	}
	for _, raw := range []string{``, `{}`, `{"installedCleanup":"make down"}`, `not json`} {
		if _, ok := InstalledCleanupCommands(json.RawMessage(raw)); ok {
			t.Fatalf("%q read as stored commands", raw)
		}
	}
	long := make([]string, 40)
	for index := range long {
		long[index] = strings.Repeat("x", 1000)
	}
	bounded := BoundInstalledCleanup(long)
	encoded, _ := json.Marshal(bounded)
	if len(bounded) == 0 || len(bounded) >= len(long) || len(encoded) > InstalledCleanupLimit {
		t.Fatalf("bounded to %d commands, %d bytes", len(bounded), len(encoded))
	}
	if short := []string{"a", "b"}; strings.Join(BoundInstalledCleanup(short), "|") != "a|b" {
		t.Fatal("a short Cleanup was cut")
	}
}
