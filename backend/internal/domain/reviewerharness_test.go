package domain

import "testing"

func TestOpenCodeV2IsNotAReviewerHarness(t *testing.T) {
	if ReviewerHarness("opencode-v2").IsKnown() {
		t.Fatal("OpenCode 2 is a worker-only harness")
	}
	if !ReviewerOpenCode.IsKnown() {
		t.Fatal("OpenCode 1 reviewer support was removed")
	}
}
