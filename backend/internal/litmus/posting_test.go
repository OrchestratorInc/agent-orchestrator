package litmus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPostUpdatesOneOwnCommentByRunMarkerAndKeepsEdits(t *testing.T) {
	svc, _, dir, _ := submitSvc(t)
	_, settingErr := svc.update("run_s1", func(r *Run) { r.ProjectID = "p1" })
	is(t, "")(nil, settingErr)
	is(t, "")(svc.SetProjectSettings(ctx, "p1", ProjectSettings{AskBeforePosting: true}))
	run, err := svc.update("run_s1", func(r *Run) { r.Comment = CommentPost })
	is(t, "")(nil, err)
	_, err = svc.Submit(ctx, "s1", verdictInput(t, svc, passVerdict()))
	is(t, "")(nil, err)
	body, err := os.ReadFile(filepath.Join(dir, "comment.md"))
	is(t, "")(nil, err)
	body = append(body, []byte("\nMy edit stays here.\n")...)
	is(t, "")(nil, svc.store.WriteFile(run.ID, "comment.md", body))
	g := &commentGH{t: t, login: "bot", comments: []fakeComment{{ID: 100, Login: "someone-else", Body: commentMarker(run, "summary")}, {ID: 101, Login: "bot", Body: commentMarker(run, "summary")}, {ID: 102, Login: "bot", Body: "<!-- litmus:pr:another-run:summary -->"}}}
	svc.gh = g
	posted, err := svc.Post(ctx, run.ID)
	is(t, "")(nil, err)
	if posted.CommentState != "updated" || !g.has("api --method PATCH repos/"+run.Repo+"/issues/comments/101 ") || len(g.sent) != 1 {
		t.Fatalf("post %+v calls %v", posted, g.calls)
	}
	if !strings.Contains(g.field(0, "body"), "My edit stays here.") || strings.Contains(g.field(0, "body"), "](receipts/") {
		t.Fatal(g.field(0, "body"))
	}
	for _, call := range g.calls {
		if strings.Contains(call, "/reviews") {
			t.Fatalf("review sent: %s", call)
		}
	}
}

func TestFindCommentReadsPaginatedJSONAndRejectsBrokenPage(t *testing.T) {
	raw := []byte(`[{"id":1,"body":"marker","user":{"login":"other"}}][{"id":2,"body":"marker","user":{"login":"BOT"}}]`)
	if id, err := findComment(raw, "marker", "bot"); err != nil || id != 2 {
		t.Fatalf("id %d err %v", id, err)
	}
	if _, err := findComment([]byte(`[{`), "marker", "bot"); err == nil {
		t.Fatal("broken page accepted")
	}
	raw = []byte(`[{"id":1,"body":"<!-- litmus:pr:run_s1:finding-10 -->","user":{"login":"bot"}},{"id":2,"body":"<!-- litmus:pr:run_s1:finding-1 -->","user":{"login":"other"}},{"id":3,"body":"<!-- litmus:pr:run_s1:finding-1 -->","user":{"login":"bot"}}]`)
	if id, err := findComment(raw, commentMarker(commentRun, "finding-1"), "bot"); err != nil || id != 3 {
		t.Fatalf("full marker matched another finding or author: id %d err %v", id, err)
	}
}

func TestPostRejectsChangedMarkerSetsBeforeNetwork(t *testing.T) {
	for _, edit := range []string{"renamed", "removed marker", "removed block", "extra", "duplicate", "inline before", "inline after", "another run", "duplicate summary", "missing summary", "removed summary block", "renamed summary", "inline summary before", "inline summary after"} {
		t.Run(edit, func(t *testing.T) {
			svc, dir, g := askSvc(t, true)
			run, err := svc.update("run_s1", func(r *Run) { r.Evidence = EvidencePost })
			is(t, "")(nil, err)
			v := passVerdict()
			v.Findings = []Finding{{Title: "First", Leg: "head", Severity: "worth-a-look", Text: "First observation."}, {Title: "Second", Leg: "head", Severity: "worth-a-look", Text: "Second observation."}}
			is(t, "")(svc.Submit(ctx, "s1", verdictInput(t, svc, v)))
			body := readRunFile(t, svc, run, "comment.md")
			marker, wantMarker := commentMarker(run, "finding-1"), "finding-1"
			switch edit {
			case "renamed":
				body = strings.Replace(body, marker, commentMarker(run, "finding-9"), 1)
				wantMarker = "finding-9"
			case "removed marker":
				body = strings.Replace(body, marker, "", 1)
			case "removed block":
				body = body[:strings.Index(body, marker)+len(marker)] + "\n"
				wantMarker = "finding-2"
			case "extra":
				body += "\n" + commentMarker(run, "finding-3") + "\n"
				wantMarker = "finding-3"
			case "duplicate":
				body += "\n" + marker + "\n"
			case "inline before":
				body = strings.Replace(body, marker, "Owner text. "+marker, 1)
			case "inline after":
				body = strings.Replace(body, marker, marker+" Owner text.", 1)
			case "another run":
				body = strings.Replace(body, marker, "<!-- litmus:pr:another-run:finding-1 -->", 1)
				wantMarker = "another-run"
			case "duplicate summary":
				body = strings.Replace(body, marker, commentMarker(run, "summary"), 1)
				wantMarker = "summary"
			case "missing summary":
				body = strings.Replace(body, commentMarker(run, "summary"), "", 1)
				wantMarker = "summary"
			case "removed summary block":
				_, body, _ = strings.Cut(body, commentMarker(run, "summary")+"\n\n")
				wantMarker = "summary"
			case "renamed summary":
				body = strings.Replace(body, commentMarker(run, "summary"), commentMarker(run, "overview"), 1)
				wantMarker = "overview"
			case "inline summary before":
				body = strings.Replace(body, commentMarker(run, "summary"), "Owner text. "+commentMarker(run, "summary"), 1)
				wantMarker = "summary"
			case "inline summary after":
				body = strings.Replace(body, commentMarker(run, "summary"), commentMarker(run, "summary")+" Owner text.", 1)
				wantMarker = "summary"
			}
			writeFile(t, filepath.Join(dir, "comment.md"), body)
			git := &evGit{t: t}
			svc.runner = git
			posted, err := svc.Post(ctx, run.ID)
			is(t, "")(nil, err)
			if posted.State != RunAwaitingApproval || !strings.HasPrefix(posted.CommentState, "failed:comment.md") || !strings.Contains(posted.CommentState, wantMarker) || len(g.calls) != 0 || len(git.calls) != 0 {
				t.Fatalf("marker edit escaped the gate: %+v gh %v git %v", posted, g.calls, git.calls)
			}
			if got := readRunFile(t, svc, run, "comment.md"); got != body {
				t.Fatal("refusal changed the owner's draft")
			}
		})
	}
}

