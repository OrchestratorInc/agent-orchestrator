package litmus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

type fakeMedia struct {
	calls [][]string
	err   error
}

func (f *fakeMedia) Run(_ context.Context, args ...string) error {
	f.calls = append(f.calls, args)
	if f.err != nil {
		return f.err
	}
	return os.WriteFile(args[len(args)-1], []byte("mp4"), 0o600)
}

func exists(t *testing.T, path string) {
	t.Helper()
	if !fileExists(path) {
		t.Fatalf("missing file %s", path)
	}
}

func passVerdict() Verdict {
	return Verdict{Result: "pass", Headline: "The menu opens.", Coverage: []Coverage{{Text: "Opened the menu.", Leg: "head", Receipts: []int{1}, Trigger: true}}, Environment: "macOS fixture"}
}

func verdictInput(t *testing.T, svc *Service, v Verdict) string {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	is(t, "")(nil, err)
	path := filepath.Join(svc.store.root, "run_s1", "scratch", "verdict.json")
	writeFile(t, path, string(data))
	return "scratch/verdict.json"
}

// submitSvc has three independent receipts, including receipt 1 on both legs.
func submitSvc(t *testing.T) (*Service, *fakeMedia, string, string) {
	t.Helper()
	svc, now := newSvc(t)
	media := &fakeMedia{}
	svc.media = media
	is(t, "")(svc.Intake(ctx, "s1", prURL))
	run, err := svc.update("run_s1", func(r *Run) {
		r.Comment, r.Evidence, r.State = CommentOff, EvidenceOff, RunRunning
		r.Legs = []Leg{{Name: "head", State: "running"}, {Name: "base", State: "running"}}
	})
	is(t, "")(nil, err)
	runDir := filepath.Join(svc.store.root, run.ID)
	for _, a := range []actionRec{{Seq: 1, Leg: "head"}, {Seq: 1, Leg: "base"}, {Seq: 2, Leg: "head"}} {
		a.Action, a.At = "click", now.Add(time.Duration(a.Seq)*10*time.Second)
		a.After = filepath.Join(runDir, "receipts", fmt.Sprintf("%s-%d-after.png", a.Leg, a.Seq))
		whitePNG(t, a.After, 40, 20)
		svc.attach(run.ID, &a)
	}
	return svc, media, runDir, sessionartifacts.Dir(svc.store.dataDir(), domain.SessionID(run.SessionID))
}

func TestSubmitRefusesLegThatNeverRan(t *testing.T) {
	for _, item := range []string{"result", "finding", "clip", "coverage"} {
		t.Run(item, func(t *testing.T) {
			svc, _, dir, _ := submitSvc(t)
			v := passVerdict()
			_, err := svc.update("run_s1", func(r *Run) {
				r.Legs = []Leg{{Name: "head", State: "running"}, {Name: "base", State: "prepared"}}
				if item == "result" {
					r.Legs[0].State = "prepared"
				}
			})
			is(t, "")(nil, err)
			if item == "finding" {
				v.Findings = []Finding{{Leg: "base", Severity: "worth-a-look"}}
			}
			if item == "clip" {
				v.Findings = []Finding{{Leg: "base", Severity: "worth-a-look", Clip: &FindingClip{From: 1, To: 1}}}
			}
			if item == "coverage" {
				v.Coverage = append(v.Coverage, Coverage{Leg: "base"})
			}
			_, err = svc.Submit(ctx, "s1", verdictInput(t, svc, v))
			is(t, "TESTING_VERDICT_LEG")(nil, err)
			want := "leg base never ran"
			if item == "result" {
				want = "leg head never ran"
			}
			if !strings.Contains(err.Error(), want) {
				t.Fatal(err)
			}
			if fileExists(filepath.Join(dir, "verdict.json")) {
				t.Fatal("refused verdict was copied")
			}
		})
	}
}

func TestSubmitPassNeedsHeadCoverageReceipt(t *testing.T) {
	svc, _, _, _ := submitSvc(t)
	v := passVerdict()
	v.Coverage = []Coverage{{Leg: "base", Receipts: []int{1}}}
	_, err := svc.Submit(ctx, "s1", verdictInput(t, svc, v))
	is(t, "TESTING_VERDICT_COVERAGE")(nil, err)
	if !strings.Contains(err.Error(), "pass needs the trigger coverage item (trigger: true) with an existing receipt on head") {
		t.Fatal(err)
	}
}

