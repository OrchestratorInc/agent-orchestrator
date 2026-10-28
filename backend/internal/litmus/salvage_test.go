package litmus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDeadlineSalvageIsBlockedAndCountsReceipts(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "open", true: "already expired"}[expired], func(t *testing.T) {
			svc, _, dir, _ := submitSvc(t)
			run, err := svc.update("run_s1", func(r *Run) {
				r.Comment = CommentPost
				if expired {
					r.State = RunExpired
				}
			})
			is(t, "")(nil, err)
			svc.clock = func() time.Time { return run.Deadline.Add(writeupGrace) }
			var report string
			svc.WithReporter(func(_ context.Context, _ string, note string) error { report = note; return nil })
			if killed := sweep(svc, nil); len(killed) != 0 {
				t.Fatal(killed)
			}
			var v Verdict
			is(t, "")(nil, svc.store.read(run.ID, verdictFile, &v))
			if v.Result != "blocked" || v.BlockedOn != "run budget expired before submit" || len(v.Findings) != 0 || v.ReceiptCount != 3 {
				t.Fatalf("verdict %+v", v)
			}
			closed, err := svc.store.Get(run.ID)
			is(t, "")(nil, err)
			if closed.State != RunSubmitted || closed.CommentState != CommentOff || !strings.Contains(report, "blocked") {
				t.Fatalf("run %+v report %s", closed, report)
			}
			exists(t, filepath.Join(dir, "comment.md"))
		})
	}
}

func TestDeadlineSalvageNeedsNoReceipts(t *testing.T) {
	svc, _, _, _ := submitSvc(t)
	is(t, "")(nil, svc.store.WriteFile("run_s1", "actions.jsonl", nil))
	run, err := svc.store.Get("run_s1")
	is(t, "")(nil, err)
	if !svc.closeAtDeadline(ctx, run, quiet) {
		t.Fatal("run was not handled")
	}
	var v Verdict
	is(t, "")(nil, svc.store.read(run.ID, verdictFile, &v))
	if v.Result != "blocked" || v.ReceiptCount != 0 {
		t.Fatalf("verdict %+v", v)
	}
}

func TestDeadlineSalvageWaitsForLongVerbAndReapsOnWriteFailure(t *testing.T) {
	svc, _, dir, _ := submitSvc(t)
	run, err := svc.store.Get("run_s1")
	is(t, "")(nil, err)
	svc.clock = func() time.Time { return run.Deadline.Add(writeupGrace) }
	svc.mu.Lock()
	done, err := svc.markBusy(run.ID, "launch head")
	svc.mu.Unlock()
	is(t, "")(nil, err)
	sweep(svc, nil)
	if fileExists(filepath.Join(dir, verdictFile)) {
		t.Fatal("salvage raced launch")
	}
	done()
	is(t, "")(nil, os.Mkdir(filepath.Join(dir, verdictFile), 0o700))
	killed := sweep(svc, nil)
	if !svc.reaped(run.ID) || len(killed) != 1 || killed[0] != "s1" {
		t.Fatalf("failed salvage not reaped, killed %v", killed)
	}
	log := readRunFile(t, svc, run, "run.log")
	if !strings.Contains(log, "deadline close failed:") {
		t.Fatal(log)
	}
	is(t, "")(nil, os.Remove(filepath.Join(dir, verdictFile)))
	if killed := sweep(svc, nil); len(killed) != 0 {
		t.Fatal(killed)
	}
	closed, err := svc.store.Get(run.ID)
	is(t, "")(nil, err)
	if closed.State != RunExpired || fileExists(filepath.Join(dir, verdictFile)) {
		t.Fatalf("failed salvage was retried: %+v", closed)
	}
}

func TestDeadlineSalvageCountsReceiptArrivingBeforeBusyMarker(t *testing.T) {
	svc, _, _, _ := submitSvc(t)
	run, err := svc.store.Get("run_s1")
	is(t, "")(nil, err)
	var once sync.Once
	svc.clock = func() time.Time {
		once.Do(func() {
			path := filepath.Join(svc.store.root, run.ID, "actions.jsonl")
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
			is(t, "")(nil, err)
			_, err = f.WriteString("{\"seq\":99,\"leg\":\"head\",\"action\":\"click\"}\n")
			is(t, "")(nil, errors.Join(err, f.Close()))
		})
		return run.Deadline.Add(writeupGrace)
	}
	if !svc.closeAtDeadline(ctx, run, quiet) {
		t.Fatal("run was not handled")
	}
	var v Verdict
	is(t, "")(nil, svc.store.read(run.ID, verdictFile, &v))
	if v.ReceiptCount != 4 {
		t.Fatalf("receipt count %d", v.ReceiptCount)
	}
}

func TestDeadlineSalvageReapsCorruptReceiptLedger(t *testing.T) {
	svc, _, dir, _ := submitSvc(t)
	run, err := svc.store.Get("run_s1")
	is(t, "")(nil, err)
	svc.clock = func() time.Time { return run.Deadline.Add(writeupGrace) }
	writeFile(t, filepath.Join(dir, "actions.jsonl"), "{broken")
	if killed := sweep(svc, nil); len(killed) != 1 || !svc.reaped(run.ID) {
		t.Fatalf("killed %v", killed)
	}
	if log := readRunFile(t, svc, run, "run.log"); !strings.Contains(log, "read actions.jsonl") {
		t.Fatal(log)
	}
}

