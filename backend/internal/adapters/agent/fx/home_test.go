package fx

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestFXImportRestorePinsExpandedHome(t *testing.T) {
	home := t.TempDir()
	t.Chdir(t.TempDir())
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FX_HOME", "")
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{"~/state", filepath.Join(home, "state")},
		{"${FX_STATE}/state", filepath.Join(home, "state")},
		{"profiles/fx", filepath.Join(wd, "profiles", "fx")},
		{" ", filepath.Join(home, ".fx")},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			env := map[string]string{"HOME": home, "FX_HOME": tc.raw, "FX_STATE": home}
			p := &Plugin{resolvedBinary: "fx"}
			root, err := p.NativeSessionConfigDir(t.Context(), env)
			if err != nil || root != tc.want {
				t.Fatalf("native root = %q, %v; want %q", root, err, tc.want)
			}
			cmd, err := p.GetLaunchCommand(t.Context(), ports.LaunchConfig{Env: env, WorkspacePath: t.TempDir()})
			if err != nil || !reflect.DeepEqual(cmd, []string{"fx"}) {
				t.Fatalf("launch = %q, %v", cmd, err)
			}
			cmd, ok, err := p.GetRestoreCommand(t.Context(), ports.RestoreConfig{Env: env, Session: ports.SessionRef{WorkspacePath: t.TempDir(), Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "native", ports.MetadataKeyImportSourceNativeID: "source"}}})
			if err != nil || !ok || !reflect.DeepEqual(cmd, []string{"env", "FX_HOME=" + root, "fx", "--resume", "native"}) {
				t.Fatalf("restore = %q, %v, %v", cmd, ok, err)
			}
			if env["FX_HOME"] != tc.raw {
				t.Fatal("launch overrides were mutated")
			}
		})
	}
}

func TestOrdinaryFXLaunchAndRestoreLeaveNativeHomeResolutionUntouched(t *testing.T) {
	t.Setenv("AO_TEST_FX_UNSET", "")
	for _, value := range []string{"$AO_TEST_FX_UNSET/state", "~another-user/state", "profiles/fx", ""} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("FX_HOME", value)
			env := map[string]string{"FX_HOME": value}
			p := &Plugin{resolvedBinary: "fx"}
			cmd, err := p.GetLaunchCommand(t.Context(), ports.LaunchConfig{WorkspacePath: t.TempDir(), Env: env})
			if err != nil || !reflect.DeepEqual(cmd, []string{"fx"}) {
				t.Fatalf("ordinary launch=%q err=%v", cmd, err)
			}
			cmd, ok, err := p.GetRestoreCommand(t.Context(), ports.RestoreConfig{Env: env, Session: ports.SessionRef{WorkspacePath: t.TempDir(), Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "native"}}})
			if err != nil || !ok || !reflect.DeepEqual(cmd, []string{"fx", "--resume", "native"}) || env["FX_HOME"] != value {
				t.Fatalf("ordinary restore=%q ok=%v err=%v", cmd, ok, err)
			}
		})
	}
}
