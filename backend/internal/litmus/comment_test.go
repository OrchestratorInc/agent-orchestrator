package litmus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCoverageWithoutReceiptsRendersNoReceipt(t *testing.T) {
	run := Run{Kind: "issue", ID: "run_coverage", IssueNumber: 7}
	v := Verdict{Result: "blocked", Coverage: []Coverage{
		{Text: "Trigger could not run.", Leg: "main", Trigger: true},
		{Text: "No observation was captured.", Leg: "main"},
	}}
	body := renderVerdict(run, v, nil, t.TempDir(), "", evidence{})
	for _, want := range []string{"Trigger: main, no receipt\n", "\nmain, no receipt\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("coverage omits %q: %s", want, body)
		}
	}
	if strings.Contains(body, "main, \n") {
		t.Fatalf("coverage has a dangling comma: %s", body)
	}
}

type fakeComment struct {
	ID          int64
	Login, Body string
}

// commentGH serves the comment endpoints from memory, records every argv, and reads each --input file when it is
// used. The listing comes back one JSON array per comment, as `gh --paginate` prints pages.
type commentGH struct {
	t             *testing.T
	login, author string
	authToken     string
	authCalls     int
	private       string // "true" or "false": the repo's visibility
	comments      []fakeComment
	fail          string // an argv containing this fails
	failComment   int    // fail this comment write, counting from one
	notFound      int    // the next this many reads of an uploaded video answer 404
	calls, sent   []string
	argv          [][]string // every call's argv as gh got it
	uploads       int
}

func (g *commentGH) AuthToken(context.Context) string {
	g.authCalls++
	return g.authToken
}

const upload = "api --method POST -H Content-Type: video/mp4 https://uploads.github.com/"

func (g *commentGH) Run(ctx context.Context, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil { // like gh, which is killed with its context
		return nil, err
	}
	line := strings.Join(args, " ")
	g.calls = append(g.calls, line)
	g.argv = append(g.argv, args)
	var in struct{ Body string }
	if i := slices.Index(args, "--input"); i >= 0 && !strings.HasPrefix(line, upload) { // a video upload sends a file, not a JSON body
		raw, err := os.ReadFile(args[i+1])
		if err != nil {
			g.t.Fatal(err)
		}
		g.sent = append(g.sent, string(raw))
		_ = json.Unmarshal(raw, &in)
	}
	if g.fail != "" && strings.Contains(line, g.fail) || g.failComment > 0 && in.Body != "" && len(g.sent) == g.failComment {
		return nil, errors.New("HTTP 403: Resource not accessible\nsecond line")
	}
	switch {
	case line == "api user --jq .login":
		return []byte(g.login + "\n"), nil
	case strings.HasPrefix(line, "repo view "):
		return []byte(g.private + "\n"), nil
	case line == "api repos/o/r --jq .id":
		return []byte("1234\n"), nil
	case strings.HasPrefix(line, upload):
		g.uploads++
		return []byte(fmt.Sprintf(`{"url":"https://github.com/user-attachments/assets/%08d-aaaa-4bbb-8ccc-dddddddddddd"}`, g.uploads)), nil
	case strings.HasPrefix(line, "api -i https://github.com/user-attachments/assets/"):
		if g.notFound > 0 {
			g.notFound--
			return nil, errors.New("gh: Not Found (HTTP 404)")
		}
		return []byte("HTTP/2.0 200 OK\r\nContent-Length: 3\r\nContent-Type: video/mp4\r\n\r\nmp4"), nil
	case strings.HasPrefix(line, "pr view ") && strings.Contains(line, " --json author "):
		return []byte(g.author + "\n"), nil
	case strings.HasSuffix(line, "/comments --paginate"):
		out := "[]"
		if len(g.comments) > 0 {
			out = ""
		}
		for _, c := range g.comments {
			page, _ := json.Marshal([]map[string]any{{"id": c.ID, "body": c.Body, "user": map[string]string{"login": c.Login}}})
			out += string(page)
		}
		return []byte(out), nil
	case strings.HasPrefix(line, "api --method POST ") && strings.Contains(line, "/issues/") && strings.Contains(line, "/comments --input "):
		id := int64(900 + len(g.comments))
		g.comments = append(g.comments, fakeComment{id, g.login, in.Body})
		return []byte(fmt.Sprintf(`{"id":%d,"html_url":"https://github.com/o/r/pull/7#issuecomment-%d"}`, id, id)), nil
	case strings.HasPrefix(line, "api --method PATCH ") && strings.Contains(line, "/issues/comments/"):
		_, rest, _ := strings.Cut(line, "/issues/comments/")
		id := strings.Fields(rest)[0]
		for i := range g.comments {
			if strconv.FormatInt(g.comments[i].ID, 10) == id {
				g.comments[i].Body = in.Body
			}
		}
		return []byte(`{"html_url":"https://github.com/o/r/pull/7#issuecomment-` + id + `"}`), nil

	}
	g.t.Fatalf("unexpected gh call %q", line)
	return nil, nil
}