func TestSubmitCoverageTriggerJSON(t *testing.T) {
	for _, tc := range []struct {
		name, field, code string
		trigger           bool
	}{
		{name: "omitted", code: "TESTING_VERDICT_COVERAGE"},
		{name: "false", field: `,"trigger":false`, code: "TESTING_VERDICT_COVERAGE"},
		{name: "true", field: `,"trigger":true`, trigger: true},
		{name: "unknown coverage field", field: `,"triger":true`, code: "TESTING_VERDICT_INVALID"},
		{name: "string is not a boolean", field: `,"trigger":"true"`, code: "TESTING_VERDICT_INVALID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, dir, _ := submitSvc(t)
			data := `{"result":"pass","coverage":[{"text":"Opened the menu.","leg":"head","receipts":[1]` + tc.field + `}]}`
			writeFile(t, filepath.Join(dir, "scratch", "verdict.json"), data)
			got, err := svc.Submit(ctx, "s1", "scratch/verdict.json")
			is(t, tc.code)(nil, err)
			if tc.code != "" {
				if fileExists(filepath.Join(dir, verdictFile)) {
					t.Fatal("invalid verdict saved")
				}
				return
			}
			if got.Coverage[0].Trigger != tc.trigger {
				t.Fatalf("coverage trigger = %v, want %v", got.Coverage[0].Trigger, tc.trigger)
			}
			saved, err := os.ReadFile(filepath.Join(dir, verdictFile))
			is(t, "")(nil, err)
			if string(saved) != data {
				t.Fatal("Submit changed tester JSON")
			}
			var stored Verdict
			is(t, "")(nil, json.Unmarshal(saved, &stored))
			if stored.Coverage[0].Trigger != tc.trigger {
				t.Fatalf("stored coverage trigger = %v, want %v", stored.Coverage[0].Trigger, tc.trigger)
			}
		})
	}
}

func TestSubmitBugNeedsReceiptOnItsLeg(t *testing.T) {
	svc, _, _, _ := submitSvc(t)
	v := Verdict{Result: "fail", Findings: []Finding{{Title: "Menu", Leg: "base", Severity: "bug", Receipts: []int{2}}}}
	_, err := svc.Submit(ctx, "s1", verdictInput(t, svc, v))
	is(t, "TESTING_VERDICT_BUG_RECEIPT")(nil, err)
	if !strings.Contains(err.Error(), "bug needs an existing receipt on base") {
		t.Fatal(err)
	}
}

func TestSubmitClipDoesNotReplaceBugProof(t *testing.T) {
	svc, _, _, _ := submitSvc(t)
	v := Verdict{Result: "fail", Findings: []Finding{{Leg: "head", Severity: "bug", Clip: &FindingClip{From: 1, To: 2}}}}
	is(t, "TESTING_VERDICT_BUG_RECEIPT")(svc.Submit(ctx, "s1", verdictInput(t, svc, v)))
}

func TestSubmitBlockedNeedsBlockedOn(t *testing.T) {
	svc, _, _, _ := submitSvc(t)
	_, err := svc.Submit(ctx, "s1", verdictInput(t, svc, Verdict{Result: "blocked"}))
	is(t, "TESTING_VERDICT_BLOCKED_ON")(nil, err)
	if !strings.Contains(err.Error(), "blocked needs blocked_on") {
		t.Fatal(err)
	}
}

func TestSubmitRejectsBlockedOnForNonBlockedResult(t *testing.T) {
	for _, result := range []string{"pass", "partial"} {
		t.Run(result, func(t *testing.T) {
			svc, _, dir, _ := submitSvc(t)
			v := passVerdict()
			v.Result = result
			v.BlockedOn = "No blocker for the exercised Chat paths. Terminal UI report delivery remains unverified."
			_, err := svc.Submit(ctx, "s1", verdictInput(t, svc, v))
			is(t, "TESTING_VERDICT_BLOCKED_ON")(nil, err)
			if !strings.Contains(err.Error(), "use not_tested for scope, version or platform limits") {
				t.Fatal(err)
			}
			if fileExists(filepath.Join(dir, verdictFile)) {
				t.Fatal("invalid verdict saved")
			}
		})
	}
}

