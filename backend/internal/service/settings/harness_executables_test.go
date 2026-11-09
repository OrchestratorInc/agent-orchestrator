package settings

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestExecutablePatchPreservesModelsAndFailedSelection 验证落盘与发布的顺序，拒绝失败的探测或写入。
func TestExecutablePatchPreservesModelsAndFailedSelection(t *testing.T) {
	spec := binaryutil.RegisterSpec("deepseek-harness", binaryutil.BinarySpec{Names: []string{"dsh"}, WinNames: []string{"dsh.cmd"}})
	dir := t.TempDir()
	path := filepath.Join(dir, binaryutil.CandidateNames(spec)[0])
	body := "#!/bin/sh\necho 0.2.0-test\n"
	if runtime.GOOS == "windows" {
		body = "@echo off\r\necho 0.2.0-test\r\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { binaryutil.SetSelectedBinaries(nil) })
	store := &harnessSettingsStore{defaults: make(map[string]domain.HarnessDefault)}
	svc := New(store, nil, Offering{}, nil)
	changed := 0
	svc.SetHarnessExecutableChanged(func(h domain.AgentHarness) {
		if h != domain.HarnessDeepSeek {
			t.Fatalf("changed harness %s", h)
		}
		changed++
	})
	models := []string{"hidden-model"}
	if _, err := svc.PatchHarnessDefault(t.Context(), domain.HarnessDeepSeek, HarnessDefaultPatch{DisabledModels: &models}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PatchHarnessDefault(t.Context(), domain.HarnessDeepSeek, HarnessDefaultPatch{ExecutableDirectory: &dir, ExecutablePath: &path}); err != nil {
		t.Fatal(err)
	}
	if changed != 1 || binaryutil.SelectedPath("deepseek-harness") != path {
		t.Fatalf("published path=%q, changes=%d", binaryutil.SelectedPath("deepseek-harness"), changed)
	}
	got := harnessDefault(t, svc, domain.HarnessDeepSeek)
	if !reflect.DeepEqual(got.DisabledModels, models) {
		t.Fatalf("models lost: %+v", got)
	}
	models = nil
	if _, err := svc.PatchHarnessDefault(t.Context(), domain.HarnessDeepSeek, HarnessDefaultPatch{DisabledModels: &models}); err != nil {
		t.Fatal(err)
	}
	if got := harnessDefault(t, svc, domain.HarnessDeepSeek); got.ExecutablePath != path || changed != 1 {
		t.Fatalf("model save changed executable: %+v", got)
	}
	missing := filepath.Join(dir, "missing", filepath.Base(path))
	if _, err := svc.PatchHarnessDefault(t.Context(), domain.HarnessDeepSeek, HarnessDefaultPatch{ExecutablePath: &missing}); !errors.Is(err, ErrInvalidHarnessDefault) {
		t.Fatalf("missing selection: %v", err)
	}
	if got := harnessDefault(t, svc, domain.HarnessDeepSeek); got.ExecutablePath != path || changed != 1 {
		t.Fatalf("failed probe changed executable: %+v", got)
	}
	binaryutil.SetSelectedBinaries(nil)
	if err := New(store, nil, Offering{}, nil).LoadHarnessExecutables(t.Context()); err != nil {
		t.Fatal(err)
	}
	if binaryutil.SelectedPath("deepseek-harness") != path {
		t.Fatal("restart lost executable")
	}
	empty := ""
	store.err = errors.New("write failed")
	if _, err := svc.PatchHarnessDefault(t.Context(), domain.HarnessDeepSeek, HarnessDefaultPatch{ExecutablePath: &empty}); err == nil {
		t.Fatal("save failure ignored")
	}
	if binaryutil.SelectedPath("deepseek-harness") != path || changed != 1 {
		t.Fatal("failed save changed runtime selection")
	}
	store.err = nil
	if _, err := svc.PatchHarnessDefault(t.Context(), domain.HarnessDeepSeek, HarnessDefaultPatch{ExecutablePath: &empty}); err != nil {
		t.Fatal(err)
	}
	if binaryutil.SelectedPath("deepseek-harness") != "" || changed != 2 || !harnessDefault(t, svc, domain.HarnessDeepSeek).IsZero() {
		t.Fatal("reset did not restore automatic detection")
	}
}
