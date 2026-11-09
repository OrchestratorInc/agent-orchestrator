package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/deepseekharness"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	settingssvc "github.com/aoagents/agent-orchestrator/backend/internal/service/settings"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// TestExecutableSelectionRefreshesInstancesAndRestores 从 SQLite 设置装配验证跨实例刷新与重启恢复。
func TestExecutableSelectionRefreshesInstancesAndRestores(t *testing.T) {
	store := sqlitetest.MustOpen(t)
	svc := settingssvc.New(settingsStore{store: store}, nil, settingssvc.Offering{}, nil)
	first, second := deepseekharness.New(), deepseekharness.New()
	svc.SetHarnessExecutableChanged(func(harness domain.AgentHarness) {
		invalidateHarnessBinary(&installationInvalidations{}, string(harness),
			pluginBinaryCache{harness: harness, plugin: first}, pluginBinaryCache{harness: harness, plugin: second})
	})
	t.Cleanup(func() { binaryutil.SetSelectedBinaries(nil) })
	var selected string
	for range 2 {
		dir := t.TempDir()
		name, script := "dsh", "#!/bin/sh\necho 0.2.0-test\n"
		if runtime.GOOS == "windows" {
			name, script = "dsh.cmd", "@echo off\r\necho 0.2.0-test\r\n"
		}
		selected = filepath.Join(dir, name)
		if err := os.WriteFile(selected, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.PatchHarnessDefault(t.Context(), domain.HarnessDeepSeek, settingssvc.HarnessDefaultPatch{ExecutableDirectory: &dir, ExecutablePath: &selected}); err != nil {
			t.Fatal(err)
		}
		for _, plugin := range []*deepseekharness.Plugin{first, second} {
			if got, err := plugin.ResolveBinary(t.Context()); err != nil || got != selected {
				t.Fatalf("instance retained old selection: %q, %v", got, err)
			}
		}
	}
	binaryutil.SetSelectedBinaries(nil)
	restarted := settingssvc.New(settingsStore{store: store}, nil, settingssvc.Offering{}, nil)
	if err := restarted.LoadHarnessExecutables(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got, err := deepseekharness.New().ResolveBinary(t.Context()); err != nil || got != selected {
		t.Fatalf("restart did not restore selection: %q, %v", got, err)
	}
	if err := os.Remove(selected); err != nil {
		t.Fatal(err)
	}
	if _, err := deepseekharness.New().ResolveBinary(t.Context()); err == nil {
		t.Fatal("missing persisted selection fell back to another CLI")
	}
}