func TestPostRejectsFindingMarkerForSummaryBeforeNetwork(t *testing.T) {
	svc, dir, g := askSvc(t, true)
	is(t, "")(svc.Submit(ctx, "s1", verdictInput(t, svc, passVerdict())))
	run, err := svc.store.Get("run_s1")
	is(t, "")(nil, err)
	body := strings.Replace(readRunFile(t, svc, run, "comment.md"), commentMarker(run, "summary"), commentMarker(run, "finding-1"), 1)
	writeFile(t, filepath.Join(dir, "comment.md"), body)
	git := &evGit{t: t}
	svc.runner = git
	posted, err := svc.Post(ctx, run.ID)
	is(t, "")(nil, err)
	if posted.State != RunAwaitingApproval || !strings.HasPrefix(posted.CommentState, "failed:comment.md marker") || !strings.Contains(posted.CommentState, "finding-1") || len(g.calls) != 0 || len(git.calls) != 0 {
		t.Fatalf("finding marker replaced the expected summary: %+v gh %v git %v", posted, g.calls, git.calls)
	}
}

func TestPostSendsReorderedSummaryFirstAndMakesItsURLPrimary(t *testing.T) {
	for _, tc := range []struct {
		name  string
		order []int
	}{{"summary in middle", []int{2, 0, 1}}, {"summary last", []int{2, 1, 0}}} {
		t.Run(tc.name, func(t *testing.T) {
			svc, dir, g := askSvc(t, true)
			v := passVerdict()
			v.Findings = []Finding{
				{Title: "Old bug", Leg: "head", Severity: "worth-a-look", Text: "The old menu is clipped."},
				{Title: "Other bug", Leg: "head", Severity: "worth-a-look", Text: "The old sidebar is clipped."},
			}
			is(t, "")(svc.Submit(ctx, "s1", verdictInput(t, svc, v)))
			run, err := svc.update("run_s1", func(r *Run) {
				r.Repo = "o/r"
				r.CommentURL = "https://github.com/o/r/pull/7#issuecomment-101"
				r.CommentURLs = map[string]string{commentMarker(*r, "finding-1"): r.CommentURL}
			})
			is(t, "")(nil, err)
			bodies := []string{
				fmt.Sprintf("Litmus: pass at %.7s\nOwner edit: the tested menu works.\n2 findings below\n\n%s\n", run.HeadSHA, commentMarker(run, "summary")),
				"**Old bug (PR)**\n\nOwner edit: the old menu label is clipped.\nKeep this second paragraph.\n\n" + commentMarker(run, "finding-1") + "\n",
				"**Other bug (PR)**\n\nOwner edit: the old sidebar label is also clipped.\n\n" + commentMarker(run, "finding-2") + "\n",
			}
			writeFile(t, filepath.Join(dir, "comment.md"), bodies[tc.order[0]]+"\n"+bodies[tc.order[1]]+"\n"+bodies[tc.order[2]])
			g.comments = []fakeComment{{ID: 101, Login: g.login, Body: commentMarker(run, "finding-1")}}
			posted, err := svc.Post(ctx, run.ID)
			is(t, "")(nil, err)
			if posted.CommentState != "posted" || len(g.sent) != 3 || !g.has("api --method PATCH repos/o/r/issues/comments/101 ") || posted.CommentURL != "https://github.com/o/r/pull/7#issuecomment-901" || posted.CommentURL != posted.CommentURLs[commentMarker(run, "summary")] {
				t.Fatalf("summary order or primary URL lost: %+v sent %v calls %v", posted, g.sent, g.calls)
			}
			for i, want := range []string{bodies[0], bodies[2], bodies[1]} {
				if got := g.field(i, "body"); got != want {
					t.Fatalf("payload %d lost edited prose or finding order:\ngot %q\nwant %q", i, got, want)
				}
			}
		})
	}
}

