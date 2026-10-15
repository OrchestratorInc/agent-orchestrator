package litmus

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestCompactSnapshotKeepsInteractiveElementsAndCapsOutput(t *testing.T) {
	input := "- heading \"ignored\" [ref=e1]\n- button \"Keep\" [ref=e2]\n- paragraph \"ignored\"\n"
	for i := 0; i < 200; i++ {
		input += "- link \"" + strings.Repeat("x", 20) + "\" [ref=e" + strings.Repeat("7", 1) + "]\n"
	}
	got := compactSnapshot(input)
	if len(got) > 2048 {
		t.Fatalf("compact snapshot length %d > 2048", len(got))
	}
	if strings.Contains(got, "heading") || strings.Contains(got, "paragraph") {
		t.Fatalf("non-interactive content survived: %q", got)
	}
	if !strings.Contains(got, `button "Keep"`) {
		t.Fatalf("button missing: %q", got)
	}
}

func TestDriveCoveredClickKeepsReceiptAndRefusal(t *testing.T) {
	svc, f, _, runDir := driveSvc(t)
	f.out["get"] = "x: 10 y: 20 width: 100 height: 40"
	f.fail["click"] = errors.New("element is covered by banner")
	res, err := svc.Drive(context.Background(), "s1", "click", []string{"e1"})
	if err == nil || !strings.Contains(err.Error(), "covered") {
		t.Fatalf("covered click err = %v", err)
	}
	rec, ok := res["receipt"].(actionRec)
	if !ok || rec.Reason == "" || rec.After == "" || rec.Box == nil {
		t.Fatalf("covered receipt = %#v", res["receipt"])
	}
	if !strings.HasPrefix(rec.After, runDir+"/receipts/") {
		t.Fatalf("covered after = %q", rec.After)
	}
	checks, readErr := svc.store.Checks("run_s1")
	if readErr != nil || len(checks) != 0 {
		t.Fatalf("unexpected checks: %v %#v", readErr, checks)
	}
	// No check is open in this fixture, but actions.jsonl must still hold the receipt.
	data, _ := os.ReadFile(runDir + "/actions.jsonl")
	if !strings.Contains(string(data), `"reason":"`) {
		t.Fatalf("covered action was not persisted: %s", data)
	}
}

func TestReplayMatchesFreshRoleNameAndStopsAtMissing(t *testing.T) {
	svc, f, _, _ := driveSvc(t)
	base, err := svc.attachEngine(context.Background(), "run_s1", Leg{Name: "base", State: "running", DebugPort: 9401})
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.store.Get("run_s1")
	if err != nil {
		t.Fatal(err)
	}
	run.Legs = append(run.Legs, base)
	if err := svc.store.Save(run); err != nil {
		t.Fatal(err)
	}
	if err := svc.store.SaveChecks(run.ID, []Check{{ID: "c1", Name: "menu", Expect: "menu opens", State: CheckPass, Receipts: []Receipt{{Seq: 1, Leg: "head", Action: "click", Args: []string{"@e7"}, TargetRole: "button", TargetName: "Go"}}}}); err != nil {
		t.Fatal(err)
	}
	f.out["snapshot"] = `- button "Go" [ref=e9]`
	res, err := svc.Replay(context.Background(), "s1", "c1", "base")
	if err != nil {
		t.Fatalf("replay err = %v", err)
	}
	if got, _ := res["stopped"].(string); got != "" {
		t.Fatalf("replay unexpectedly stopped: %s", got)
	}
	if !strings.Contains(res["text"].(string), "step 1:") || !strings.Contains(res["text"].(string), "final snapshot:") {
		t.Fatalf("replay text = %q", res["text"])
	}
	if res["baseCheck"] != "c2" || !strings.Contains(res["text"].(string), "base check: c2") {
		t.Fatalf("replay base check = %#v, text %q", res["baseCheck"], res["text"])
	}
	checks, err := svc.store.Checks(run.ID)
	if err != nil || len(checks) != 2 || len(checks[1].Receipts) != 1 || checks[1].Receipts[0].Args[0] != "@e9" {
		t.Fatalf("replay base receipts = %#v, err %v", checks, err)
	}

	f.out["snapshot"] = `- button "Other" [ref=e10]`
	res, err = svc.Replay(context.Background(), "s1", "c1", "base")
	if err != nil {
		t.Fatalf("missing replay err = %v", err)
	}
	if got := res["stopped"].(string); !strings.Contains(got, "missing button") {
		t.Fatalf("missing replay stop = %q", got)
	}
	checks, err = svc.store.Checks(run.ID)
	if err != nil || len(checks) != 2 || len(checks[1].Receipts) != 2 || checks[1].Receipts[1].Action != "observe" || checks[1].Receipts[1].AfterPath == "" || checks[1].Receipts[1].Reason == "" {
		t.Fatalf("missing replay observation = %#v, err %v", checks, err)
	}
	if _, err := svc.CheckEnd(context.Background(), "s1", "c2", CheckAbsent, "not present on base", false, 0); err != nil {
		t.Fatalf("missing replay could not end base absent: %v", err)
	}
}

func TestReplayStopsCoveredClickAndFailedLoad(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail string
		want string
	}{
		{name: "covered", fail: "click", want: "covered"},
		{name: "load", fail: "wait", want: "failed load"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, f, _, _ := driveSvc(t)
			base, err := svc.attachEngine(context.Background(), "run_s1", Leg{Name: "base", State: "running", DebugPort: 9401})
			if err != nil {
				t.Fatal(err)
			}
			run, err := svc.store.Get("run_s1")
			if err != nil {
				t.Fatal(err)
			}
			run.Legs = append(run.Legs, base)
			if err := svc.store.Save(run); err != nil {
				t.Fatal(err)
			}
			if err := svc.store.SaveChecks(run.ID, []Check{{ID: "c1", Name: tc.name, Expect: "works", State: CheckPass, Receipts: []Receipt{{Seq: 1, Leg: "head", Action: "click", Args: []string{"@e7"}, TargetRole: "button", TargetName: "Go"}}}}); err != nil {
				t.Fatal(err)
			}
			f.out["snapshot"] = `- button "Go" [ref=e9]`
			f.fail[tc.fail] = errors.New(tc.want)
			res, err := svc.Replay(context.Background(), "s1", "c1", "base")
			if err != nil {
				t.Fatalf("replay err = %v", err)
			}
			if stopped, _ := res["stopped"].(string); !strings.Contains(stopped, tc.want) {
				t.Fatalf("replay stop = %q, want %q", stopped, tc.want)
			}
		})
	}
}