func (g *commentGH) has(prefix string) bool {
	return slices.ContainsFunc(g.calls, func(c string) bool { return strings.HasPrefix(c, prefix) })
}

func (g *commentGH) field(i int, name string) string {
	var m map[string]string
	_ = json.Unmarshal([]byte(g.sent[i]), &m)
	return m[name]
}

var commentRun = Run{ID: "run_s1", SessionID: "s1", Repo: "o/r", PRNumber: 7, BaseSHA: "0ca80cfc370b21443c4a2b9d22199c047b8c74e7", HeadSHA: "e2204e0f361867a28310e71421a4227ab1d09544",
	State: RunSubmitted, CreatedAt: time.Date(2026, 10, 11, 8, 56, 0, 0, time.UTC), Comment: CommentPost}

func TestSubmitReportsPutTriggerCoverageFirst(t *testing.T) {
	for _, tc := range []struct {
		name, kind, result, leg string
		trigger                 bool
	}{
		{"issue with trigger", "issue", "not_reproduced", "main", true},
		{"issue without trigger", "issue", "reproduced", "main", false},
		{"PR with trigger", "pr", "pass", "head", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var svc *Service
			var dir, artifacts string
			if tc.kind == "issue" {
				svc, dir, artifacts = issueSubmitSvc(t)
			} else {
				svc, _, dir, artifacts = submitSvc(t)
			}
			v := Verdict{Result: tc.result, Headline: "The tested step held.", Coverage: []Coverage{
				{Text: "First ordinary step.", Leg: tc.leg, Receipts: []int{1}},
				{Text: "The *reported* step.\nStill the tester's words.", Leg: tc.leg, Receipts: []int{2}, Trigger: tc.trigger},
				{Text: "Last ordinary step.", Leg: tc.leg, Receipts: []int{1}},
			}}
			if tc.result == "reproduced" {
				v.Findings = []Finding{{Leg: "main", Severity: "bug", Receipts: []int{1}, Clip: &FindingClip{From: 1, To: 1}}}
			}
			path := verdictInput(t, svc, v)
			original, err := os.ReadFile(filepath.Join(dir, path))
			is(t, "")(nil, err)
			got, err := svc.Submit(ctx, "s1", path)
			is(t, "")(nil, err)
			if got.Coverage[0].Text != v.Coverage[0].Text || got.Coverage[1].Text != v.Coverage[1].Text || got.Coverage[1].Trigger != tc.trigger {
				t.Fatalf("coverage changed: %+v", got.Coverage)
			}
			saved, err := os.ReadFile(filepath.Join(dir, verdictFile))
			is(t, "")(nil, err)
			if string(saved) != string(original) {
				t.Fatal("rendering changed tester JSON")
			}
			legName := "PR"
			if tc.leg == "main" {
				legName = "main"
			}
			entry := func(text string, id int, trigger bool) string {
				prefix := ""
				if trigger {
					prefix = "Trigger: "
				}
				// The issue fixture relabels head receipts as main without renaming their image files.
				return fmt.Sprintf("%s%s, [%s receipt %d](receipts/head-%d-after.png)\n\n%s\n\n", prefix, legName, tc.leg, id, id, text)
			}
			first := entry("First ordinary step.", 1, false)
			step := entry("The \\*reported\\* step.\nStill the tester's words.", 2, tc.trigger)
			last := entry("Last ordinary step.", 1, false)
			want := "## Coverage\n\n" + first + step + last + "## Environment"
			if tc.trigger {
				want = "## Coverage\n\n" + step + first + last + "## Environment"
			}
			for _, root := range []string{dir, artifacts} {
				body, err := os.ReadFile(filepath.Join(root, "verdict.md"))
				is(t, "")(nil, err)
				if !strings.Contains(string(body), want) {
					t.Fatalf("coverage text, order or links changed in %s:\n%s", filepath.Join(root, "verdict.md"), body)
				}
				if !tc.trigger && strings.Contains(string(body), "Trigger:") {
					t.Fatalf("unmarked coverage labeled as trigger in %s", filepath.Join(root, "verdict.md"))
				}
				comment, err := os.ReadFile(filepath.Join(root, "comment.md"))
				is(t, "")(nil, err)
				if strings.Contains(string(comment), "## Coverage") || strings.Contains(string(comment), "receipt") {
					t.Fatalf("full coverage leaked into comment: %s", comment)
				}
				if !strings.Contains(string(comment), "\nThe tested step held.\n") || strings.Contains(string(comment), "The \\*reported\\* step.\nStill the tester's words.") {
					t.Fatalf("summary lost its headline or included trigger coverage: %s", comment)
				}
			}
		})
	}
}

