package crush

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNativeDataRootPlatformDefaultsAndOverrides(t *testing.T) {
	home := t.TempDir()
	for _, tc := range []struct {
		name, platform string
		env            map[string]string
		want           string
	}{
		{"unix", "linux", nil, filepath.Join(home, ".local", "share", "crush")},
		{"windows local appdata", "windows", map[string]string{"LOCALAPPDATA": filepath.Join(home, "local")}, filepath.Join(home, "local", "crush")},
		{"windows profile fallback", "windows", map[string]string{"USERPROFILE": home}, filepath.Join(home, "AppData", "Local", "crush")},
		{"windows home fallback", "windows", nil, filepath.Join(home, "AppData", "Local", "crush")},
		{"xdg override", "windows", map[string]string{"XDG_DATA_HOME": filepath.Join(home, "xdg")}, filepath.Join(home, "xdg", "crush")},
		{"explicit override", "windows", map[string]string{"CRUSH_GLOBAL_DATA": filepath.Join(home, "global"), "XDG_DATA_HOME": "ignored"}, filepath.Join(home, "global")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := NativeDataRoot(home, tc.platform, func(key string) string { return tc.env[key] })
			if got != tc.want {
				t.Fatalf("root = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestImportedRestorePinsDataDirectoryAcrossConfigAndAdoption(t *testing.T) {
	workspace := t.TempDir()
	for _, name := range []string{".crushrc", "crushrc"} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(`options.data_directory = "/original/store"`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, imported := range []bool{false, true} {
		for _, adopted := range []bool{false, true} {
			metadata := map[string]string{ports.MetadataKeyAgentSessionID: "12345678-1234-1234-1234-123456789012"}
			if imported {
				metadata[ports.MetadataKeyImportSourceNativeID] = metadata[ports.MetadataKeyAgentSessionID]
				if adopted {
					metadata[ports.MetadataKeyImportResumeNativeID] = metadata[ports.MetadataKeyAgentSessionID]
				}
			}
			p := &Plugin{resolvedBinary: "crush"}
			cmd, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{Session: ports.SessionRef{WorkspacePath: workspace, Metadata: metadata}})
			if err != nil || !ok {
				t.Fatalf("restore = %v, %v, %v", cmd, ok, err)
			}
			if contains(cmd, "--data-dir") != imported || (imported && !containsSubsequence(cmd, []string{"--data-dir", filepath.Join(workspace, ".crush")})) {
				t.Fatalf("imported=%v adopted=%v lost data-directory pin: %v", imported, adopted, cmd)
			}
		}
	}
}
