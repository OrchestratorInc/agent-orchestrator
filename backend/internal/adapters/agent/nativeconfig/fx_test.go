package nativeconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFXHomeExpandsInvocationPaths(t *testing.T) {
	home := t.TempDir()
	t.Chdir(t.TempDir())
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FX_HOME", "~/inherited")
	t.Setenv("FX_TEST_STATE", filepath.Join(home, "ambient"))
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"inherited tilde", nil, filepath.Join(home, "inherited")},
		{"explicit clear", map[string]string{"FX_HOME": ""}, filepath.Join(home, ".fx")},
		{"tilde", map[string]string{"FX_HOME": "~/custom"}, filepath.Join(home, "custom")},
		{"variable", map[string]string{"FX_HOME": "$FX_TEST_STATE/fx"}, filepath.Join(home, "ambient", "fx")},
		{"child variable wins", map[string]string{"FX_HOME": "${FX_TEST_STATE}/fx", "FX_TEST_STATE": filepath.Join(home, "child")}, filepath.Join(home, "child", "fx")},
		{"child HOME", map[string]string{"FX_HOME": "$HOME/fx", "HOME": filepath.Join(home, "child")}, filepath.Join(home, "child", "fx")},
		{"relative", map[string]string{"FX_HOME": "profiles/fx"}, filepath.Join(wd, "profiles", "fx")},
		{"absolute", map[string]string{"FX_HOME": filepath.Join(home, "absolute")}, filepath.Join(home, "absolute")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FXHome(tc.env, home)
			if err != nil || got != tc.want {
				t.Fatalf("home = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	if _, err := FXHome(map[string]string{"FX_HOME": "$FX_TEST_STATE/fx", "FX_TEST_STATE": ""}, home); err == nil {
		t.Fatal("an explicitly cleared variable fell back to the daemon value")
	}
	for _, value := range []string{"~", filepath.VolumeName(home) + string(filepath.Separator)} {
		if _, err := FXHome(map[string]string{"FX_HOME": value}, home); err == nil {
			t.Fatalf("accepted a profile root native FX refuses: %q", value)
		}
	}
}