func TestSubmitPrintsMeasuredClipLengthInReports(t *testing.T) {
	svc, _, dir, artifacts := submitSvc(t)
	svc.runner = &videoProbeRunner{duration: "24"}
	_, err := svc.update("run_s1", func(r *Run) {
		r.Legs[0].RecordStartedAt = r.CreatedAt
		r.Legs[0].RecordStoppedAt = r.CreatedAt.Add(24 * time.Second)
	})
	is(t, "")(nil, err)
	writeFile(t, recordingPath(dir, "head"), "recording")
	v := passVerdict()
	v.Result = "fail"
	v.Findings = []Finding{{Title: "Menu", Leg: "head", Severity: "bug", Receipts: []int{1}, Clip: &FindingClip{From: 1, To: 1}}}
	_, err = svc.Submit(ctx, "s1", verdictInput(t, svc, v))
	is(t, "")(nil, err)
	for _, root := range []string{dir, artifacts} {
		for _, name := range []string{"verdict.md", "comment.md"} {
			body, err := os.ReadFile(filepath.Join(root, name))
			is(t, "")(nil, err)
			if !strings.Contains(string(body), "[Clip, 24 s](clips/finding-1.mp4)") {
				t.Fatalf("clip length missing from %s: %s", filepath.Join(root, name), body)
			}
		}
	}
	body, err := os.ReadFile(filepath.Join(artifacts, "index.md"))
	is(t, "")(nil, err)
	if !strings.Contains(string(body), "[finding-1.mp4, 24 s](clips/finding-1.mp4)") {
		t.Fatalf("clip length missing from artifact index: %s", body)
	}
}