func TestSubmitNotReproducedWithNotTested(t *testing.T) {
	svc, dir, artifacts := issueSubmitSvc(t)
	v := passVerdict()
	v.Result, v.Coverage[0].Leg = "not_reproduced", "main"
	v.NotTested = "Codex 0.164 only; older versions and existing controllers were not tested."
	got, err := svc.Submit(ctx, "s1", verdictInput(t, svc, v))
	is(t, "")(nil, err)
	if got.NotTested != v.NotTested {
		t.Fatalf("Submit lost not_tested: %+v", got)
	}
	for _, root := range []string{dir, artifacts} {
		for _, name := range []string{"comment.md", "verdict.md"} {
			body, err := os.ReadFile(filepath.Join(root, name))
			is(t, "")(nil, err)
			if !strings.Contains(string(body), "Not tested: "+v.NotTested+"\n") || strings.Contains(string(body), "Blocked on:") {
				t.Fatalf("scope limit missing or labeled as a blocker in %s: %s", filepath.Join(root, name), body)
			}
		}
	}
}

func TestSubmitRefusesReceiptMissingOnNamedLeg(t *testing.T) {
	for _, item := range []string{"finding", "coverage"} {
		t.Run(item, func(t *testing.T) {
			svc, _, _, _ := submitSvc(t)
			v := passVerdict()
			if item == "finding" {
				v.Findings = []Finding{{Leg: "base", Severity: "worth-a-look", Receipts: []int{2}}}
			} else {
				v.Coverage = append(v.Coverage, Coverage{Leg: "base", Receipts: []int{2}})
			}
			_, err := svc.Submit(ctx, "s1", verdictInput(t, svc, v))
			is(t, "TESTING_VERDICT_RECEIPT")(nil, err)
			if !strings.Contains(err.Error(), "receipt 2 does not exist on leg base") {
				t.Fatal(err)
			}
		})
	}
}

func TestSubmitValidatesFindingClipReceipts(t *testing.T) {
	for _, tc := range []struct {
		name, leg string
		clip      FindingClip
		want      string
	}{
		{"missing from", "head", FindingClip{From: 9, To: 2}, "finding 1: clip from receipt 9 does not exist on leg head"},
		{"missing to", "head", FindingClip{From: 1, To: 9}, "finding 1: clip to receipt 9 does not exist on leg head"},
		{"from on another leg", "base", FindingClip{From: 2, To: 1}, "finding 1: clip from receipt 2 does not exist on leg base"},
		{"to on another leg", "base", FindingClip{From: 1, To: 2}, "finding 1: clip to receipt 2 does not exist on leg base"},
		{"reversed", "head", FindingClip{From: 2, To: 1}, "finding 1: clip from receipt 2 must not follow to receipt 1 on leg head"},
		{"zero from", "head", FindingClip{To: 2}, "finding 1: clip from receipt 0 does not exist on leg head"},
		{"zero to", "head", FindingClip{From: 1}, "finding 1: clip to receipt 0 does not exist on leg head"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, dir, _ := submitSvc(t)
			v := passVerdict()
			v.Findings = []Finding{{Leg: tc.leg, Severity: "worth-a-look", Clip: &tc.clip}}
			_, err := svc.Submit(ctx, "s1", verdictInput(t, svc, v))
			is(t, "TESTING_VERDICT_RECEIPT")(nil, err)
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatal(err)
			}
			if fileExists(filepath.Join(dir, verdictFile)) {
				t.Fatal("invalid clip was saved")
			}
		})
	}
}

func TestSubmitPreservesExplicitClipWithoutChangingProofReceipts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		receipts []int
		clip     FindingClip
	}{
		{"uncited endpoint", []int{2}, FindingClip{From: 1, To: 2}},
		{"single moment without proof", nil, FindingClip{From: 1, To: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, dir, _ := submitSvc(t)
			v := passVerdict()
			v.Findings = []Finding{{Leg: "head", Severity: "worth-a-look", Receipts: tc.receipts, Clip: &tc.clip}}
			path := verdictInput(t, svc, v)
			original, err := os.ReadFile(filepath.Join(dir, path))
			is(t, "")(nil, err)
			got, err := svc.Submit(ctx, "s1", path)
			is(t, "")(nil, err)
			if got.Findings[0].Clip == nil || *got.Findings[0].Clip != tc.clip || !slices.Equal(got.Findings[0].Receipts, tc.receipts) {
				t.Fatalf("finding changed: %+v", got.Findings[0])
			}
			saved, err := os.ReadFile(filepath.Join(dir, verdictFile))
			is(t, "")(nil, err)
			if !bytes.Equal(saved, original) {
				t.Fatal("Submit changed tester JSON")
			}
		})
	}
}