func TestDeadlineWriteupGraceKeepsSubmitOpen(t *testing.T) {
	svc, _, dir, _ := submitSvc(t)
	run, err := svc.store.Get("run_s1")
	is(t, "")(nil, err)
	at := run.Deadline
	svc.clock = func() time.Time { return at }
	for _, action := range []string{"browser", "screenshot", "record"} {
		args := map[string][]string{"browser": {"snapshot"}, "record": {"stop"}}[action]
		is(t, "TESTING_RUN_CLOSED")(svc.Drive(ctx, "s1", action, args))
	}
	is(t, "TESTING_RUN_CLOSED")(svc.Launch(ctx, "s1", "head"))
	is(t, "")(svc.Status(ctx, "s1"))
	path := verdictInput(t, svc, passVerdict())
	at = run.Deadline.Add(writeupGrace - time.Second)
	if killed := sweep(svc, nil); len(killed) != 0 || fileExists(filepath.Join(dir, verdictFile)) {
		t.Fatal("sweep closed the run during write-up grace")
	}
	got, err := svc.Submit(ctx, "s1", path)
	is(t, "")(nil, err)
	if got.Result != "pass" {
		t.Fatalf("grace submit: %+v", got)
	}
}

func TestDeadlineSalvageValidatesScratchVerdict(t *testing.T) {
	for _, tc := range []struct{ name, data, result string }{
		{"valid", "", "pass"},
		{"valid observed bug", `{"result":"fail","headline":"The menu closes.","findings":[{"title":"Menu","leg":"head","severity":"bug","receipts":[1],"clip":{"from":1,"to":1},"text":"Closes immediately."}]}`, "fail"},
		{"contradictory word", `{"result":"fail","findings":[]}`, "blocked"},
		{"malformed", `{`, "blocked"},
		{"unknown field", `{"result":"blocked","blocked_on":"setup","extra":true}`, "blocked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, dir, _ := submitSvc(t)
			path := verdictInput(t, svc, passVerdict())
			if tc.data != "" {
				writeFile(t, filepath.Join(dir, path), tc.data)
			}
			original, err := os.ReadFile(filepath.Join(dir, path))
			is(t, "")(nil, err)
			run, err := svc.store.Get("run_s1")
			is(t, "")(nil, err)
			svc.clock = func() time.Time { return run.Deadline.Add(writeupGrace) }
			is(t, "TESTING_RUN_CLOSED")(svc.Submit(ctx, "s1", path))
			sweep(svc, nil)
			var v Verdict
			is(t, "")(nil, svc.store.read(run.ID, verdictFile, &v))
			if v.Result != tc.result {
				t.Fatalf("salvaged verdict: %+v", v)
			}
			closed, err := svc.store.Get(run.ID)
			if err != nil || closed.State != RunSubmitted {
				t.Fatalf("salvage state %+v: %v", closed, err)
			}
			report := readRunFile(t, svc, run, "verdict.md")
			if !strings.HasPrefix(report, "# Verdict: "+tc.result+"\n") || !strings.Contains(report, "Receipts: 3\n") || tc.result != "blocked" && !strings.Contains(report, "submitted by Litmus at the deadline from scratch/verdict.json") {
				t.Errorf("verdict.md lacks result, receipt count or salvage provenance: %s", report)
			}
			comment := readRunFile(t, svc, run, "comment.md")
			if tc.result == "blocked" {
				if comment != "" {
					t.Errorf("blocked verdict produced a public body: %s", comment)
				}
			} else {
				blocks, err := splitComments(run, v, comment)
				start := "Litmus: " + tc.result + " at " + run.HeadSHA[:7] + "\n" + v.Headline + "\n"
				if err != nil || len(blocks) != 1+len(v.Findings) || !strings.HasPrefix(comment, start) || !strings.HasSuffix(blocks[0].body, "\n"+commentMarker(run, "summary")+"\n") || strings.Count(comment, "<!-- litmus:") != 1+len(v.Findings) {
					t.Fatalf("salvaged comment lost its summary or markers: %s err %v", comment, err)
				}
				if tc.result == "fail" && (!strings.HasPrefix(blocks[1].body, "**Menu (PR)**\n\nCloses immediately.\n") || !strings.HasSuffix(comment, "\n"+commentMarker(run, "finding-1")+"\n")) {
					t.Errorf("salvaged comment lost its finding body or marker: %s", comment)
				}
			}
			if tc.result != "blocked" {
				saved, err := os.ReadFile(filepath.Join(dir, verdictFile))
				if err != nil || string(saved) != string(original) {
					t.Fatalf("salvage changed tester JSON: %v", err)
				}
			} else if v.ReceiptCount != 3 {
				t.Fatalf("generated verdict lost receipt count: %+v", v)
			}
			if tc.result == "fail" && (len(v.Findings) != 1 || v.Findings[0].Text != "Closes immediately." || v.Headline != "The menu closes.") {
				t.Fatalf("observed bug was lost: %+v", v)
			}
			if tc.result == "blocked" && v.BlockedOn != "run budget expired before submit" {
				t.Fatalf("invalid scratch verdict was accepted: %+v", v)
			}
		})
	}
}
