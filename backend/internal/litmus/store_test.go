package litmus

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTestingStoreRoundTripIsAtomic(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	run := Run{Kind: "pr", ID: "run_abc", SessionID: "s1", State: RunIntake, Deadline: time.Unix(910, 0).UTC()}
	for _, state := range []RunState{RunIntake, RunPrepared} {
		run.State = state
		is(t, "")(nil, st.Save(run))
	}
	if got, err := st.Get(run.ID); err != nil || got.State != RunPrepared || got.Kind != "pr" || !got.Deadline.Equal(run.Deadline) {
		t.Fatalf("got %+v err %v", got, err)
	}
	is(t, "")(nil, st.WriteFile(run.ID, "actions.jsonl", []byte("{\"seq\":1,\"leg\":\"base\",\"action\":\"click\"}\n")))
	if receipts, err := st.Receipts(run.ID); err != nil || len(receipts) != 1 || receipts[0].Seq != 1 || receipts[0].Leg != "base" {
		t.Fatalf("receipts %+v err %v", receipts, err)
	}
	if tmp, _ := filepath.Glob(filepath.Join(dir, "litmus", "runs", run.ID, ".tmp-*")); len(tmp) != 0 {
		t.Fatalf("temp files left: %v", tmp)
	}
	if runs, err := st.List(); err != nil || len(runs) != 1 {
		t.Fatalf("list %+v err %v", runs, err)
	}
	if _, err := st.Get("run_none"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing run err %v", err)
	}
	if st.Save(Run{ID: "../escape"}) == nil || st.WriteFile(run.ID, "../x", nil) == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestSavingRunPreservesPackAndUpdatesDeadlineAndTargetPorts(t *testing.T) {
	svc, _ := newSvc(t)
	run, err := svc.Intake(ctx, "s1", prURL)
	is(t, "")(nil, err)
	pack := readRunFile(t, svc, run, "context-pack.md")
	run.Deadline = run.Deadline.Add(10 * time.Minute)
	run.State = RunRunning
	run.Legs = []Leg{{Name: "head", State: "running", DaemonPort: 3210, DebugPort: 9210}}
	is(t, "")(nil, svc.store.Save(run))
	if got := readRunFile(t, svc, run, "context-pack.md"); got != pack {
		t.Fatalf("saving run rewrote its intake pack: %s", got)
	}
	saved, err := svc.store.Get(run.ID)
	is(t, "")(nil, err)
	if !saved.Deadline.Equal(run.Deadline) {
		t.Fatalf("saved deadline = %s, want %s", saved.Deadline, run.Deadline)
	}
	var target struct{ Legs []targetLeg }
	is(t, "")(nil, svc.store.read(run.ID, "target.json", &target))
	if len(target.Legs) != 2 || target.Legs[0].Name != "head" || target.Legs[0].DaemonPort != 3210 || target.Legs[0].DebugPort != 9210 || target.Legs[0].State != "running" {
		t.Fatalf("target ports did not follow the saved run: %+v", target)
	}
}

func TestSavingRunPreservesLegacyPackForTitleBackfill(t *testing.T) {
	svc, _ := newSvc(t)
	run, err := svc.Intake(ctx, "s1", prURL)
	is(t, "")(nil, err)
	legacy := "- #6394 legacy title\n"
	is(t, "")(nil, svc.store.WriteFile(run.ID, "context-pack.md", []byte(legacy)))
	run.Title, run.State = "", RunSubmitted
	is(t, "")(nil, svc.store.Save(run))
	if got := readRunFile(t, svc, run, "context-pack.md"); got != legacy {
		t.Fatalf("saving run replaced legacy title source: %q", got)
	}
	if got := svc.summarize(run); got.PR.Title != "legacy title" {
		t.Fatalf("legacy title backfill = %q", got.PR.Title)
	}
}

func TestSavingRunDoesNotReadOrCreateContextPack(t *testing.T) {
	store := NewStore(t.TempDir())
	run := Run{ID: "run_pack", Kind: "pr", Repo: "acme/widget", PRNumber: 1, BaseSHA: base6394, HeadSHA: head6394}
	is(t, "")(nil, store.Save(run))
	path, err := store.path(run.ID, "context-pack.md")
	is(t, "")(nil, err)
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Save created a pack: %v", err)
	}
	is(t, "")(nil, os.Mkdir(path, 0o750))
	is(t, "")(nil, store.Save(run))
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("Save touched the unreadable pack: %v", err)
	}
}

func TestContextPackOmitsFilesThatDoNotExist(t *testing.T) {
	store := NewStore(t.TempDir())
	run := Run{ID: "run_pack", Kind: "pr", Repo: "acme/widget", PRNumber: 1}
	is(t, "")(nil, store.Save(run))
	is(t, "")(nil, store.writeContextPack(run))
	pack := readRunFile(t, New(store, nil), run, "context-pack.md")
	if strings.Contains(pack, "- target.json:") || !strings.Contains(pack, "- run.json:") {
		t.Fatalf("pack lists a missing target or omits the run: %s", pack)
	}
}