func TestSubmitRefusesIncompleteClipJSON(t *testing.T) {
	for _, clip := range []string{`{}`, `{"from":1}`, `{"to":2}`} {
		t.Run(clip, func(t *testing.T) {
			svc, _, dir, _ := submitSvc(t)
			data := `{"result":"partial","coverage":[{"leg":"head","receipts":[1]}],"findings":[{"leg":"head","severity":"worth-a-look","clip":` + clip + `}]}`
			writeFile(t, filepath.Join(dir, "scratch", "verdict.json"), data)
			is(t, "TESTING_VERDICT_RECEIPT")(svc.Submit(ctx, "s1", "scratch/verdict.json"))
		})
	}
}

func TestSubmitWritesTesterJSONAndReportsWithoutPosting(t *testing.T) {
	svc, _, dir, artifacts := submitSvc(t)
	_, settingErr := svc.update("run_s1", func(r *Run) { r.ProjectID = "p1" })
	is(t, "")(nil, settingErr)
	is(t, "")(svc.SetProjectSettings(ctx, "p1", ProjectSettings{AskBeforePosting: true}))
	_, err := svc.update("run_s1", func(r *Run) { r.Comment = CommentPost; r.Legs = r.Legs[:1] })
	is(t, "")(nil, err)
	g := &commentGH{t: t, login: "bot"}
	svc.gh = g
	v := passVerdict()
	v.Findings = []Finding{{Title: "A note", Leg: "head", Severity: "worth-a-look", Text: "No receipt needed."}}
	path := verdictInput(t, svc, v)
	original, err := os.ReadFile(filepath.Join(dir, path))
	is(t, "")(nil, err)
	var report string
	svc.WithReporter(func(_ context.Context, id, note string) error { report = id + " " + note; return nil })
	killed := make(chan string, 1)
	old := endGrace
	endGrace = time.Millisecond
	t.Cleanup(func() { endGrace = old })
	svc.WithKiller(func(_ context.Context, id string) error { killed <- id; return nil })
	got, err := svc.Submit(ctx, "s1", path)
	is(t, "")(nil, err)
	if got.Result != "pass" || got.ReceiptCount != 3 || got.CommentState != CommentAwaiting || len(g.calls) != 0 {
		t.Fatalf("verdict %+v, calls %v", got, g.calls)
	}
	copied, err := os.ReadFile(filepath.Join(dir, "verdict.json"))
	is(t, "")(nil, err)
	if !bytes.Equal(copied, original) {
		t.Fatal("Submit changed tester JSON")
	}
	for _, name := range []string{"verdict.md", "comment.md", "verdict.json", "index.md"} {
		exists(t, filepath.Join(artifacts, name))
	}
	if !strings.Contains(report, "s1 Litmus verdict pass") {
		t.Fatal(report)
	}
	select {
	case id := <-killed:
		if id != "s1" {
			t.Fatal(id)
		}
	case <-time.After(time.Second):
		t.Fatal("tester was not stopped")
	}
	run, err := svc.store.Get("run_s1")
	is(t, "")(nil, err)
	if run.State != RunAwaitingApproval || run.Legs[0].State != "cleaned" {
		t.Fatalf("run %+v", run)
	}
	is(t, "TESTING_RUN_CLOSED")(svc.Status(ctx, "s1"))
}

