package litmus

import (
	"strings"
	"testing"
)

func TestCheckEndShotValidationAndPersistence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		shot   int
		leg    string
		path   string
		result CheckState
		want   string
	}{
		{"unknown receipt", 99, "head", evidenceFile(t), CheckFail, "TESTING_ARGS_INVALID"},
		{"base receipt", 11, "base", evidenceFile(t), CheckFail, "TESTING_ARGS_INVALID"},
		{"missing after-shot", 11, "head", "missing.png", CheckFail, "TESTING_ARGS_INVALID"},
		{"directory after-shot", 11, "head", t.TempDir(), CheckFail, "TESTING_ARGS_INVALID"},
		{"negative receipt", -1, "head", evidenceFile(t), CheckFail, "TESTING_ARGS_INVALID"},
		{"fail", 11, "head", evidenceFile(t), CheckFail, ""},
		{"partial", 11, "head", evidenceFile(t), CheckPartial, ""},
		{"pass", 11, "", evidenceFile(t), CheckPass, ""},
		{"absent", 11, "head", evidenceFile(t), CheckAbsent, ""},
		{"skipped", 11, "head", evidenceFile(t), CheckSkipped, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, now := newSvc(t)
			is(t, "")(nil, svc.store.Save(Run{ID: "run_s1", SessionID: "s1", State: RunRunning, Deadline: now.Add(RunBudget)}))
			checks := []Check{
				{ID: "c2", State: CheckPass, Leg: tc.leg, Receipts: []Receipt{{Seq: 11, Leg: tc.leg, AfterPath: tc.path}}},
				{ID: "c9", State: CheckRunning, Receipts: []Receipt{{Seq: 42, AfterPath: evidenceFile(t)}}},
			}
			is(t, "")(nil, svc.store.SaveChecks("run_s1", checks))
			got, err := svc.CheckEnd(ctx, "s1", "c9", tc.result, "notice covers buttons", false, tc.shot)
			is(t, tc.want)(got, err)
			saved, err := svc.store.Checks("run_s1")
			is(t, "")(nil, err)
			if tc.want == "" {
				if got.Shot != tc.shot || saved[1].Shot != tc.shot || saved[1].State != tc.result {
					t.Fatalf("shot not saved: returned %+v, saved %+v", got, saved[1])
				}
			} else if saved[1].Shot != 0 || saved[1].State != CheckRunning {
				t.Fatalf("invalid shot changed the check: %+v", saved[1])
			}
		})
	}
}

func TestCheckEndSameFaultIsBaseOnly(t *testing.T) {
	for _, leg := range []string{"", "head", "base"} {
		for _, result := range []CheckState{CheckFail, CheckPartial} {
			svc, now := newSvc(t)
			is(t, "")(nil, svc.store.Save(Run{ID: "run_s1", SessionID: "s1", State: RunRunning, Deadline: now.Add(RunBudget)}))
			c := Check{ID: "c1", Name: "menu", State: CheckRunning, Leg: leg, Receipts: []Receipt{{Leg: leg, Action: "click", AfterPath: evidenceFile(t)}}}
			is(t, "")(nil, svc.store.SaveChecks("run_s1", []Check{c}))
			got, err := svc.CheckEnd(ctx, "s1", c.ID, result, "same broken row", true, 0)
			if leg != "base" {
				is(t, "TESTING_CHECK_SAME_FAULT")(got, err)
			} else if err != nil || !got.SameFault || got.State != result {
				t.Fatalf("base %s: %+v, %v", result, got, err)
			}
			saved, err := svc.store.Checks("run_s1")
			is(t, "")(nil, err)
			if saved[0].SameFault != (leg == "base") || (leg != "base" && saved[0].State != CheckRunning) {
				t.Fatalf("%s %s: saved %+v", leg, result, saved[0])
			}
		}
	}
}

func TestCheckEvidenceNeedsSavedFiles(t *testing.T) {
	for _, c := range []struct {
		name   string
		r      Receipt
		result CheckState
		want   string
	}{
		{"blank shot", Receipt{Action: "click"}, CheckPass, "no picture was saved; act again or end it skipped"},
		{"missing shot", Receipt{Action: "click", AfterPath: "missing.png"}, CheckFail, "no picture was saved; act again or end it skipped"},
		{"partial with missing shot", Receipt{Action: "click"}, CheckPartial, "no picture was saved; act again or end it skipped"},
		{"absent with missing shot", Receipt{Action: "click"}, CheckAbsent, "no picture was saved; act again or end it skipped"},
		{"directory is not a picture", Receipt{Action: "click", AfterPath: t.TempDir()}, CheckFail, "no picture was saved"},
	} {
		_, _, err := checkEvidence(Check{Receipts: []Receipt{c.r}}, c.result, "")
		is(t, "TESTING_CHECK_EVIDENCE")(nil, err)
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func TestCheckEndSameFaultRequiresFailedOrPartialBase(t *testing.T) {
	for _, result := range []CheckState{CheckPass, CheckAbsent, CheckSkipped} {
		svc, now := newSvc(t)
		is(t, "")(nil, svc.store.Save(Run{ID: "run_s1", SessionID: "s1", State: RunRunning, Deadline: now.Add(RunBudget)}))
		c := Check{ID: "c1", Name: "account", Leg: "base", State: CheckRunning, Receipts: []Receipt{{Leg: "base", Action: "click", AfterPath: evidenceFile(t)}}}
		is(t, "")(nil, svc.store.SaveChecks("run_s1", []Check{c}))
		is(t, "TESTING_CHECK_SAME_FAULT")(svc.CheckEnd(ctx, "s1", "c1", result, "same", true, 0))
		saved, _ := svc.store.Checks("run_s1")
		if saved[0].State != CheckRunning || saved[0].SameFault {
			t.Fatalf("refused result persisted: %+v", saved[0])
		}
	}
}

func TestCheckEvidenceUsesOnlyScreenshotsOnTheChecksLeg(t *testing.T) {
	for _, leg := range []string{"head", "base"} {
		for _, result := range []CheckState{CheckPass, CheckFail, CheckPartial, CheckAbsent, CheckSkipped} {
			c := Check{Leg: leg, Receipts: []Receipt{{Leg: leg, Action: "click", AfterPath: evidenceFile(t)}}}
			if state, note, err := checkEvidence(c, result, "observed"); err != nil || state != result || note != "observed" {
				t.Fatalf("%s %s: state=%s note=%q err=%v", leg, result, state, note, err)
			}
			c.Receipts[0].Leg = map[string]string{"head": "base", "base": "head"}[leg]
			_, _, err := checkEvidence(c, result, "observed")
			if (err != nil) != (result != CheckSkipped) {
				t.Fatalf("%s %s with the other leg's screenshot: %v", leg, result, err)
			}
		}
	}
}