func TestPostUpsertsEachFindingAndPublishesMediaRegardlessOfStartSetting(t *testing.T) {
	for _, mode := range []string{EvidenceOff, EvidencePost} {
		t.Run(mode, func(t *testing.T) {
			svc, dir, g := askSvc(t, true)
			g.private = "false"
			run, err := svc.update("run_s1", func(r *Run) {
				r.Repo, r.RepoID, r.Evidence = "o/r", 1234, mode
				r.Gaps = []string{"Signed-in recovery flow."}
			})
			is(t, "")(nil, err)
			v := passVerdict()
			v.Result = "partial"
			for i, title := range []string{"Menu", "Sidebar"} {
				v.Findings = append(v.Findings, Finding{Title: title, Leg: "head", Severity: "bug", Receipts: []int{i + 1}, Clip: &FindingClip{From: i + 1, To: i + 1}, Text: "Open the menu. It stays open. It should close."})
				writeFile(t, findingClipPath(dir, i), "clip")
			}
			is(t, "")(svc.Submit(ctx, "s1", verdictInput(t, svc, v)))
			if len(g.calls) != 0 {
				t.Fatal("Submit uploaded before approval")
			}
			git := &evGit{t: t}
			svc.runner = git
			for attempt := 0; attempt < 2; attempt++ {
				posted, err := svc.Post(ctx, run.ID)
				is(t, "")(nil, err)
				wantState := "posted"
				if attempt > 0 {
					wantState = "updated"
				}
				if posted.CommentState != wantState || posted.Evidence != mode || posted.EvidenceState != "published" || posted.EvidenceCommit != fakeSHA || posted.VideoState != "attached" || len(posted.VideoURLs) != 2 || g.uploads != 2 || len(g.comments) != 3 || len(g.sent) != 3*(attempt+1) || posted.CommentURL != "https://github.com/o/r/pull/7#issuecomment-900" || posted.CommentURLs[commentMarker(run, "summary")] != posted.CommentURL {
					t.Fatalf("post %d: %+v comments %v calls %v", attempt, posted, g.comments, g.calls)
				}
				summary := g.field(attempt*3, "body")
				wantSummary := fmt.Sprintf("Litmus: partial at %.7s\nThe menu opens.\nNot tested: Signed-in recovery flow.\n2 findings below\n\n%s\n", run.HeadSHA, commentMarker(run, "summary"))
				if summary != wantSummary {
					t.Fatalf("summary was not posted first with four lines: %s", summary)
				}
				sentBodies := []string{summary}
				for i := range v.Findings {
					body := g.field(attempt*3+i+1, "body")
					sentBodies = append(sentBodies, body)
					if !strings.HasSuffix(body, commentMarker(run, findingID(i))+"\n") || strings.Count(body, "<!-- litmus:") != 1 || !strings.Contains(body, "](https://raw.githubusercontent.com/o/r/"+fakeSHA+"/") || !strings.Contains(body, findingID(i)+".png)") || !strings.Contains(body, "](https://github.com/user-attachments/assets/") {
						t.Fatalf("wrong finding body or missing https media: %s", body)
					}
					for _, unwanted := range []string{"](evidence/", "](clips/", "not published", "receipt", "Environment", ".gif"} {
						if strings.Contains(body, unwanted) {
							t.Fatalf("local media or report text %q: %s", unwanted, body)
						}
					}
					if attempt > 0 && !strings.Contains(body, "Owner edit.") {
						t.Fatalf("repost lost hand edit: %s", body)
					}
				}
				if draft := readRunFile(t, svc, run, "comment.md"); draft != strings.Join(sentBodies, "\n") {
					t.Fatalf("draft differs from exact post bodies: %s", draft)
				}
				if attempt == 0 {
					git.remote = fakeSHA + "\trefs/heads/litmus/evidence\n"
					git.unchanged = true
					git.fail = map[string]error{"commit ": errors.New("nothing to commit, working tree clean")}
					g.fail = upload
					for i := range v.Findings {
						is(t, "")(nil, os.Remove(findingClipPath(dir, i)))
					}
					writeFile(t, filepath.Join(dir, "comment.md"), strings.ReplaceAll(readRunFile(t, svc, run, "comment.md"), "Open the menu.", "Owner edit. Open the menu."))
				}
			}
			if !g.has("api --method PATCH repos/o/r/issues/comments/900 ") || !g.has("api --method PATCH repos/o/r/issues/comments/901 ") || !g.has("api --method PATCH repos/o/r/issues/comments/902 ") {
				t.Fatalf("repeat post did not update each marker: %v", g.calls)
			}
			commits, uploads := 0, 0
			for _, call := range git.calls {
				if strings.Contains(call, " commit ") {
					commits++
				}
			}
			for _, call := range g.calls {
				if strings.HasPrefix(call, upload) {
					uploads++
				}
			}
			if commits != 1 || uploads != 2 {
				t.Fatalf("repost attempted an empty commit or another upload: commits %d uploads %d", commits, uploads)
			}
		})
	}
}

func TestPostFailedUploadPostsNothingAndPreservesDraft(t *testing.T) {
	for _, failure := range []string{"image", "clip", "both"} {
		t.Run(failure, func(t *testing.T) {
			svc, dir, g := askSvc(t, true)
			run, err := svc.update("run_s1", func(r *Run) { r.Repo, r.RepoID = "o/r", 1234 })
			is(t, "")(nil, err)
			v := passVerdict()
			v.Result = "fail"
			v.Findings = []Finding{{Title: "Menu", Leg: "head", Severity: "bug", Receipts: []int{1}, Clip: &FindingClip{From: 1, To: 1}, Text: "The menu stays open."}}
			writeFile(t, findingClipPath(dir, 0), "clip")
			is(t, "")(svc.Submit(ctx, "s1", verdictInput(t, svc, v)))
			git := &evGit{t: t}
			svc.runner = git
			reason := "HTTP 403"
			if failure == "image" || failure == "both" {
				git.fail = map[string]error{"push ": errors.New("image upload failed")}
				reason = "image upload failed"
			}
			if failure == "clip" || failure == "both" {
				g.fail = upload
			}
			draft := readRunFile(t, svc, run, "comment.md")
			if !strings.Contains(draft, "](evidence/finding-1.png)") {
				t.Fatal("upload-failure fixture has no staged image line")
			}
			posted, err := svc.Post(ctx, run.ID)
			is(t, "")(nil, err)
			if posted.State != RunAwaitingApproval || !strings.HasPrefix(posted.CommentState, "failed:") || !strings.Contains(posted.CommentState, reason) || posted.Evidence != EvidenceOff || posted.CommentURL != "" || len(g.sent) != 0 || len(g.comments) != 0 || g.has("api repos/o/r/issues/7/comments") || readRunFile(t, svc, run, "comment.md") != draft {
				t.Fatalf("failed upload posted or changed the draft: %+v sent %v", posted, g.sent)
			}
		})
	}
}