func TestSubmitUploadFailureAwaitsApprovalAndCanRetry(t *testing.T) {
	for _, mode := range []string{EvidenceOff, EvidencePost} {
		t.Run(mode, func(t *testing.T) {
			svc, dir, g := askSvc(t, false)
			run, err := svc.update("run_s1", func(r *Run) { r.Repo, r.RepoID, r.Evidence = "o/r", 1234, mode })
			is(t, "")(nil, err)
			v := passVerdict()
			v.Result = "fail"
			v.Findings = []Finding{{Title: "Menu", Leg: "head", Severity: "bug", Receipts: []int{1}, Clip: &FindingClip{From: 1, To: 1}, Text: "The menu stays open."}}
			writeFile(t, findingClipPath(dir, 0), "clip")
			git := &evGit{t: t}
			svc.runner, g.fail = git, upload
			got, err := svc.Submit(ctx, "s1", verdictInput(t, svc, v))
			is(t, "")(nil, err)
			saved, err := svc.store.Get(run.ID)
			is(t, "")(nil, err)
			if saved.Evidence != mode {
				t.Fatalf("submit changed the evidence setting: %+v", saved)
			}
			if mode == EvidenceOff {
				if got.CommentState != "posted" || saved.State != RunSubmitted || saved.EvidenceState != EvidenceOff || len(git.calls) != 0 || g.has(upload) || len(g.sent) != 2 || strings.Contains(g.field(1, "body"), "](") {
					t.Fatalf("evidence-off close uploaded or retained media: %+v gh %v git %v", saved, g.calls, git.calls)
				}
				return
			}
			if saved.State != RunAwaitingApproval || !strings.HasPrefix(saved.CommentState, "failed:") || !strings.Contains(saved.CommentState, "HTTP 403") || got.CommentState != saved.CommentState || saved.CommentURL != "" || len(saved.CommentURLs) != 0 || len(g.sent) != 0 || g.has("api repos/o/r/issues/6394/comments") {
				t.Fatalf("failed close cannot retry or wrote a comment: verdict %+v run %+v calls %v", got, saved, g.calls)
			}
			g.fail = ""
			git.unchanged, git.remote = true, fakeSHA+"\trefs/heads/litmus/evidence\n"
			git.fail = map[string]error{"commit ": errors.New("nothing to commit, working tree clean")}
			posted, err := svc.Post(ctx, run.ID)
			is(t, "")(nil, err)
			if posted.State != RunSubmitted || posted.CommentState != "posted" || posted.CommentURL == "" || posted.Evidence != EvidencePost || posted.EvidenceCommit != fakeSHA || posted.VideoState != "attached" || g.uploads != 1 || len(g.sent) != 2 || !strings.Contains(g.field(1, "body"), "](https://") {
				t.Fatalf("close-time upload failure could not retry: %+v sent %v", posted, g.sent)
			}
		})
	}
}

func TestSubmitKeepsTextAndEscapesMarkdownAtRenderTime(t *testing.T) {
	svc, _, dir, _ := submitSvc(t)
	v := passVerdict()
	v.Headline = "head: compared WITH base?"
	v.Findings = []Finding{{Title: "Odd *title*", Leg: "head", Severity: "worth-a-look", Text: "First line.\n\nSecond line, still mine.\n![image](url) <b> @someone"}}
	writeFile(t, filepath.Join(dir, "home-changes.txt"), homeChangeNotice+"\n+ changed one\n~ changed two\n")
	_, err := svc.Submit(ctx, "s1", verdictInput(t, svc, v))
	is(t, "")(nil, err)
	md, err := os.ReadFile(filepath.Join(dir, "comment.md"))
	is(t, "")(nil, err)
	for _, want := range []string{"First line.\n\nSecond line, still mine.", `Odd \*title\*`, `\!\[image\]\(url\) &lt;b&gt; @` + "\u200b" + "someone", "<!-- litmus:pr:run_s1:finding-1 -->"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("missing %q in %s", want, md)
		}
	}
	local, err := os.ReadFile(filepath.Join(dir, "verdict.md"))
	is(t, "")(nil, err)
	for _, want := range []string{v.Headline, "Home changes (not attributed): 2", "owner logins:", "<!-- litmus:pr:run_s1 -->"} {
		if !strings.Contains(string(local), want) {
			t.Errorf("full report lost %q: %s", want, local)
		}
	}
	var saved Verdict
	is(t, "")(nil, svc.store.read("run_s1", verdictFile, &saved))
	if saved.Findings[0].Text != v.Findings[0].Text {
		t.Fatal("stored tester text changed")
	}
}

func TestSubmitRequiresScratchPathAndReadableReceiptLedger(t *testing.T) {
	svc, _, dir, _ := submitSvc(t)
	path := verdictInput(t, svc, passVerdict())
	outside := filepath.Join(t.TempDir(), "verdict.json")
	writeFile(t, outside, `{"result":"blocked","blocked_on":"outside"}`)
	is(t, "TESTING_VERDICT_PATH")(svc.Submit(ctx, "s1", outside))
	link := filepath.Join(dir, "scratch", "outside.json")
	is(t, "")(nil, os.Symlink(outside, link))
	is(t, "TESTING_VERDICT_PATH")(svc.Submit(ctx, "s1", link))
	writeFile(t, filepath.Join(dir, "actions.jsonl"), "{broken")
	if _, err := svc.Submit(ctx, "s1", path); err == nil || !strings.Contains(err.Error(), "read actions.jsonl") {
		t.Fatalf("corrupt ledger accepted: %v", err)
	}
}

