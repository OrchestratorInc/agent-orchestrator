package runner

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadStateAcceptsPrivateLoopbackConfiguration(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	chmodPrivateDir(t, root)
	authDir := filepath.Join(root, "auth")
	if err := os.Mkdir(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writePrivateFile(t, filepath.Join(root, "control.key"), "control-secret\n")
	writePrivateFile(t, filepath.Join(root, "management.key"), "management-secret\n")
	writePrivateFile(t, filepath.Join(root, "routing.key"), strings.Repeat("11", 32)+"\n")
	writePrivateFile(t, filepath.Join(root, "config.yaml"), strings.Join([]string{
		"host: 127.0.0.1",
		"port: 43127",
		"auth-dir: " + authDir,
		"api-keys:",
		"  - client-secret",
		"remote-management:",
		"  allow-remote: false",
		"  secret-key: ''",
		"  disable-control-panel: true",
		"  disable-auto-update-panel: true",
		"plugins:",
		"  enabled: false",
		"pprof:",
		"  enable: false",
		"discovery:",
		"  enabled: false",
		"request-log: false",
		"logging-to-file: false",
		"usage-statistics-enabled: false",
	}, "\n")+"\n")

	state, err := LoadState(root)
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if state.ControlKey != "control-secret" {
		t.Fatalf("control key was not loaded")
	}
	if state.ManagementKey != "management-secret" {
		t.Fatalf("management key was not loaded")
	}
	if len(state.RoutingKey) != 32 {
		t.Fatalf("routing key length = %d, want 32", len(state.RoutingKey))
	}
	if state.Config.Host != "127.0.0.1" || state.Config.Port != 43127 {
		t.Fatalf("unexpected listener: %s:%d", state.Config.Host, state.Config.Port)
	}
	if state.Config.AuthDir != authDir {
		t.Fatalf("auth dir = %q, want %q", state.Config.AuthDir, authDir)
	}
	if len(state.Config.APIKeys) != 1 || state.Config.APIKeys[0] != "client-secret" {
		t.Fatalf("client key was not loaded")
	}
}

func TestLoadStateRejectsUnsafeManagementKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(t *testing.T, path string)
		want   string
	}{
		{
			name: "missing",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			},
			want: "management key",
		},
		{
			name: "empty",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "empty",
		},
		{
			name: "permissive",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				if err := os.Chmod(path, 0o640); err != nil {
					t.Fatal(err)
				}
			},
			want: "permissions",
		},
		{
			name: "replaced by directory",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			},
			want: "regular",
		},
	}
	if runtime.GOOS != "windows" {
		tests = append(tests, struct {
			name   string
			mutate func(t *testing.T, path string)
			want   string
		}{
			name: "symlink",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				target := filepath.Join(filepath.Dir(path), "real-management.key")
				writePrivateFile(t, target, "management-secret\n")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			},
			want: "regular",
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, _ := validStateFixture(t)
			path := filepath.Join(root, "management.key")
			tt.mutate(t, path)
			_, err := LoadState(root)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tt.want) {
				t.Fatalf("LoadState() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestLoadStateRejectsUnsafeConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(root, authDir string)
		want   string
	}{
		{
			name: "non loopback host",
			mutate: func(root, _ string) {
				replaceInFile(t, filepath.Join(root, "config.yaml"), "host: 127.0.0.1", "host: 0.0.0.0")
			},
			want: "loopback",
		},
		{
			name: "wrong auth directory",
			mutate: func(root, authDir string) {
				replaceInFile(t, filepath.Join(root, "config.yaml"), "auth-dir: "+authDir, "auth-dir: "+filepath.Join(root, "other"))
			},
			want: "auth directory",
		},
		{
			name: "management enabled",
			mutate: func(root, _ string) {
				replaceInFile(t, filepath.Join(root, "config.yaml"), "allow-remote: false", "allow-remote: true")
			},
			want: "management",
		},
		{
			name: "plugins enabled",
			mutate: func(root, _ string) {
				replaceInFile(t, filepath.Join(root, "config.yaml"), "plugins:\n  enabled: false", "plugins:\n  enabled: true")
			},
			want: "plugins",
		},
		{
			name: "profiling enabled",
			mutate: func(root, _ string) {
				replaceInFile(t, filepath.Join(root, "config.yaml"), "pprof:\n  enable: false", "pprof:\n  enable: true")
			},
			want: "pprof",
		},
		{
			name: "discovery enabled",
			mutate: func(root, _ string) {
				replaceInFile(t, filepath.Join(root, "config.yaml"), "discovery:\n  enabled: false", "discovery:\n  enabled: true")
			},
			want: "discovery",
		},
		{
			name: "request logging enabled",
			mutate: func(root, _ string) {
				replaceInFile(t, filepath.Join(root, "config.yaml"), "request-log: false", "request-log: true")
			},
			want: "request logging",
		},
		{
			name: "file logging enabled",
			mutate: func(root, _ string) {
				replaceInFile(t, filepath.Join(root, "config.yaml"), "logging-to-file: false", "logging-to-file: true")
			},
			want: "file logging",
		},
		{
			name: "usage statistics enabled",
			mutate: func(root, _ string) {
				replaceInFile(t, filepath.Join(root, "config.yaml"), "usage-statistics-enabled: false", "usage-statistics-enabled: true")
			},
			want: "usage statistics",
		},
		{
			name: "missing client key",
			mutate: func(root, _ string) {
				replaceInFile(t, filepath.Join(root, "config.yaml"), "  - client-secret", "  - ''")
			},
			want: "client key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, authDir := validStateFixture(t)
			tt.mutate(root, authDir)
			_, err := LoadState(root)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tt.want) {
				t.Fatalf("LoadState() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestLoadStateRejectsRelativeAndSymlinkedState(t *testing.T) {
	t.Parallel()

	if _, err := LoadState("relative/state"); err == nil {
		t.Fatal("LoadState() accepted a relative state directory")
	}

	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on Windows")
	}
	root, _ := validStateFixture(t)
	realKey := filepath.Join(root, "real-control.key")
	writePrivateFile(t, realKey, "control-secret\n")
	if err := os.Remove(filepath.Join(root, "control.key")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realKey, filepath.Join(root, "control.key")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(root); err == nil || !strings.Contains(strings.ToLower(err.Error()), "regular") {
		t.Fatalf("LoadState() error = %v, want non-regular-file rejection", err)
	}
}

func validStateFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	chmodPrivateDir(t, root)
	authDir := filepath.Join(root, "auth")
	if err := os.Mkdir(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writePrivateFile(t, filepath.Join(root, "control.key"), "control-secret\n")
	writePrivateFile(t, filepath.Join(root, "management.key"), "management-secret\n")
	writePrivateFile(t, filepath.Join(root, "routing.key"), strings.Repeat("11", 32)+"\n")
	writePrivateFile(t, filepath.Join(root, "config.yaml"), "host: 127.0.0.1\nport: 43127\nauth-dir: "+authDir+"\napi-keys:\n  - client-secret\nremote-management:\n  allow-remote: false\n  secret-key: ''\n  disable-control-panel: true\n  disable-auto-update-panel: true\nplugins:\n  enabled: false\npprof:\n  enable: false\ndiscovery:\n  enabled: false\nrequest-log: false\nlogging-to-file: false\nusage-statistics-enabled: false\n")
	return root, authDir
}

func chmodPrivateDir(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func writePrivateFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func replaceInFile(t *testing.T, path, old, replacement string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(b), old, replacement, 1)
	if updated == string(b) {
		t.Fatalf("fixture did not contain %q", old)
	}
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
}
