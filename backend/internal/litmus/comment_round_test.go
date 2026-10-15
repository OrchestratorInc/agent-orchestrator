package litmus

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPerBugCommentEligibilityAndDraftFiles(t *testing.T) {
	dir := t.TempDir()
	writeShot := func(name string, c color.RGBA) string {
		t.Helper()
		path := filepath.Join(dir, name)
		img := image.NewRGBA(image.Rect(0, 0, 800, 600))
		for y := 0; y < 600; y++ {
			for x := 0; x < 800; x++ {
				img.SetRGBA(x, y, c)
			}
		}
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}
	basePath := writeShot("base.png", color.RGBA{R: 255, A: 255})
	headPath := writeShot("head.png", color.RGBA{B: 255, A: 255})
	svc := New(NewStore(dir), func() time.Time { return time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC) })
	run := Run{ID: "run_comments", Repo: "o/r", PRNumber: 7, HeadSHA: "abc", CreatedAt: time.Date(2026, 10, 15, 11, 0, 0, 0, time.UTC)}
	if err := svc.store.Save(run); err != nil {
		t.Fatal(err)
	}
	headFail := Check{ID: "menu-head", Name: "menu opens", Expect: "the menu stays open", State: CheckFail, Receipts: []Receipt{{Leg: "head", AfterPath: headPath}}}
	basePass := Check{ID: "menu-base", Name: "menu opens", State: CheckPass, Leg: "base", Receipts: []Receipt{{Leg: "base", AfterPath: basePath}}}
	preExisting := Check{ID: "old-head", Name: "old failure", State: CheckFail}
	preExistingBase := Check{ID: "old-base", Name: "old failure", State: CheckFail, Leg: "base"}
	v := Verdict{Overall: CheckFail, Checks: []Check{headFail, basePass, preExisting, preExistingBase}}
	bugs := failingBugComments(v.Checks)
	if len(bugs) != 1 || bugs[0].Check.Name != "menu opens" {
		t.Fatalf("bug eligibility = %#v", bugs)
	}
	docs, err := svc.draftComments(run, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || !strings.Contains(docs[0].Body, "Bug 1 of 1:") || strings.Count(docs[0].Body, litmusCommentMarker()) != 1 {
		t.Fatalf("draft = %#v", docs)
	}
	if _, err := os.Stat(filepath.Join(svc.store.root, run.ID, "comments", "1.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(svc.store.root, run.ID, "comments", "bug-1.png")); err != nil {
		t.Fatal(err)
	}
}

func TestPerBugNumberSurvivesAFollowingRun(t *testing.T) {
	svc := New(NewStore(t.TempDir()), nil)
	old := Run{ID: "old", Repo: "o/r", PRNumber: 7, Comments: []PostedComment{{Name: "removed check", Number: 1, Fixed: true}, {Name: "menu opens", Number: 2, ID: 22}}}
	if err := svc.store.Save(old); err != nil {
		t.Fatal(err)
	}
	run := Run{ID: "new", Repo: old.Repo, PRNumber: old.PRNumber}
	bugs := []bugComment{{Check: Check{Name: "menu opens"}}, {Check: Check{Name: "new check"}}}
	svc.numberBugComments(run, bugs)
	if bugs[0].Number != 2 || bugs[1].Number != 3 || bugs[0].Total != 3 || bugs[1].Total != 3 {
		t.Fatalf("numbered bugs = %#v", bugs)
	}
}

func TestPerBugMarkerFollowsTheExistingComment(t *testing.T) {
	svc := New(NewStore(t.TempDir()), nil)
	old := Run{ID: "old", Repo: "o/r", PRNumber: 7, Comments: []PostedComment{
		{Name: "A", ID: 101, URL: "https://github.com/o/r/pull/7#issuecomment-101", Number: 1},
		{Name: "B", ID: 102, URL: "https://github.com/o/r/pull/7#issuecomment-102", Number: 2},
	}}
	if err := svc.store.Save(old); err != nil {
		t.Fatal(err)
	}
	run := Run{ID: "new", Repo: old.Repo, PRNumber: old.PRNumber}
	if err := svc.store.Save(run); err != nil {
		t.Fatal(err)
	}
	checks := []Check{
		{ID: "b", Name: "B", State: CheckFail},
		{ID: "bb", Name: "B", Leg: "base", State: CheckPass},
		{ID: "a", Name: "A", State: CheckFail},
		{ID: "aa", Name: "A", Leg: "base", State: CheckPass},
	}
	docs, err := svc.draftComments(run, Verdict{Overall: CheckFail, Checks: checks})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 || strings.Contains(docs[0].Body, litmusCommentMarker()) || !strings.Contains(docs[1].Body, litmusCommentMarker()) {
		t.Fatalf("marker moved with file order: %#v", docs)
	}
}

func TestPerBugPublishReusesIdsAndMarkerByCheckName(t *testing.T) {
	svc := New(NewStore(t.TempDir()), nil)
	old := Run{ID: "old", Repo: "o/r", PRNumber: 7, Comments: []PostedComment{
		{Name: "A", ID: 101, URL: "https://github.com/o/r/pull/7#issuecomment-101", Number: 1},
		{Name: "B", ID: 102, URL: "https://github.com/o/r/pull/7#issuecomment-102", Number: 2},
	}}
	if err := svc.store.Save(old); err != nil {
		t.Fatal(err)
	}
	run := Run{ID: "new", Repo: old.Repo, PRNumber: old.PRNumber, HeadSHA: "new", Comments: []PostedComment{{Name: "B", ID: 102, URL: "https://github.com/o/r/pull/7#issuecomment-102", Number: 2}}}
	if err := svc.store.Save(run); err != nil {
		t.Fatal(err)
	}
	g := &commentGH{t: t, login: "litmus-bot", comments: []fakeComment{
		{ID: 101, Login: "litmus-bot", Body: "Bug 1 of 2\n" + litmusCommentMarker()},
		{ID: 102, Login: "litmus-bot", Body: "Bug 2 of 2"},
	}}
	svc.gh = g
	checks := []Check{
		{ID: "b", Name: "B", State: CheckFail},
		{ID: "bb", Name: "B", Leg: "base", State: CheckPass},
		{ID: "a", Name: "A", State: CheckFail},
		{ID: "aa", Name: "A", Leg: "base", State: CheckPass},
	}
	v := Verdict{Overall: CheckFail, Checks: checks}
	if _, err := svc.draftComments(run, v); err != nil {
		t.Fatal(err)
	}
	_, _, err := svc.publishCommentSet(ctx, run, v, evidence{}, "fallback")
	if err != nil {
		t.Fatal(err)
	}
	if !g.has("api --method PATCH repos/o/r/issues/comments/102") || !g.has("api --method PATCH repos/o/r/issues/comments/101") || g.has("api --method POST repos/o/r/issues/7/comments") {
		t.Fatalf("comment calls = %q", g.calls)
	}
	firstMarker := len(g.sent) > 0 && strings.Contains(g.field(0, "body"), litmusCommentMarker())
	secondMarker := len(g.sent) > 1 && strings.Contains(g.field(1, "body"), litmusCommentMarker())
	if len(g.sent) != 2 || firstMarker || !secondMarker {
		t.Fatalf("marker bodies len=%d first=%t second=%t", len(g.sent), firstMarker, secondMarker)
	}
}

func TestPerBugPassMarksOnlyTheKnownBugFixed(t *testing.T) {
	svc := New(NewStore(t.TempDir()), nil)
	run := Run{ID: "run_comments", Repo: "o/r", PRNumber: 7, HeadSHA: "new", Comments: []PostedComment{{Name: "A", ID: 101, URL: "https://github.com/o/r/pull/7#issuecomment-101", Number: 1}}}
	if err := svc.store.Save(run); err != nil {
		t.Fatal(err)
	}
	g := &commentGH{t: t, login: "litmus-bot", comments: []fakeComment{{ID: 101, Login: "litmus-bot", Body: litmusCommentMarker() + "\nBug 1 of 1"}}}
	svc.gh = g
	v := Verdict{Overall: CheckPass, Checks: []Check{{ID: "a", Name: "A", State: CheckPass}}}
	if _, err := svc.draftComments(run, v); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.publishCommentSet(ctx, run, v, evidence{}, "summary"); err != nil {
		t.Fatal(err)
	}
	fixed := ""
	for i := range g.sent {
		if strings.Contains(g.field(i, "body"), "Fixed at new") {
			fixed = g.field(i, "body")
			break
		}
	}
	if !g.has("api --method PATCH repos/o/r/issues/comments/101") || fixed == "" || !strings.Contains(fixed, litmusCommentMarker()) {
		t.Fatalf("fixed comment calls=%q sent=%q", g.calls, g.sent)
	}
}

func TestPerBugDoesNotFixASkippedOrMissingCheck(t *testing.T) {
	svc := New(NewStore(t.TempDir()), nil)
	run := Run{ID: "run_comments", Repo: "o/r", PRNumber: 7, HeadSHA: "new", Comments: []PostedComment{{Name: "A", ID: 101, URL: "https://github.com/o/r/pull/7#issuecomment-101", Number: 1}}}
	if err := svc.store.Save(run); err != nil {
		t.Fatal(err)
	}
	g := &commentGH{t: t, login: "litmus-bot", comments: []fakeComment{{ID: 101, Login: "litmus-bot", Body: litmusCommentMarker() + "\nold"}}}
	svc.gh = g
	for _, checks := range [][]Check{
		{{ID: "a", Name: "A", State: CheckSkipped}},
		{},
		{{ID: "a", Name: "A", State: CheckPartial}},
	} {
		if _, _, err := svc.publishCommentSet(ctx, run, Verdict{Overall: CheckPartial, Checks: checks}, evidence{}, "summary"); err != nil {
			t.Fatal(err)
		}
	}
	for _, call := range g.calls {
		if strings.Contains(call, "/issues/comments/101") {
			t.Fatalf("a skipped, partial, or missing check was marked fixed: %q", call)
		}
	}
}

func TestPerBugDraftPreservesHandEditedFixedText(t *testing.T) {
	svc := New(NewStore(t.TempDir()), nil)
	run := Run{ID: "run_comments", Repo: "o/r", PRNumber: 7, HeadSHA: "new", Comments: []PostedComment{{Name: "A", ID: 101, URL: "https://github.com/o/r/pull/7#issuecomment-101", Number: 1}}}
	if err := svc.store.Save(run); err != nil {
		t.Fatal(err)
	}
	v := Verdict{Overall: CheckPass, Checks: []Check{{ID: "a", Name: "A", State: CheckPass}}}
	if _, err := svc.draftComments(run, v); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(svc.store.root, run.ID, "comments", "2.md")
	const hand = "I checked this by hand and kept this wording.\n\n" + "<!-- litmus:comment -->"
	if err := os.WriteFile(path, []byte(hand), 0o640); err != nil {
		t.Fatal(err)
	}
	docs, err := svc.commentDrafts(run, v, evidence{}, "summary")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 || docs[1].Body != hand || !docs[1].Fixed || !docs[1].Marker {
		t.Fatalf("fixed hand edit = %#v", docs)
	}
}

func TestPerBugDraftRemovesUnavailableGeneratedLinks(t *testing.T) {
	svc := New(NewStore(t.TempDir()), nil)
	run := Run{ID: "run_comments", Repo: "o/r", PRNumber: 7, HeadSHA: "new"}
	if err := svc.store.Save(run); err != nil {
		t.Fatal(err)
	}
	checks := []Check{{ID: "a", Name: "A", State: CheckFail}, {ID: "base-a", Name: "A", Leg: "base", State: CheckPass}}
	v := Verdict{Overall: CheckFail, Checks: checks}
	if _, err := svc.draftComments(run, v); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(svc.store.root, run.ID, "comments", "1.md")
	hand := "Bug 1 of 1: hand note.\n\n![The base build on the left and the PR build on the right](bug-1.png)\n\n[Watch the check](../clips/a-pr.mp4)"
	if err := os.WriteFile(path, []byte(hand), 0o640); err != nil {
		t.Fatal(err)
	}
	docs, err := svc.commentDrafts(run, v, evidence{}, "fallback")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || strings.Contains(docs[0].Body, "bug-1.png") || strings.Contains(docs[0].Body, "../clips/a-pr.mp4") || !strings.Contains(docs[0].Body, "not available") {
		t.Fatalf("unavailable links = %#v", docs)
	}
}