func TestPublishCommentHandlesClipLengthAndPreservesEdits(t *testing.T) {
	for _, tc := range []struct {
		name, fail string
		legacy     bool
		upload     bool
	}{
		{name: "published", upload: true},
		{name: "upload failed", fail: upload, upload: true},
		{name: "old draft upload failed", fail: upload, legacy: true, upload: true},
		{name: "evidence off"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, dir, _ := submitSvc(t)
			svc.runner = &videoProbeRunner{duration: "24"}
			g := &commentGH{t: t, login: "bot", fail: tc.fail}
			svc.gh = g
			run, err := svc.update("run_s1", func(r *Run) {
				r.Repo, r.RepoID, r.RepoVisibility = "o/r", 1234, "public"
				r.Evidence = EvidencePost
			})
			is(t, "")(nil, err)
			writeFile(t, findingClipPath(dir, 0), "clip")
			v := passVerdict()
			v.Findings = []Finding{{Title: "Menu", Leg: "head", Severity: "bug", Receipts: []int{1}, Clip: &FindingClip{From: 1, To: 1}}}
			re := svc.credentialPattern(ctx)
			body, _, err := svc.composeComment(run, v, re)
			is(t, "")(nil, err)
			if tc.legacy {
				body = strings.ReplaceAll(body, "[Clip, 24 s]", "[Clip]")
			}
			body += "\nOwner edit.\n"
			writeFile(t, filepath.Join(dir, "comment.md"), body)
			git := &evGit{t: t}
			svc.runner = git
			_, state, _ := svc.publishComment(ctx, run, v, body, re, tc.upload)
			if !tc.upload && (g.uploads != 0 || len(git.calls) != 0) {
				t.Fatalf("evidence-off close uploaded: gh %v git %v", g.calls, git.calls)
			}
			if tc.fail != "" {
				if !strings.HasPrefix(state, "failed:") || !strings.Contains(state, "HTTP 403") || len(g.sent) != 0 || readRunFile(t, svc, run, "comment.md") != body {
					t.Fatalf("upload failure posted or changed the draft: state %s, sent %v", state, g.sent)
				}
				return
			}
			if state != "posted" || len(g.sent) != 2 {
				t.Fatalf("comment state %s, sent %v", state, g.sent)
			}
			got := g.field(1, "body")
			want := "[Clip, 24 s](https://github.com/user-attachments/assets/00000001-aaaa-4bbb-8ccc-dddddddddddd)"
			if tc.upload && !strings.Contains(got, want) || !tc.upload && strings.Contains(got, "[Clip") || !strings.Contains(got, "\nOwner edit.\n") || strings.Contains(got, "](clips/") || strings.Contains(got, "not published") {
				t.Fatalf("clip publication or owner edit changed: %s", got)
			}
		})
	}
}

const findingCommentSample = `**Menu stays open in dark theme (PR)**

Open the menu and choose an item. The menu stays open. It should close after selection.

![Menu stays open in dark theme](evidence/finding-1.png)

[Clip](clips/finding-1.mp4)

<!-- litmus:pr:run_s1:finding-1 -->
`

const passCommentSample = `Litmus: pass at e2204e0
The menu opens.

![head recording](evidence/head.gif)

<!-- litmus:pr:run_s1:summary -->
`