func TestPostPublishesSingleFindingImageAndClipWithoutReceiptLinks(t *testing.T) {
	svc, _, dir, _ := submitSvc(t)
	_, settingErr := svc.update("run_s1", func(r *Run) { r.ProjectID = "p1" })
	is(t, "")(nil, settingErr)
	is(t, "")(svc.SetProjectSettings(ctx, "p1", ProjectSettings{AskBeforePosting: true}))
	run, err := svc.update("run_s1", func(r *Run) { r.Repo = "o/r"; r.RepoID = 1234; r.Comment = CommentPost; r.Evidence = EvidenceOff })
	is(t, "")(nil, err)
	g := &commentGH{t: t, login: "bot", private: "false"}
	svc.gh = g
	v := passVerdict()
	v.Result = "fail"
	v.Findings = []Finding{{Title: "Menu", Leg: "head", Severity: "bug", Receipts: []int{1, 2}, Clip: &FindingClip{From: 1, To: 2}, Text: "The menu closes."}}
	writeFile(t, findingClipPath(dir, 0), "clip")
	_, err = svc.Submit(ctx, "s1", verdictInput(t, svc, v))
	is(t, "")(nil, err)
	if len(g.calls) != 0 {
		t.Fatal("Submit published evidence")
	}
	git := &evGit{t: t}
	svc.runner = git
	authCalls := g.authCalls
	posted, err := svc.Post(ctx, run.ID)
	is(t, "")(nil, err)
	if posted.Evidence != EvidenceOff || posted.EvidenceState != "published" || posted.VideoState != "attached" || g.uploads != 1 || g.authCalls != authCalls+1 {
		t.Fatalf("posted %+v", posted)
	}
	if len(g.sent) != 2 || !strings.Contains(g.field(0, "body"), commentMarker(run, "summary")) {
		t.Fatalf("summary did not precede the finding: %v", g.sent)
	}
	body := g.field(1, "body")
	for _, want := range []string{"raw.githubusercontent.com/o/r/", "finding-1.png", "https://github.com/user-attachments/assets/", commentMarker(run, "finding-1")} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s in %s", want, body)
		}
	}
	if strings.Contains(body, "](evidence/") || strings.Contains(body, "](clips/") || strings.Contains(body, "](receipts/") {
		t.Fatal("public comment has local links")
	}
	for _, unwanted := range []string{"head-1-after.png", "head-2-after.png", "Evidence:", "Environment", "```", ".gif"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("comment contains %q: %s", unwanted, body)
		}
	}
}

func TestPostingWritesOnlyOwnCommentsAndEvidence(t *testing.T) {
	for _, kind := range []string{"pr", "issue"} {
		for _, approval := range []bool{false, true} {
			for _, existing := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/approval=%t/existing=%t", kind, approval, existing), func(t *testing.T) {
					var svc *Service
					var dir string
					leg, result := "head", "fail"
					if kind == "issue" {
						svc, dir, _ = issueSubmitSvc(t)
						leg, result = "main", "reproduced"
					} else {
						svc, _, dir, _ = submitSvc(t)
					}
					run, err := svc.update("run_s1", func(r *Run) {
						r.Repo, r.RepoID, r.RepoVisibility = "o/r", 1234, "public"
						r.ProjectID, r.Comment, r.Evidence = "p1", CommentPost, EvidencePost
					})
					is(t, "")(nil, err)
					is(t, "")(svc.SetProjectSettings(ctx, "p1", ProjectSettings{AskBeforePosting: approval}))
					g := &commentGH{t: t, login: "bot", comments: []fakeComment{
						{ID: 100, Login: "someone-else", Body: commentMarker(run, "finding-1")},
						{ID: 102, Login: "bot", Body: "<!-- litmus:" + kind + ":another-run -->"},
						{ID: 104, Login: "someone-else", Body: commentMarker(run, "summary")},
					}}
					if existing {
						g.comments = append(g.comments, fakeComment{ID: 101, Login: "bot", Body: commentMarker(run, "finding-1")}, fakeComment{ID: 103, Login: "bot", Body: commentMarker(run, "summary")})
					}
					svc.gh = g
					git := &evGit{t: t}
					v := Verdict{Result: result, Headline: "The menu closes.", Environment: "fixture", Findings: []Finding{
						{Title: "Menu", Leg: leg, Severity: "bug", Receipts: []int{1}, Clip: &FindingClip{From: 1, To: 1}, Text: "The menu closes."},
					}}
					writeFile(t, findingClipPath(dir, 0), "clip")
					// Intake and cleanup use their existing fakes; only publication uses the evidence clone.
					if !approval {
						svc.runner = git
					}
					_, err = svc.Submit(ctx, "s1", verdictInput(t, svc, v))
					is(t, "")(nil, err)
					if approval {
						if len(g.argv) != 0 {
							t.Fatalf("submit wrote before approval: %v", g.argv)
						}
						svc.runner = git
						_, err = svc.Post(ctx, run.ID)
						is(t, "")(nil, err)
					}
					posted, err := svc.store.Get(run.ID)
					is(t, "")(nil, err)
					state := "posted"
					commentWrite := fmt.Sprintf("POST repos/o/r/issues/%d/comments", run.Number())
					summaryWrite := commentWrite
					if existing {
						state, commentWrite = "updated", "PATCH repos/o/r/issues/comments/101"
						summaryWrite = "PATCH repos/o/r/issues/comments/103"
					}
					if posted.CommentState != state || posted.EvidenceState != "published" || posted.VideoState != "attached" {
						t.Fatalf("publication failed: %+v", posted)
					}
					var writes []string
					for _, args := range g.argv {
						if args[0] != "api" && args[0] != "repo" {
							t.Fatalf("unexpected GitHub command: %v", args)
						}
						if write, ok := githubAPIWrite(args); ok {
							writes = append(writes, write)
						}
					}
					want := []string{
						"POST " + assetsURL + "?name=finding-1.mp4&content_type=video%2Fmp4&repository_id=1234",
						summaryWrite,
						commentWrite,
					}
					if !slices.Equal(writes, want) {
						t.Fatalf("only own comments and evidence may be written: got %v want %v", writes, want)
					}
					if len(g.sent) != 2 || !strings.HasSuffix(g.field(0, "body"), commentMarker(run, "summary")+"\n") || !strings.HasSuffix(g.field(1, "body"), commentMarker(run, "finding-1")+"\n") || posted.CommentURL != posted.CommentURLs[commentMarker(run, "summary")] {
						t.Fatalf("comment payloads: %v", g.sent)
					}
					for _, sent := range g.sent {
						var payload map[string]json.RawMessage
						is(t, "")(nil, json.Unmarshal([]byte(sent), &payload))
						if len(payload) != 1 || payload["body"] == nil {
							t.Fatalf("only the comment body may be sent, no labels, assignees, state or lock: %s", sent)
						}
					}
					pushes := 0
					clone := filepath.Join(svc.store.dataDir(), "litmus", "evidence", "o__r")
					for _, call := range git.calls {
						dir, args, _ := strings.Cut(call, "|")
						args, ok := strings.CutPrefix(args, "-c "+ghCredentials+" ")
						if !ok {
							t.Fatalf("evidence git lacks its per-command credential helper: %s", call)
						}
						if dir != clone && !(dir == filepath.Dir(clone) && strings.HasPrefix(args, "clone ")) {
							t.Fatalf("evidence command outside its clone: %s", call)
						}
						if strings.HasPrefix(args, "push ") {
							pushes++
							if args != "push origin litmus/evidence" {
								t.Fatalf("write outside evidence branch: %s", call)
							}
						}
					}
					if pushes != 1 {
						t.Fatalf("evidence pushes: %d", pushes)
					}
				})
			}
		}
	}
}

