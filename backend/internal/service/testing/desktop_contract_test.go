package testing

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestFullFrameAndDeliveryPolicyReachInput(t *testing.T) {
	f := newFixture(t, func(deps *Deps) {
		deps.Desktop = &policyDesktop{fakeProviders: deps.Desktop.(*fakeProviders), mode: "foreground", gap: "declared window recording gap"}
	})
	for _, tool := range []string{"click", "type", "key"} {
		shot, err := f.call("shot-"+tool, "screenshot", domain.TestScreenshotRequest{})
		if err != nil {
			t.Fatal(err)
		}
		frame := shot.Screenshot.Frame
		var input any
		switch tool {
		case "click":
			input = domain.TestClickRequest{ScreenshotID: frame.ScreenshotID, X: 1, Y: 1}
		case "type":
			input = domain.TestTypeRequest{ScreenshotID: frame.ScreenshotID, Text: "hello"}
		case "key":
			input = domain.TestKeyRequest{ScreenshotID: frame.ScreenshotID, Keys: []string{"Enter"}}
		}
		if _, err := f.call("input-"+tool, tool, input); err != nil {
			t.Fatal(err)
		}
		got := f.provider.inputFrames[len(f.provider.inputFrames)-1]
		if frame.CaptureHandle == "" || frame.Scale != 2 || !reflect.DeepEqual(got, frame) {
			t.Fatalf("%s lost the adapter frame: %+v", tool, got)
		}
	}
	dir := filepath.Join(f.dir, "testing", string(f.run.ID), string(f.start.AttemptID))
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(dir, file.Name()))
		if err != nil || bytes.Contains(data, []byte("private-capture-receipt")) {
			t.Fatal("private capture receipt reached evidence", file.Name(), err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "actions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var record domain.TestActionRecord
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		if record.Tool != "click" && record.Tool != "type" && record.Tool != "key" {
			continue
		}
		want := "foreground"
		if record.Tool == "click" {
			want = "background"
		}
		if record.ConfiguredDeliveryMode != "foreground" || record.DeliveryMode != want {
			t.Fatalf("input policy missing from %s journal: %+v", record.State, record)
		}
		counts[record.Tool]++
	}
	for _, tool := range []string{"click", "type", "key"} {
		if counts[tool] != 2 {
			t.Fatal("input missing journal pair", tool, counts)
		}
	}
	var recipe Recipe
	if err := json.Unmarshal([]byte(f.run.RecipeSnapshot), &recipe); err != nil || recipe.DeliveryMode != "foreground" {
		t.Fatal("recipe did not retain delivery policy", err)
	}
}

func TestDeclaredRecordingGapPersistsAndReleasesBeforeTargetStop(t *testing.T) {
	var desktop *policyDesktop
	f := newFixture(t, func(deps *Deps) {
		desktop = &policyDesktop{fakeProviders: deps.Desktop.(*fakeProviders), mode: "background", gap: "main display recording is refused"}
		deps.Desktop = desktop
	})
	rec, _, err := f.store.GetTestAttempt(context.Background(), f.start.AttemptID)
	if err != nil || rec.RecordingGap != desktop.gap || desktop.starts != 1 {
		t.Fatal("provider recording gap was not saved", rec.RecordingGap, err)
	}
	if _, err := f.svc.Cancel(context.Background(), f.start.AttemptID); err != nil {
		t.Fatal(err)
	}
	f.wait(t)
	if !reflect.DeepEqual(f.provider.cleanupEvents, []string{"desktop_release", "target_stop"}) {
		t.Fatal("gap provider was recorded or released after target stop", f.provider.cleanupEvents)
	}
	journal, err := os.ReadFile(filepath.Join(f.dir, "testing", string(f.run.ID), string(f.start.AttemptID), "actions.jsonl"))
	if err != nil || !bytes.Contains(journal, []byte(`"recordingGap":"main display recording is refused"`)) {
		t.Fatal("declared recording gap missing from journal", err)
	}
}

func TestRecordingJournalFailurePreventsWorkerLaunch(t *testing.T) {
	f := newFixture(t)
	_, _ = f.svc.Cancel(context.Background(), f.start.AttemptID)
	f.wait(t)
	f.evidence.failState = "dispatching"
	result, err := f.svc.StartAttempt(context.Background(), f.run.ID, StartAttemptInput{WorkerPrompt: "Investigate", Timeout: time.Minute})
	if code(err) != "TEST_EVIDENCE_WRITE_FAILED" || result.AttemptID == "" || f.worker.launches != 1 {
		t.Fatal("recording evidence failure launched a worker", result, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := f.svc.WaitCleanup(ctx, result.AttemptID); err != nil {
		t.Fatal(err)
	}
}

func TestTargetLaunchReceivesRevisionAndFixtureSnapshot(t *testing.T) {
	f := newFixture(t, func(deps *Deps) {
		recipe := deps.Recipes["native"]
		recipe.VisualMarker = true
		deps.Recipes["native"] = recipe
	})
	spec := f.provider.launchSpecs[0]
	var fixture struct {
		VisualMarker bool `json:"visualMarker"`
	}
	if err := json.Unmarshal([]byte(spec.RecipeSnapshot), &fixture); err != nil || !fixture.VisualMarker {
		t.Fatal("fixture flag missing from target snapshot", err)
	}
	rec, _, err := f.store.GetTestAttempt(context.Background(), f.start.AttemptID)
	if err != nil || spec.AttemptID != rec.ID || spec.Generation != rec.LeaseGeneration || spec.CommitSHA != f.run.CommitSHA || spec.RecipeSnapshot != f.run.RecipeSnapshot || spec.CheckoutPath != f.dir || spec.StateRoot != filepath.Join(f.deps.TargetStateRoot, string(rec.ID)) || !spec.Deadline.Equal(rec.Deadline) {
		t.Fatal("target launch spec did not retain the run and attempt", err)
	}
}