func TestFindingCommentsHaveOneBodyPerFinding(t *testing.T) {
	for _, tc := range []struct{ kind, result, leg string }{{"pr", "fail", "head"}, {"pr", "partial", "head"}, {"issue", "reproduced", "main"}, {"issue", "partial", "main"}} {
		t.Run(tc.kind+"/"+tc.result, func(t *testing.T) {
			run, dir := commentRun, t.TempDir()
			run.Kind, run.TargetSHA = tc.kind, run.BaseSHA
			writeFile(t, findingClipPath(dir, 0), "clip")
			v := passVerdict()
			v.Result = tc.result
			v.BlockedOn = "Signed-in recovery flow."
			oldLeg := "base"
			if tc.kind == "issue" {
				oldLeg = "main"
			}
			v.Findings = []Finding{
				{Title: "Menu stays open in dark theme", Leg: tc.leg, Severity: "bug", Receipts: []int{1}, Clip: &FindingClip{From: 1, To: 1}, Text: "Open the menu and choose an item. The menu stays open. It should close after selection."},
				{Title: "T3", Leg: oldLeg, Severity: "worth-a-look", Clip: &FindingClip{From: 1, To: 1}, Text: "Open the old menu. Its label is clipped. The full label should be visible."},
				{Title: "Sidebar", Leg: tc.leg, Severity: "worth-a-look", Clip: &FindingClip{From: 1, To: 1}, Text: "The sidebar label is clipped."},
			}
			ev := evidence{items: []evItem{{file: "head.gif", kind: kindGIF}}}
			for i := range v.Findings {
				ev.items = append(ev.items, evItem{file: findingID(i) + ".png", finding: findingID(i), kind: kindFinding})
			}
			body := renderComments(run, v, dir, ev, func(s string) string { return s })
			blocks, err := splitComments(run, v, body)
			indices, summary := []int{0, 2}, "2 findings below; also seen before this PR: T3."
			if tc.kind == "issue" {
				indices, summary = []int{0, 1, 2}, "3 findings below"
			}
			if err != nil || len(blocks) != len(indices)+1 {
				t.Fatalf("blocks %+v err %v", blocks, err)
			}
			sha := "e2204e0"
			if tc.kind == "issue" {
				sha = "0ca80cf"
			}
			wantSummary := fmt.Sprintf("Litmus: %s at %s\nThe menu opens.\nNot tested: Signed-in recovery flow.\n%s\n\n%s\n", tc.result, sha, summary, commentMarker(run, "summary"))
			if blocks[0].body != wantSummary || strings.Count(strings.TrimSpace(strings.Split(blocks[0].body, "\n\n")[0]), "\n") != 3 {
				t.Fatalf("summary must have four lines with sha7: %s", blocks[0].body)
			}
			if tc.kind == "pr" && blocks[1].body != findingCommentSample {
				t.Fatalf("finding sample changed:\n%s", blocks[1].body)
			}
			if tc.kind == "pr" && (strings.Contains(body, "Open the old menu.") || strings.Contains(body, commentMarker(run, "finding-2"))) {
				t.Fatalf("base finding has a body: %s", body)
			}
			for j, block := range blocks[1:] {
				i := indices[j]
				if !strings.HasSuffix(block.body, commentMarker(run, findingID(i))+"\n") || !strings.Contains(block.body, "evidence/"+findingID(i)+".png") {
					t.Fatalf("finding media or marker missing: %s", block.body)
				}
			}
			for _, unwanted := range []string{"receipt", "Evidence:", "```", "Environment", "Coverage", ".gif", "worth-a-look", "\nbug,"} {
				if strings.Contains(body, unwanted) {
					t.Fatalf("comment contains %q: %s", unwanted, body)
				}
			}
		})
	}
}

func TestSummaryCommentUsesHeadlineAndRequiredLegGIF(t *testing.T) {
	for _, tc := range []struct {
		kind, result, word, leg string
		trigger                 bool
	}{{"pr", "pass", "pass", "head", true}, {"pr", "partial", "partial", "head", false}, {"issue", "not_reproduced", "not reproduced", "main", true}, {"issue", "partial", "partial", "main", false}} {
		t.Run(tc.kind+"/"+tc.result, func(t *testing.T) {
			run, v := commentRun, passVerdict()
			run.Kind, v.Result, v.Coverage[0].Leg = tc.kind, tc.result, tc.leg
			run.TargetSHA = run.BaseSHA
			v.Coverage[0].Trigger = tc.trigger
			v.Coverage = append(v.Coverage, Coverage{Leg: "base", Trigger: true, Text: "Wrong leg."}, Coverage{Leg: tc.leg, Text: "Ordinary coverage."})
			ev := evidence{items: []evItem{{file: "base.gif", kind: kindGIF}, {file: tc.leg + ".gif", alt: tc.leg + " recording", kind: kindGIF}}}
			body := renderComments(run, v, t.TempDir(), ev, func(s string) string { return s })
			blocks, err := splitComments(run, v, body)
			sha := "e2204e0"
			if tc.kind == "issue" {
				sha = "0ca80cf"
			}
			if err != nil || len(blocks) != 1 || !strings.HasPrefix(body, fmt.Sprintf("Litmus: %s at %s\nThe menu opens.\n", mdText(tc.word), sha)) || !strings.Contains(body, "](evidence/"+tc.leg+".gif)") || !strings.HasSuffix(body, commentMarker(run, "summary")+"\n") || strings.Count(body, "![") != 1 {
				t.Fatalf("summary %s err %v", body, err)
			}
			if tc.result == "pass" && body != passCommentSample {
				t.Fatalf("pass sample changed:\n%s", body)
			}
			for _, unwanted := range []string{"Opened the menu.", "Wrong leg", "Ordinary coverage", "base.gif", "Environment", "receipt", "Not tested:", "findings below"} {
				if strings.Contains(body, unwanted) {
					t.Fatalf("summary contains %q: %s", unwanted, body)
				}
			}
		})
	}
	if body := renderComments(commentRun, Verdict{Result: "blocked"}, t.TempDir(), evidence{}, func(s string) string { return s }); body != "" {
		t.Fatalf("blocked run has a post body: %s", body)
	}
}

