package litmus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

type roundCommentGH struct {
	bodies map[int64]string
	next   int64
	failID int64
	failed bool
	posts  int
}

func (g *roundCommentGH) Run(_ context.Context, args ...string) ([]byte, error) {
	if len(args) > 1 && args[1] == "user" {
		return []byte("bot\n"), nil
	}
	if len(args) == 3 && args[2] == "--paginate" {
		var out []map[string]any
		for id, body := range g.bodies {
			out = append(out, map[string]any{"id": id, "body": body, "user": map[string]string{"login": "bot"}})
		}
		return json.Marshal(out)
	}
	if len(args) != 6 || args[1] != "--method" || args[4] != "--input" {
		return nil, fmt.Errorf("unexpected gh call %v", args)
	}
	var body struct {
		Body string `json:"body"`
	}
	data, err := os.ReadFile(args[5])
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, err
	}
	var id int64
	if args[2] == "PATCH" {
		id, err = strconv.ParseInt(args[3][strings.LastIndex(args[3], "/")+1:], 10, 64)
		if err != nil {
			return nil, err
		}
		if id == g.failID && !g.failed {
			g.failed = true
			return nil, errors.New("posting failed once")
		}
	} else {
		id = g.next
		g.next++
		g.posts++
	}
	g.bodies[id] = body.Body
	return json.Marshal(map[string]any{"id": id, "html_url": fmt.Sprintf("https://github.com/o/r/pull/7#issuecomment-%d", id)})
}

func roundMappingRun(t *testing.T, svc *Service, id string, minute int) Run {
	t.Helper()
	run := Run{ID: id, Repo: "o/r", PRNumber: 7, HeadSHA: id, CreatedAt: time.Date(2026, 10, 15, 12, minute, 0, 0, time.UTC)}
	if err := svc.store.Save(run); err != nil {
		t.Fatal(err)
	}
	return run
}

func roundBugChecks(names ...string) []Check {
	var out []Check
	for i, name := range names {
		out = append(out, Check{ID: fmt.Sprintf("h%d", i), Name: name, State: CheckFail}, Check{ID: fmt.Sprintf("b%d", i), Name: name, State: CheckPass, Leg: "base"})
	}
	return out
}

func TestPerBugRetryRetainsAllCommentIds(t *testing.T) {
	svc := New(NewStore(t.TempDir()), nil)
	old := roundMappingRun(t, svc, "old", 0)
	old.Comments = []PostedComment{{Name: "A", ID: 101, Number: 1}, {Name: "B", ID: 102, Number: 2}}
	if err := svc.store.Save(old); err != nil {
		t.Fatal(err)
	}
	run := roundMappingRun(t, svc, "retry", 1)
	g := &roundCommentGH{bodies: map[int64]string{101: "old A\n" + litmusCommentMarker(), 102: "old B"}, next: 900, failID: 102}
	svc.gh = g
	v := Verdict{Overall: CheckFail, Checks: roundBugChecks("A", "B")}
	if _, err := svc.draftComments(run, v); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.publishCommentSet(ctx, run, v, evidence{}, ""); err == nil {
		t.Fatal("expected the second update to fail")
	}
	saved, err := svc.store.Get(run.ID)
	if err != nil || len(saved.Comments) != 2 {
		t.Fatalf("retry lost history: %+v, %v", saved.Comments, err)
	}
	if _, _, err := svc.publishCommentSet(ctx, saved, v, evidence{}, ""); err != nil {
		t.Fatal(err)
	}
	if g.posts != 0 || !strings.Contains(g.bodies[101], "Bug 1 of 2") || !strings.Contains(g.bodies[102], "Bug 2 of 2") {
		t.Fatalf("retry created a duplicate or lost a bug: posts=%d bodies=%v", g.posts, g.bodies)
	}
}

func TestPerBugFixedHistorySurvivesPassAndRecurrence(t *testing.T) {
	svc := New(NewStore(t.TempDir()), nil)
	old := roundMappingRun(t, svc, "old", 0)
	old.Comments = []PostedComment{{Name: "A", ID: 101, Number: 1}}
	if err := svc.store.Save(old); err != nil {
		t.Fatal(err)
	}
	g := &roundCommentGH{bodies: map[int64]string{101: "old A\n" + litmusCommentMarker()}, next: 900}
	svc.gh = g
	pass := Verdict{Overall: CheckPass, Checks: []Check{{ID: "h", Name: "A", State: CheckPass}}}
	for i, id := range []string{"fixed", "still-fixed"} {
		run := roundMappingRun(t, svc, id, i+1)
		if _, err := svc.draftComments(run, pass); err != nil {
			t.Fatal(err)
		}
		if _, _, err := svc.publishCommentSet(ctx, run, pass, evidence{}, ""); err != nil {
			t.Fatal(err)
		}
		stored, err := svc.store.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		a, found := postedCommentFor(stored.Comments, "A")
		if !found || a.ID != 101 || !a.Fixed {
			t.Fatalf("fixed history disappeared: %+v", stored.Comments)
		}
	}
	run := roundMappingRun(t, svc, "recurred", 3)
	v := Verdict{Overall: CheckFail, Checks: roundBugChecks("A")}
	if _, err := svc.draftComments(run, v); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.publishCommentSet(ctx, run, v, evidence{}, ""); err != nil {
		t.Fatal(err)
	}
	if g.posts != 1 || !strings.HasPrefix(g.bodies[101], "Bug 1 of 1:") {
		t.Fatalf("recurrence created a new bug comment: posts=%d body=%q", g.posts, g.bodies[101])
	}
	markers := 0
	for _, body := range g.bodies {
		markers += strings.Count(body, litmusCommentMarker())
	}
	if markers != 1 {
		t.Fatalf("marker count %d in %v", markers, g.bodies)
	}
}

func TestPostKeepsCommentOff(t *testing.T) {
	svc := New(NewStore(t.TempDir()), nil)
	run := roundMappingRun(t, svc, "off", 0)
	run.State, run.Comment = RunAwaitingApproval, CommentOff
	if err := svc.store.Save(run); err != nil {
		t.Fatal(err)
	}
	g := &roundCommentGH{bodies: map[int64]string{}, next: 900}
	svc.gh = g
	is(t, "TESTING_COMMENT_OFF")(svc.Post(ctx, run.ID))
	if g.posts != 0 || len(g.bodies) != 0 {
		t.Fatal("a no-comment run posted a comment")
	}
}