func TestLaunchAndCloseRefuseEachOther(t *testing.T) {
	svc, _, _, _ := submitSvc(t)
	for _, pair := range [][2]string{{"launch head", "submit"}, {"launch base", "cleanup"}, {"submit", "launch head"}, {"cleanup", "launch base"}} {
		svc.mu.Lock()
		done, err := svc.markBusy("run_s1", pair[0])
		svc.mu.Unlock()
		is(t, "")(nil, err)
		svc.mu.Lock()
		_, err = svc.markBusy("run_s1", pair[1])
		svc.mu.Unlock()
		is(t, "TESTING_RUN_BUSY")(nil, err)
		done()
	}
}

func TestSubmitSurvivesFailedCleanupAndSaysSo(t *testing.T) {
	svc, _, dir, _ := submitSvc(t)
	writeFile(t, filepath.Join(dir, "head", "owned.json"), "{broken")
	_, err := svc.Submit(ctx, "s1", verdictInput(t, svc, passVerdict()))
	is(t, "")(nil, err)
	run, err := svc.store.Get("run_s1")
	is(t, "")(nil, err)
	if run.Legs[0].State == "cleaned" {
		t.Fatal("failed cleanup claimed cleaned")
	}
	md, err := os.ReadFile(filepath.Join(dir, "verdict.md"))
	is(t, "")(nil, err)
	if !strings.Contains(string(md), "Cleanup after submit failed") {
		t.Fatal(string(md))
	}
}

func TestSubmitClipFailureKeepsRawRecordingAndReason(t *testing.T) {
	svc, media, dir, artifacts := submitSvc(t)
	svc.runner = &videoProbeRunner{duration: "120"}
	run, err := svc.update("run_s1", func(r *Run) {
		for i := range r.Legs {
			r.Legs[i].RecordStartedAt = r.CreatedAt
			r.Legs[i].RecordStoppedAt = r.CreatedAt.Add(2 * time.Minute)
		}
	})
	is(t, "")(nil, err)
	for _, leg := range run.Legs {
		writeFile(t, recordingPath(dir, leg.Name), "raw-"+leg.Name)
	}
	media.err = errors.New("encode failed")
	v := passVerdict()
	v.Result = "fail"
	v.Findings = []Finding{{Title: "Menu", Leg: "head", Severity: "bug", Receipts: []int{1}, Clip: &FindingClip{From: 1, To: 1}}}
	_, err = svc.Submit(ctx, "s1", verdictInput(t, svc, v))
	is(t, "")(nil, err)
	for _, leg := range []string{"head", "base"} {
		exists(t, filepath.Join(artifacts, leg+".mp4"))
	}
	if !slices.ContainsFunc(media.calls, func(args []string) bool { return slices.Contains(args, recordingPath(dir, "head")) }) {
		t.Fatal("clip encoder did not use head recording")
	}
	log, err := os.ReadFile(filepath.Join(dir, "comment.log"))
	is(t, "")(nil, err)
	if !strings.Contains(string(log), "clip finding-1: encode failed") {
		t.Fatalf("clip error hidden: %s", log)
	}

}

func leaveLeg(t *testing.T, svc *Service) (*fakeSys, *fakeRunner, chan string) {
	t.Helper()
	t0 := time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)
	f, r, killed := &fakeSys{started: map[int]time.Time{100: t0}}, &fakeRunner{}, make(chan string, 4)
	svc.sys, svc.runner = f.ops(), r
	svc.closeEngine = func(id string) { f.log = append(f.log, "engine "+id) }
	svc.WithKiller(func(_ context.Context, id string) error { killed <- id; return nil })
	clone := t.TempDir()
	is(t, "")(nil, os.MkdirAll(filepath.Join(clone, ".git"), 0o750))
	_, err := svc.update("run_s1", func(r *Run) { r.Legs = []Leg{{Name: "head", State: "running", Worktree: clone}} })
	is(t, "")(nil, err)
	dir := svc.store.legDir("run_s1", "head")
	is(t, "")(nil, os.MkdirAll(filepath.Join(dir, "data", "worktrees"), 0o750))
	is(t, "")(nil, writeOwned(dir, Owned{RunID: "run_s1", Leg: "head", PGID: Proc{100, t0}, State: "running", Dirs: []string{filepath.Join(dir, "data")}}))
	return f, r, killed
}