func TestSummaryCommentUsesBlockedOnOrFirstGap(t *testing.T) {
	for _, tc := range []struct {
		name, blocked string
		gaps          []string
		want          string
	}{
		{name: "nothing untested"},
		{name: "one gap", gaps: []string{"Signed-in recovery flow."}, want: "Not tested: Signed-in recovery flow.\n"},
		{name: "several gaps", gaps: []string{"Signed-in recovery flow.", "Another account.", "Base build."}, want: "Not tested: Signed-in recovery flow and 2 more.\n"},
		{name: "blocked_on wins", blocked: "Missing *test* account.", gaps: []string{"Other gap.", "Last gap."}, want: "Not tested: Missing \\*test\\* account.\n"},
		{name: "whitespace blocked_on falls back", blocked: " \t\r\n\u2003 ", gaps: []string{"Signed-in recovery flow.", "Another account.", "Base build."}, want: "Not tested: Signed-in recovery flow and 2 more.\n"},
		{name: "multiline blocked_on", blocked: " One.\nTwo.\r\nThree.\tFour.\u2003Five. ", want: "Not tested: One. Two. Three. Four. Five.\n"},
		{name: "multiline gap", gaps: []string{" One.\nTwo.\r\nThree.\tFour.\u2003Five. "}, want: "Not tested: One. Two. Three. Four. Five.\n"},
		{name: "long blocked_on", blocked: strings.Repeat("x", 600), want: "Not tested: " + strings.Repeat("x", 187) + "…\n"},
		{name: "long gap keeps count", gaps: []string{strings.Repeat("x", 600), "Another account.", "Base build."}, want: "Not tested: " + strings.Repeat("x", 176) + "… and 2 more.\n"},
		{name: "run_ao-46 sentence cut", blocked: "No blocker for the exercised Chat paths. Terminal UI report delivery remains unverified: the terminal opened, but attempted typing was not visible. " + strings.Repeat("more ", 120), want: "Not tested: No blocker for the exercised Chat paths. Terminal UI report delivery remains unverified: the terminal opened, but attempted typing was not visible…\n"},
		{name: "word cut after ellipsis", blocked: "foo… " + strings.Repeat("barbaz", 40), want: "Not tested: foo…\n"},
		{name: "uncut ellipsis", blocked: "Terminal report remains unverified…", want: "Not tested: Terminal report remains unverified….\n"},
		{name: "clean blocked_on", blocked: "Missing private-token account.", want: "Not tested: Missing \\[...\\] account.\n"},
		{name: "clean gap", gaps: []string{"Missing private-token account."}, want: "Not tested: Missing \\[...\\] account.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := commentRun
			run.Gaps = tc.gaps
			v := Verdict{Result: "partial", Headline: "The *fix* held for private-token.", BlockedOn: tc.blocked, Findings: []Finding{{Title: "Menu label", Leg: "head", Text: "The menu is clipped."}}}
			body := renderComments(run, v, t.TempDir(), evidence{}, func(s string) string { return strings.ReplaceAll(s, "private-token", "[...]") })
			blocks, err := splitComments(run, v, body)
			if err != nil || len(blocks) != 2 {
				t.Fatalf("blocks %+v err %v", blocks, err)
			}
			want := "Litmus: partial at e2204e0\nThe \\*fix\\* held for \\[...\\].\n" + tc.want + "1 finding below\n\n" + commentMarker(run, "summary") + "\n"
			if blocks[0].body != want {
				t.Fatalf("summary got:\n%s\nwant:\n%s", blocks[0].body, want)
			}
		})
	}
}

