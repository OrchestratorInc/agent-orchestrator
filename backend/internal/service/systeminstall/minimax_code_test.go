package systeminstall

import (
	"strings"
	"testing"
)

func TestMiniMaxRequiresQualifiedRelease(t *testing.T) {
	plan := newTestService("linux", "npm").planAgent(TargetMiniMaxCode)
	if !plan.Unsupported || plan.Method != "manual" || !strings.Contains(plan.DocsURL, "v0.6.5") {
		t.Fatalf("MiniMax plan=%#v", plan)
	}
}