func TestEverySubmitCleansItsLegAndLeavesTheCloneClean(t *testing.T) {
	endGrace = 0
	t.Cleanup(func() { endGrace = 10 * time.Second })
	for name, submit := range map[string]func(*Service) (Verdict, error){
		"pass": func(s *Service) (Verdict, error) { return s.Submit(ctx, "s1", verdictInput(t, s, passVerdict())) },
		"blocked": func(s *Service) (Verdict, error) {
			return s.Submit(ctx, "s1", verdictInput(t, s, Verdict{Result: "blocked", BlockedOn: "seed failed"}))
		},
	} {
		t.Run(name, func(t *testing.T) {
			svc, _, runDir, _ := submitSvc(t)
			f, r, killed := leaveLeg(t, svc)
			if _, err := submit(svc); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(f.log, "|"); !strings.Contains(got, "engine run_s1") || !strings.Contains(got, "killGroup 100") {
				t.Fatalf("submit left the leg running: %q", got)
			}
			if _, err := os.Stat(filepath.Join(svc.store.legDir("run_s1", "head"), "data")); err == nil {
				t.Fatal("the leg's private data survived the submit")
			}
			if r.count("worktree prune") != 1 {
				t.Fatalf("the shared clone was not pruned: %q", r.calls)
			}
			if run, _ := svc.store.Get("run_s1"); run.State != RunSubmitted || run.Legs[0].State != "cleaned" {
				t.Fatalf("run %+v", run)
			}
			if md, _ := os.ReadFile(filepath.Join(runDir, "verdict.md")); strings.Contains(string(md), "Cleanup after submit failed") {
				t.Fatalf("a clean submit reported a cleanup failure:\n%s", md)
			}
			select { // the tester ends after its grace, through the daemon's kill
			case id := <-killed:
				if id != "s1" {
					t.Fatalf("killed %q", id)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("the tester was not stopped after its run closed")
			}
		})
	}
}

func TestSubmitAcceptsPreviouslyLaunchedLegWithoutRecording(t *testing.T) {
	svc, _, _, _ := submitSvc(t)
	_, err := svc.update("run_s1", func(r *Run) { r.Legs = []Leg{{Name: "head", State: "cleaned", Engine: &EngineInfo{}}} })
	svc.closeEngine = func(string) {}
	is(t, "")(nil, err)
	_, err = svc.Submit(ctx, "s1", verdictInput(t, svc, passVerdict()))
	is(t, "")(nil, err)
}

func TestSubmitRefusesScratchDirectoryLinkedOutsideRun(t *testing.T) {
	svc, _, dir, _ := submitSvc(t)
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "verdict.json"), `{"result":"blocked","blocked_on":"outside"}`)
	is(t, "")(nil, os.Symlink(outside, filepath.Join(dir, "scratch")))
	is(t, "TESTING_VERDICT_PATH")(svc.Submit(ctx, "s1", "scratch/verdict.json"))
}

func TestSubmitRefusesUnknownAndGoOwnedFields(t *testing.T) {
	for _, key := range []string{"unknown", "receipt_count", "commentUrl", "commentState", "evidenceState", "evidenceCommit", "createdAt"} {
		t.Run(key, func(t *testing.T) {
			svc, _, dir, _ := submitSvc(t)
			writeFile(t, filepath.Join(dir, "scratch", "verdict.json"), fmt.Sprintf(`{"result":"blocked","blocked_on":"fixture",%q:1}`, key))
			_, err := svc.Submit(ctx, "s1", "scratch/verdict.json")
			is(t, "TESTING_VERDICT_INVALID")(nil, err)
			if !strings.Contains(err.Error(), `unknown field "`+key+`"`) {
				t.Fatal(err)
			}
			if fileExists(filepath.Join(dir, verdictFile)) {
				t.Fatal("invalid input saved")
			}
		})
	}
}

func TestSubmitRefusesUnknownSeverity(t *testing.T) {
	for _, severity := range []string{"", "Bug", "critical"} {
		t.Run(severity, func(t *testing.T) {
			svc, _, _, _ := submitSvc(t)
			v := passVerdict()
			v.Findings = []Finding{{Leg: "head", Severity: severity}}
			is(t, "TESTING_VERDICT_SEVERITY")(svc.Submit(ctx, "s1", verdictInput(t, svc, v)))
		})
	}
}