// githubAPIWrite classifies recorded argv independently of commentGH's endpoint whitelist.
func githubAPIWrite(args []string) (string, bool) {
	if len(args) == 0 || args[0] != "api" {
		return "", false
	}
	var method, endpoint string
	var fields, shortMethod bool
	for i, arg := range args {
		flag, value, assigned := strings.Cut(arg, "=")
		switch {
		case flag == "--method" || arg == "-X":
			if !assigned && i+1 < len(args) {
				value = args[i+1]
			}
			method = strings.ToUpper(value)
			shortMethod = shortMethod || arg == "-X"
		case strings.HasPrefix(arg, "-X"):
			method, shortMethod = strings.ToUpper(strings.TrimPrefix(arg, "-X")), true
		case flag == "--field" || flag == "--raw-field" || flag == "--input" || strings.HasPrefix(arg, "-f") || strings.HasPrefix(arg, "-F"):
			fields = true
		case strings.HasPrefix(arg, "repos/") || strings.HasPrefix(arg, "https://"):
			endpoint = arg
		}
	}
	if method == "" {
		method = "GET"
		if fields {
			method = "POST"
		}
	}
	return method + " " + endpoint, shortMethod || fields || method != "GET"
}

func TestGitHubAPIWriteClassification(t *testing.T) {
	const endpoint = "repos/o/r/issues/7"
	for _, tc := range []struct {
		name, method string
		flags        []string
		write        bool
	}{
		{name: "default GET", method: "GET"},
		{name: "explicit GET", method: "GET", flags: []string{"--method", "GET"}},
		{name: "assigned GET", method: "GET", flags: []string{"--method=GET"}},
		{name: "explicit POST", method: "POST", flags: []string{"--method", "POST"}, write: true},
		{name: "assigned PATCH", method: "PATCH", flags: []string{"--method=PATCH"}, write: true},
		{name: "short PATCH", method: "PATCH", flags: []string{"-X", "PATCH"}, write: true},
		{name: "attached PATCH", method: "PATCH", flags: []string{"-XPATCH"}, write: true},
		{name: "short GET is guarded", method: "GET", flags: []string{"-X", "GET"}, write: true},
		{name: "raw labels", method: "POST", flags: []string{"-f", "labels[]=bug"}, write: true},
		{name: "typed state", method: "POST", flags: []string{"-F", "state=closed"}, write: true},
		{name: "attached raw field", method: "POST", flags: []string{"-fstate=closed"}, write: true},
		{name: "attached typed field", method: "POST", flags: []string{"-Fstate=closed"}, write: true},
		{name: "long field", method: "POST", flags: []string{"--field", "state=closed"}, write: true},
		{name: "assigned field", method: "POST", flags: []string{"--field=state=closed"}, write: true},
		{name: "long raw field", method: "POST", flags: []string{"--raw-field", "state=closed"}, write: true},
		{name: "assigned raw field", method: "POST", flags: []string{"--raw-field=state=closed"}, write: true},
		{name: "input", method: "POST", flags: []string{"--input", "payload.json"}, write: true},
		{name: "assigned input", method: "POST", flags: []string{"--input=payload.json"}, write: true},
		{name: "GET with fields is guarded", method: "GET", flags: []string{"--method", "GET", "-f", "state=closed"}, write: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"api", endpoint}, tc.flags...)
			got, write := githubAPIWrite(args)
			if got != tc.method+" "+endpoint || write != tc.write {
				t.Fatalf("%v: got %q write %t", args, got, write)
			}
		})
	}
	for _, args := range [][]string{nil, {"repo", "view", "o/r"}} {
		if _, write := githubAPIWrite(args); write {
			t.Fatalf("non-API command classified as API write: %v", args)
		}
	}
}