func TestSubmitBoundsSummaryAndPreservesFullHeadline(t *testing.T) {
	for _, tc := range []struct{ name, headline, summary string }{
		{"five-line headline", "One.\nTwo.\nThree.\nFour.\nFive.", "One. Two. Three. Four. Five."},
		{"600-character headline", strings.Repeat("x", 600), strings.Repeat("x", 200) + "…"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, dir, artifacts := submitSvc(t)
			v := passVerdict()
			v.Result, v.Headline, v.BlockedOn = "partial", tc.headline, "An account is missing."
			v.Findings = []Finding{{Title: "Old bug", Leg: "base", Severity: "worth-a-look", Text: "The old menu is clipped."}}
			got, err := svc.Submit(ctx, "s1", verdictInput(t, svc, v))
			is(t, "")(nil, err)
			if got.Headline != tc.headline {
				t.Fatalf("Submit changed the full headline: %q", got.Headline)
			}
			for _, root := range []string{dir, artifacts} {
				comment, err := os.ReadFile(filepath.Join(root, "comment.md"))
				is(t, "")(nil, err)
				summary, _, ok := strings.Cut(string(comment), "\n\n")
				lines := strings.Split(summary, "\n")
				if !ok || len(lines) != 4 || lines[1] != tc.summary {
					t.Fatalf("summary must have four bounded lines in %s: %q", root, summary)
				}
				for _, line := range lines {
					if len([]rune(line)) > 201 {
						t.Fatalf("summary line exceeds 200 characters plus ellipsis: %q", line)
					}
				}
				report, err := os.ReadFile(filepath.Join(root, "verdict.md"))
				is(t, "")(nil, err)
				if !strings.HasPrefix(string(report), "# Verdict: partial\n\n"+mdText(tc.headline)+"\n") {
					t.Fatalf("verdict.md lost the full headline in %s: %s", root, report)
				}
			}
		})
	}
}

func TestSummaryCommentTruncatesAtSentenceOrWordBoundary(t *testing.T) {
	for _, tc := range []struct{ name, headline, want string }{
		{"last period", "First sentence. Last sentence. " + strings.Repeat("more ", 120), "First sentence. Last sentence…"},
		{"last exclamation", "First sentence. Last sentence! " + strings.Repeat("more ", 120), "First sentence. Last sentence…"},
		{"last question", "First sentence. Last sentence? " + strings.Repeat("more ", 120), "First sentence. Last sentence…"},
		{"sentence cut after two periods", "Done.. " + strings.Repeat("more ", 120), "Done…"},
		{"sentence cut after three periods", "Wait... " + strings.Repeat("more ", 60), "Wait…"},
		{"hard cut after period", strings.Repeat("x", 199) + ".more", strings.Repeat("x", 199) + "…"},
		{"last word", strings.Repeat("word ", 120), strings.TrimSpace(strings.Repeat("word ", 40)) + "…"},
		{"Unicode characters", strings.Repeat("界", 600), strings.Repeat("界", 200) + "…"},
		{"readable characters count toward bound", strings.Repeat("*word* ", 120), strings.TrimSpace(strings.Repeat("\\*word\\* ", 28)) + "…"},
		{"entity at cut", strings.Repeat("x", 198) + "&zzz", strings.Repeat("x", 198) + "&amp;z…"},
		{"escape at cut", strings.Repeat("x", 199) + "*more", strings.Repeat("x", 199) + "\\*…"},
		{"exactly 200 characters", strings.Repeat("x", 200), strings.Repeat("x", 200)},
		{"sentence ends at 200", strings.Repeat("x", 199) + ". More words.", strings.Repeat("x", 199) + "…"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run, v := commentRun, passVerdict()
			v.Headline = tc.headline
			body := renderComments(run, v, t.TempDir(), evidence{}, func(s string) string { return s })
			want := "Litmus: pass at e2204e0\n" + tc.want + "\n\n" + commentMarker(run, "summary") + "\n"
			if body != want {
				t.Fatalf("summary got:\n%s\nwant:\n%s", body, want)
			}
		})
	}
}