func TestReceiptOutputInFindingCommentIsRedactedAndCapped(t *testing.T) {
	svc, _, dir, _ := submitSvc(t)
	lines := []string{"HTTP 200 ghp_fakeToken123", "```", "![not an image](url)"}
	for i := 4; i <= 12; i++ {
		lines = append(lines, fmt.Sprintf("output line %d", i))
	}
	output := strings.Join(lines, "\n")
	after := filepath.Join(dir, "receipts", "head-3-after.png")
	whitePNG(t, after, 40, 20)
	svc.attach("run_s1", &actionRec{Seq: 3, Leg: "head", Action: "eval", After: after, Output: output})
	v := passVerdict()
	v.Findings = []Finding{{Title: "API result", Leg: "head", Severity: "worth-a-look", Receipts: []int{3}}}
	is(t, "")(svc.Submit(ctx, "s1", verdictInput(t, svc, v)))
	body, err := os.ReadFile(filepath.Join(dir, "verdict.md"))
	is(t, "")(nil, err)
	finding := strings.Split(string(body), "## Coverage")[0]
	if !strings.Contains(finding, "[head receipt 3](receipts/head-3-after.png)\n\n````text\n"+output+"\n````") {
		t.Fatalf("output missing or outside code fence in verdict.md: %s", body)
	}
	comment, err := os.ReadFile(filepath.Join(dir, "comment.md"))
	is(t, "")(nil, err)
	lines[0] = "HTTP 200 [...]"
	if !strings.Contains(string(comment), "````text\n"+strings.Join(lines[:10], "\n")+"\n````") {
		t.Fatalf("comment output missing, unredacted or not capped at ten lines: %s", comment)
	}
	is(t, "")(nil, checkEgress(svc.credentialPattern(ctx), string(comment)))
	for _, unwanted := range []string{"ghp_fakeToken123", "output line 11", "output line 12", "receipt 3", "receipts/", "Environment"} {
		if strings.Contains(string(comment), unwanted) {
			t.Fatalf("comment contains %q: %s", unwanted, comment)
		}
	}
}

func TestCutReceiptOutputQuotesOnlyWholeLines(t *testing.T) {
	svc, f, _, dir := driveSvc(t)
	svc.media = &fakeMedia{}
	token := "ghp_" + strings.Repeat("a", 36)
	f.out["eval"] = token + "\n" + strings.Repeat("x", 4<<10-len(token)+2) + "\nlast line"
	_, err := svc.Drive(ctx, "s1", "eval", nil)
	is(t, "")(nil, err)
	if raw := readActions(dir); len(raw) != 1 || !strings.HasPrefix(raw[0].Output, "aaaa") {
		t.Fatalf("the cut should split the token for this test: %+v", raw)
	}
	v := passVerdict()
	v.Findings = []Finding{{Title: "API result", Leg: "head", Severity: "worth-a-look", Receipts: []int{1}}}
	is(t, "")(svc.Submit(ctx, "s1", verdictInput(t, svc, v)))
	comment, err := os.ReadFile(filepath.Join(dir, "comment.md"))
	is(t, "")(nil, err)
	if strings.Contains(string(comment), "aaaa") || !strings.Contains(string(comment), "last line") {
		t.Fatalf("cut output quoted a split first line or lost whole lines: %s", strings.ReplaceAll(string(comment), strings.Repeat("x", 100), "X"))
	}
}

func TestQuotedOutputDropsPageBoundaryAndListBullets(t *testing.T) {
	svc, f, _, dir := driveSvc(t)
	svc.media = &fakeMedia{}
	f.out["eval"] = "--- AGENT_BROWSER_PAGE_CONTENT nonce=27b3 origin=http://localhost:5173/#/projects/p ---\n\"[{\\\"text\\\":\\\"Download\\\"}]\"\n--- END_AGENT_BROWSER_PAGE_CONTENT nonce=27b3 ---\n"
	_, err := svc.Drive(ctx, "s1", "eval", nil)
	is(t, "")(nil, err)
	v := passVerdict()
	v.NotTested = "- Linux and Windows.\n- The benchmark."
	v.Findings = []Finding{{Title: "Low contrast", Leg: "head", Severity: "worth-a-look", Receipts: []int{1}}}
	is(t, "")(svc.Submit(ctx, "s1", verdictInput(t, svc, v)))
	for _, name := range []string{"comment.md", "verdict.md"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		is(t, "")(nil, err)
		out := string(data)
		if strings.Contains(out, "AGENT_BROWSER_PAGE_CONTENT") || strings.Contains(out, "localhost:5173") || !strings.Contains(out, "```text\n[{\"text\":\"Download\"}]\n```") {
			t.Fatalf("%s quoted the engine boundary: %s", name, out)
		}
		if !strings.Contains(out, "Not tested: Linux and Windows. The benchmark.") {
			t.Fatalf("%s did not read the not_tested list as one line: %s", name, out)
		}
	}
}