func TestDriveThemeIsUnknownAction(t *testing.T) {
	for _, action := range []string{"theme", "unknown"} {
		t.Run(action, func(t *testing.T) {
			svc, f, _, _ := driveSvc(t)
			f.fail[action] = fmt.Errorf("unknown command: %s", action)
			out, err := svc.Drive(ctx, "s1", action, []string{"dark"})
			is(t, "TESTING_ENGINE_FAILED")(nil, err)
			if out["refused"] != true || !strings.Contains(err.Error(), f.fail[action].Error()) {
				t.Fatalf("out=%v err=%v", out, err)
			}
			if got := f.find(action); !slices.Equal(got, []string{action, "dark"}) {
				t.Fatalf("engine command=%v", got)
			}
		})
	}
}

func TestUploadedVideoMustBePlayableAndRetriesOneFresh404(t *testing.T) {
	svc, _, dir, _ := submitSvc(t)
	g := &commentGH{t: t, login: "bot", notFound: 1}
	svc.gh = g
	old := videoRetryWait
	videoRetryWait = time.Millisecond
	t.Cleanup(func() { videoRetryWait = old })
	file := filepath.Join(dir, "clip.mp4")
	writeFile(t, file, "clip")
	url, err := svc.uploadVideoFile(ctx, file, "finding-1", 1234, svc.credentialPattern(ctx))
	is(t, "")(nil, err)
	if !assetURL.MatchString(url) || g.notFound != 0 || g.uploads != 1 {
		t.Fatalf("url %s calls %v", url, g.calls)
	}
	wrong := ghFunc(func(context.Context, ...string) ([]byte, error) {
		return []byte("HTTP/2.0 200 OK\r\nContent-Type: text/html\r\n\r\n"), nil
	})
	svc.gh = wrong
	if err := svc.verifyVideo(ctx, url); err == nil {
		t.Fatal("HTML accepted as video")
	}
}

type ghFunc func(context.Context, ...string) ([]byte, error)

func (f ghFunc) Run(ctx context.Context, args ...string) ([]byte, error) { return f(ctx, args...) }

func TestGhSendUsesTemporaryJSONAndRemovesIt(t *testing.T) {
	svc, _, _, _ := submitSvc(t)
	run, err := svc.store.Get("run_s1")
	is(t, "")(nil, err)
	text := "Quoted \"text\"\nwith a second line."
	var file string
	svc.gh = ghFunc(func(_ context.Context, args ...string) ([]byte, error) {
		file = args[len(args)-1]
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		var body map[string]string
		if err := json.Unmarshal(data, &body); err != nil {
			return nil, err
		}
		if body["body"] != text {
			t.Fatal(body)
		}
		return []byte(`{}`), nil
	})
	_, err = svc.ghSend(ctx, run, "POST", "comments", map[string]string{"body": text})
	is(t, "")(nil, err)
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("temporary payload remains: %v", err)
	}
}

func TestPostAllowsOwnerToDeleteMediaLinesForTextOnly(t *testing.T) {
	svc, _, g := askSvc(t, true)
	run, err := svc.update("run_s1", func(r *Run) { r.Repo = "o/r" })
	is(t, "")(nil, err)
	v := passVerdict()
	v.Findings = []Finding{{Title: "Menu", Leg: "head", Severity: "worth-a-look", Clip: &FindingClip{From: 1, To: 1}, Text: "The menu label is clipped."}}
	writeFile(t, findingClipPath(filepath.Join(svc.store.root, run.ID), 0), "clip")
	is(t, "")(svc.Submit(ctx, "s1", verdictInput(t, svc, v)))
	body := readRunFile(t, svc, run, "comment.md")
	if !strings.Contains(body, "![") || !strings.Contains(body, "[Clip") {
		t.Fatalf("text-only edit has no media to remove: %s", body)
	}
	body = rewriteCommentLink(body, "evidence/finding-1.png", "")
	body = rewriteCommentLink(body, "clips/finding-1.mp4", "")
	writeFile(t, filepath.Join(svc.store.root, run.ID, "comment.md"), body)
	svc.runner = &evGit{t: t, fail: map[string]error{"push ": errors.New("image upload failed")}}
	posted, err := svc.Post(ctx, run.ID)
	is(t, "")(nil, err)
	if posted.CommentState != "posted" || posted.Evidence != EvidenceOff || len(g.sent) != 2 || !strings.Contains(g.field(1, "body"), v.Findings[0].Text) || strings.Contains(g.field(1, "body"), "![") || !strings.HasSuffix(g.field(0, "body"), commentMarker(run, "summary")+"\n") {
		t.Fatalf("owner's text-only edit failed: %+v sent %v", posted, g.sent)
	}
}

