package litmus

import (
	"strings"
	"testing"
	"time"
)

func TestAllSkippedCommentSaysNotTested(t *testing.T) {
	checks := []Check{
		{ID: "c1", Name: "nothing-to-click", Expect: "the feature can be checked", State: CheckSkipped, Note: "nothing to click; not tested by Litmus"},
		{ID: "c2", Name: "nothing-to-click", Expect: "the feature can be checked", State: CheckSkipped, Leg: "base", Note: "nothing to click; not tested by Litmus"},
	}
	body := renderComment(commentRun, commentVerdict(checks), evidence{}, commentRun.CreatedAt.Add(time.Minute))
	if !strings.Contains(body, "**Result: could not test this from the app.**") {
		t.Fatalf("all-skipped comment does not say it was not tested:\n%s", body)
	}
	if !strings.Contains(body, "Nothing to click; not tested by Litmus.") {
		t.Fatalf("all-skipped comment lost the reason:\n%s", body)
	}
	for _, unwanted := range []string{"something broke; do not merge yet", "I could not reach any check on the PR build"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("all-skipped comment says %q:\n%s", unwanted, body)
		}
	}
}