func TestCommentMediaLinksUsePathsAfterTitleRedaction(t *testing.T) {
	svc, _ := newSvc(t)
	re := svc.credentialPattern(ctx)
	v := Verdict{Result: "fail", Findings: []Finding{{Title: "Menu ghp_fakeToken123", Leg: "head", Clip: &FindingClip{From: 1, To: 1}, Text: "Open the menu. It stays open. It should close."}}}
	ev := evidence{items: []evItem{{file: "finding-1.png", finding: "finding-1", kind: kindFinding}}}
	body := renderComments(commentRun, v, t.TempDir(), ev, func(s string) string { s, _ = redact(s, re); return s })
	if !strings.Contains(body, `Menu \[...\]`) || strings.Contains(body, "fakeToken123") {
		t.Fatal(body)
	}
	const url = "https://example.com/finding.png"
	for _, target := range []string{"evidence/finding-1.png", "./evidence/finding-1.png", "evidence/./finding-1.png", "evidence/unused/../finding-1.png", "%65vidence%2Ffinding-1.png", "&#101;vidence/finding-1.png", "<./evidence/finding-1.png>"} {
		t.Run(target, func(t *testing.T) {
			draft := strings.ReplaceAll(body, "evidence/finding-1.png", target)
			got := rewriteCommentLink(draft, "evidence/finding-1.png", url)
			if !strings.Contains(got, "]("+url+")") || strings.Contains(got, target) {
				t.Fatal(got)
			}
			if got := rewriteCommentLink(draft, "evidence/finding-1.png", ""); strings.Contains(got, "![") || strings.Contains(got, "not published") {
				t.Fatal(got)
			}
		})
	}
}

func TestPRBaseOnlyFindingStaysInSummaryWithGIF(t *testing.T) {
	run, v := commentRun, passVerdict()
	run.Gaps = []string{"Account flow."}
	v.Findings = []Finding{{Title: "T3", Leg: "base", Text: "The old menu is clipped."}}
	ev := evidence{items: []evItem{{file: "head.gif", alt: "head recording", kind: kindGIF}, {file: "base.gif", kind: kindGIF}}}
	body := renderComments(run, v, t.TempDir(), ev, func(s string) string { return s })
	blocks, err := splitComments(run, v, body)
	want := "Litmus: pass at e2204e0\nThe menu opens.\nNot tested: Account flow.\nNo new findings; also seen before this PR: T3.\n\n![head recording](evidence/head.gif)\n\n" + commentMarker(run, "summary") + "\n"
	if err != nil || len(blocks) != 1 || body != want {
		t.Fatalf("base-only summary got %q err %v, want %q", body, err, want)
	}
	if strings.Contains(body, "finding-1") || strings.Contains(body, "The old menu is clipped.") || strings.Contains(body, "base.gif") {
		t.Fatalf("base finding leaked a body or wrong GIF: %s", body)
	}
	v.Findings = append(v.Findings, Finding{Title: "T4", Leg: "base"})
	body = renderComments(run, v, t.TempDir(), ev, func(s string) string { return s })
	if !strings.Contains(body, "No new findings; also seen before this PR: T3; T4.\n") {
		t.Fatalf("multiple old findings lost their titles: %s", body)
	}
	v.Findings[0].Title = "Old *menu* & label\n" + strings.Repeat("界", 240)
	body = renderComments(run, v, t.TempDir(), ev, func(s string) string { return s })
	line := strings.Split(body, "\n")[3]
	if want := commentSummaryText("No new findings; also seen before this PR: "+v.Findings[0].Title+"; T4.", 200); line != want || !strings.Contains(line, `Old \*menu\* &amp; label`) {
		t.Fatalf("base titles lost escaping or line cap: %q, want %q", line, want)
	}
}