func TestPostDropsMediaThatWasNotStaged(t *testing.T) {
	for _, scenario := range []string{"clipless", "old clipless draft", "missing after-shot", "copy failed", "missing clip"} {
		t.Run(scenario, func(t *testing.T) {
			svc, dir, g := askSvc(t, true)
			run, err := svc.update("run_s1", func(r *Run) { r.Repo, r.RepoID = "o/r", 1234 })
			is(t, "")(nil, err)
			v := passVerdict()
			v.Findings = []Finding{{Title: "Menu", Leg: "head", Severity: "worth-a-look", Text: "The menu label is clipped."}}
			if scenario == "missing after-shot" || scenario == "copy failed" || scenario == "missing clip" {
				v.Result, v.Findings[0].Severity = "fail", "bug"
				v.Findings[0].Receipts, v.Findings[0].Clip = []int{1}, &FindingClip{From: 1, To: 1}
				writeFile(t, findingClipPath(dir, 0), "clip")
			}
			if scenario == "missing after-shot" {
				is(t, "")(nil, os.Remove(filepath.Join(dir, "receipts", "head-1-after.png")))
			}
			if scenario == "copy failed" {
				is(t, "")(nil, os.MkdirAll(filepath.Join(dir, "evidence", "finding-1.png"), 0o750))
			}
			is(t, "")(svc.Submit(ctx, "s1", verdictInput(t, svc, v)))
			draft := readRunFile(t, svc, run, "comment.md")
			if scenario != "missing clip" && (strings.Contains(draft, "![") || fileExists(filepath.Join(dir, "evidence", "finding-1.png"))) {
				t.Fatalf("unstaged finding image appears in draft: %s", draft)
			}
			if scenario == "missing clip" {
				is(t, "")(nil, os.Remove(findingClipPath(dir, 0)))
				if !strings.Contains(draft, "[Clip") {
					t.Fatal("missing-clip fixture has no draft clip line")
				}
			} else if scenario != "clipless" {
				// Also exercise old drafts that named an image before staging could fail.
				draft = strings.Replace(draft, commentMarker(run, "finding-1"), "![old image](evidence/finding-1.png)\n\n"+commentMarker(run, "finding-1"), 1)
				writeFile(t, filepath.Join(dir, "comment.md"), draft)
			}
			svc.runner = &evGit{t: t}
			posted, err := svc.Post(ctx, run.ID)
			is(t, "")(nil, err)
			wantUploads := 0
			if scenario == "missing after-shot" || scenario == "copy failed" {
				wantUploads = 1
			}
			if posted.CommentState != "posted" || posted.Evidence != EvidenceOff || len(g.sent) != 2 || g.uploads != wantUploads || !strings.Contains(g.field(1, "body"), v.Findings[0].Text) {
				t.Fatalf("unstaged media blocked posting: %+v sent %v", posted, g.sent)
			}
			for _, body := range []string{g.field(1, "body"), readRunFile(t, svc, run, "comment.md")} {
				if scenario != "missing clip" && strings.Contains(body, "![") || scenario == "missing clip" && strings.Contains(body, "[Clip") {
					t.Fatalf("unstaged media line survived: %s", body)
				}
				if err := checkCommentLinks(body, dir); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestFailedRepostKeepsEvidenceAndRetriesWithoutUploadsOrCommit(t *testing.T) {
	for _, failure := range []string{"marker", "local link", "credential", "login", "listing"} {
		t.Run(failure, func(t *testing.T) {
			svc, dir, g := askSvc(t, true)
			run, err := svc.update("run_s1", func(r *Run) { r.Repo, r.RepoID = "o/r", 1234 })
			is(t, "")(nil, err)
			v := passVerdict()
			v.Result = "fail"
			v.Findings = []Finding{{Title: "Menu", Leg: "head", Severity: "bug", Receipts: []int{1}, Clip: &FindingClip{From: 1, To: 1}, Text: "The menu stays open."}}
			writeFile(t, findingClipPath(dir, 0), "clip")
			is(t, "")(svc.Submit(ctx, "s1", verdictInput(t, svc, v)))
			git := &evGit{t: t}
			svc.runner = git
			before, err := svc.Post(ctx, run.ID)
			is(t, "")(nil, err)
			if before.CommentState != "posted" || before.EvidenceCommit != fakeSHA || len(before.VideoURLs) != 1 || g.uploads != 1 {
				t.Fatalf("first post failed: %+v", before)
			}
			original := readRunFile(t, svc, run, "comment.md")
			body := original
			git.unchanged, git.remote = true, fakeSHA+"\trefs/heads/litmus/evidence\n"
			git.fail = map[string]error{"commit ": errors.New("nothing to commit, working tree clean")}
			g.fail = upload
			is(t, "")(nil, os.Remove(findingClipPath(dir, 0)))
			switch failure {
			case "marker":
				body = strings.Replace(body, commentMarker(run, "finding-1"), commentMarker(run, "finding-9"), 1)
			case "local link":
				body += "\n[proof](receipts/head-1-after.png)\n"
			case "credential":
				body += "\nghp_injectedToken\n"
			case "login":
				svc.login, g.fail = "", "api user"
			case "listing":
				g.fail = "comments --paginate"
			}
			writeFile(t, filepath.Join(dir, "comment.md"), body)
			failed, err := svc.Post(ctx, run.ID)
			is(t, "")(nil, err)
			saved, saveErr := svc.store.Get(run.ID)
			if saveErr != nil || !strings.HasPrefix(failed.CommentState, "partial:") || failed.State != RunSubmitted || failed.EvidenceCommit != before.EvidenceCommit || saved.EvidenceCommit != before.EvidenceCommit || failed.VideoURLs["finding-1"] != before.VideoURLs["finding-1"] || saved.VideoURLs["finding-1"] != before.VideoURLs["finding-1"] || len(g.sent) != 2 {
				t.Fatalf("failed repost erased publication facts: before %+v failed %+v saved %+v err %v", before, failed, saved, saveErr)
			}
			g.fail = upload
			writeFile(t, filepath.Join(dir, "comment.md"), strings.Replace(original, "The menu stays open.", "Owner edit. The menu stays open.", 1))
			retried, err := svc.Post(ctx, run.ID)
			is(t, "")(nil, err)
			if retried.CommentState != "updated" || retried.EvidenceCommit != fakeSHA || retried.VideoURLs["finding-1"] != before.VideoURLs["finding-1"] || len(g.sent) != 4 || !strings.Contains(g.field(3, "body"), "Owner edit.") {
				t.Fatalf("retry failed: %+v sent %v", retried, g.sent)
			}
			commits, uploads := 0, 0
			for _, call := range git.calls {
				if strings.Contains(call, " commit ") {
					commits++
				}
			}
			for _, call := range g.calls {
				if strings.HasPrefix(call, upload) {
					uploads++
				}
			}
			if commits != 1 || uploads != 1 {
				t.Fatalf("retry attempted an empty commit or repeated upload: commits %d uploads %d", commits, uploads)
			}
		})
	}
}

func TestPostPassesOldBaseBlocksThroughUnchanged(t *testing.T) {
	for _, onlyBase := range []bool{false, true} {
		for _, failure := range []string{"", "credential", "unsafe link"} {
			t.Run(fmt.Sprintf("only base=%t/%s", onlyBase, failure), func(t *testing.T) {
				t.Setenv("LITMUS_TEST_PASSWORD", "old&verdict-secret")
				svc, dir, g := askSvc(t, true)
				run, err := svc.update("run_s1", func(r *Run) { r.Repo = "o/r"; r.Gaps = []string{"Login flow."} })
				is(t, "")(nil, err)
				v := passVerdict()
				v.Findings = []Finding{{Title: "Head one", Leg: "head", Severity: "worth-a-look"}, {Title: "Old *bug* old&verdict-secret", Leg: "base", Severity: "worth-a-look"}, {Title: "Head three", Leg: "head", Severity: "worth-a-look"}}
				if onlyBase {
					v.Findings = v.Findings[1:2]
				}
				is(t, "")(svc.Submit(ctx, "s1", verdictInput(t, svc, v)))
				summary := fmt.Sprintf("Litmus: pass at %.7s\nNo new findings after signing in.\nNot tested: Owner gap stays.\n%d findings below\n\nOwner summary note stays.\n\n%s\n", run.HeadSHA, len(v.Findings), commentMarker(run, "summary"))
				bodies := []string{summary}
				baseIndex := 0
				for i, f := range v.Findings {
					block := fmt.Sprintf("**Owner title %d**\n\nOwner finding edit %d stays.\n\n%s\n", i+1, i+1, commentMarker(run, findingID(i)))
					if f.Leg == "base" {
						baseIndex = i + 1
						extra := ""
						if failure == "credential" {
							extra = "ghp_injectedToken"
						} else if failure == "unsafe link" {
							extra = "[proof](http://example.com/private)"
						}
						block = extra + block
						g.comments = []fakeComment{{ID: 42, Login: g.login, Body: "Previously posted base comment.\n\n" + commentMarker(run, findingID(i))}}
					}
					bodies = append(bodies, block)
				}
				draft := strings.Join(bodies, "\n")
				writeFile(t, filepath.Join(dir, "comment.md"), draft)
				git := &evGit{t: t}
				svc.runner = git
				posted, err := svc.Post(ctx, run.ID)
				is(t, "")(nil, err)
				if failure != "" {
					reason := "credential detected"
					if failure == "unsafe link" {
						reason = "unsafe link target"
					}
					if posted.State != RunAwaitingApproval || !strings.HasPrefix(posted.CommentState, "failed:") || !strings.Contains(posted.CommentState, reason) || len(g.sent) != 0 || len(g.calls) != 0 || len(git.calls) != 0 {
						t.Fatalf("old base block bypassed egress checks: %+v gh %v git %v", posted, g.calls, git.calls)
					}
				} else {
					if posted.CommentState != "posted" || len(g.sent) != len(bodies) || !g.has("api --method PATCH repos/o/r/issues/comments/42 ") || g.has("api --method DELETE") || g.comments[0].Body != bodies[baseIndex] {
						t.Fatalf("old base block did not follow normal upsert: %+v calls %v", posted, g.calls)
					}
					for i, want := range bodies {
						if got := g.field(i, "body"); got != want {
							t.Fatalf("draft block %d changed: got %q want %q", i, got, want)
						}
						is(t, "")(nil, checkEgress(svc.credentialPattern(ctx), g.field(i, "body")))
						is(t, "")(nil, checkCommentLinks(g.field(i, "body"), dir))
					}
				}
				if got := readRunFile(t, svc, run, "comment.md"); got != draft {
					t.Fatalf("old draft changed: got %q want %q", got, draft)
				}
			})
		}
	}
}

func TestPostSkipsBaseFindingMedia(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprintf("cached=%t", cached), func(t *testing.T) {
			svc, dir, g := askSvc(t, true)
			run, err := svc.update("run_s1", func(r *Run) { r.Repo = "o/r" })
			is(t, "")(nil, err)
			v := passVerdict()
			v.Findings = []Finding{{Title: "Old bug", Leg: "base", Severity: "worth-a-look", Receipts: []int{1}, Clip: &FindingClip{From: 1, To: 1}}}
			writeFile(t, findingClipPath(dir, 0), "base clip")
			is(t, "")(svc.Submit(ctx, "s1", verdictInput(t, svc, v)))
			if fileExists(filepath.Join(dir, "evidence", "finding-1.png")) || fileExists(filepath.Join(dir, "evidence", "base-1-after.png")) {
				t.Fatal("base finding staged an unlinked image")
			}
			if cached {
				run, err = svc.update(run.ID, func(r *Run) {
					r.VideoURLs = map[string]string{"finding-1": "https://github.com/user-attachments/assets/11111111-aaaa-4bbb-8ccc-dddddddddddd"}
				})
				is(t, "")(nil, err)
			}
			g.fail = upload
			git := &evGit{t: t}
			svc.runner = git
			url, state, ev := svc.publishComment(ctx, run, v, readRunFile(t, svc, run, "comment.md"), regexp.MustCompile(`finding-1\.mp4`), true)
			if state != "posted" || url == "" || ev.videoState != "off" || len(ev.findingClips) != 0 || g.has(upload) || len(g.sent) != 1 {
				t.Fatalf("base media affected publication: state %s ev %+v calls %v", state, ev, g.calls)
			}
			if !slices.Equal(git.added, []string{"head-1-after.png"}) {
				t.Fatalf("base finding uploaded images: %v", git.added)
			}
		})
	}
}
