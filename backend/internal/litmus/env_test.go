package litmus

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func daemonSecretEnv(t *testing.T) []string {
	t.Helper()
	keys := []string{
		"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GH_CONFIG_DIR",
		"SSH_AUTH_SOCK", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
		"AZURE_CLIENT_SECRET", "GOOGLE_APPLICATION_CREDENTIALS", "NPM_TOKEN", "NPM_CONFIG_USERCONFIG", "npm_config_userconfig",
		"NODE_OPTIONS", "LD_PRELOAD", "DYLD_INSERT_LIBRARIES", "NEW_PROVIDER_SECRET",
	}
	for _, key := range keys {
		t.Setenv(key, "private-daemon-value")
	}
	return keys
}

func requireNoDaemonSecrets(t *testing.T, env, keys []string) {
	t.Helper()
	for _, key := range keys {
		if slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, key+"=") }) {
			t.Errorf("daemon variable %s reached PR-controlled code", key)
		}
	}
}

func TestPrepareBaseEnvAllowlistKeepsEssentialsAndPinnedNode(t *testing.T) {
	keys := daemonSecretEnv(t)
	for key, value := range map[string]string{
		"PATH": "/system/bin", "HOME": t.TempDir(), "TMPDIR": t.TempDir(), "LANG": "en_US.UTF-8",
		"AO_PORT": "3001", "AO_DATA_DIR": "/real/ao", "ELECTRON_RUN_AS_NODE": "1", "GIT_TERMINAL_PROMPT": "1",
	} {
		t.Setenv(key, value)
	}
	nodeDir := filepath.Join(t.TempDir(), "node", "bin")
	env := baseEnv(nodeDir)
	requireNoDaemonSecrets(t, env, append(keys, "AO_PORT", "AO_DATA_DIR", "ELECTRON_RUN_AS_NODE"))
	for key, want := range map[string]string{
		"PATH": nodeDir + string(os.PathListSeparator) + "/system/bin", "HOME": os.Getenv("HOME"),
		"TMPDIR": os.Getenv("TMPDIR"), "LANG": "en_US.UTF-8", "GIT_TERMINAL_PROMPT": "0",
	} {
		if envValue(env, key) != want {
			t.Errorf("%s did not retain its expected process/toolchain value", key)
		}
	}
	t.Setenv("PATH", "")
	if envValue(baseEnv(nodeDir), "PATH") != nodeDir {
		t.Fatal("pinned Node must remain on an otherwise empty PATH without adding the current directory")
	}
}

// scriptEnvRunner captures the environments actually passed to install/build scripts.
type scriptEnvRunner struct {
	Runner
	envs [][]string
	args [][]string
	dirs []string
}

func (r *scriptEnvRunner) Run(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	if name == "sh" {
		r.envs = append(r.envs, slices.Clone(env))
		r.args = append(r.args, slices.Clone(args))
		r.dirs = append(r.dirs, dir)
	}
	return r.Runner.Run(ctx, dir, env, name, args...)
}

func TestPrepareDoesNotInheritDaemonSecrets(t *testing.T) {
	keys := daemonSecretEnv(t)
	t.Setenv("PATH", "/system/bin")
	svc, fake := prepSvc(t, map[string]string{"v24": "v24.21.0"})
	runner := &scriptEnvRunner{Runner: fake}
	svc.runner = runner
	r, err := ParseRecipe(defaultRecipe)
	is(t, "")(nil, err)
	r.Env = map[string]string{"FEATURE_FLAG": "recipe-value", "GOMAXPROCS": "2", "GOCACHE": filepath.Join(t.TempDir(), "go-cache")}
	leg, err := svc.prepareLeg(ctx, DefaultRepo, r, "head", strings.Repeat("a", 40))
	is(t, "")(nil, err)
	if len(runner.envs) != 2 {
		t.Fatalf("captured %d script environments, want install and build", len(runner.envs))
	}
	nodeDir := filepath.Dir(readManifest(leg.Worktree).NodePath)

	legDir := filepath.Join(t.TempDir(), "head")
	appEnv, err := legEnv(r, Run{ID: "run_s1", PRNumber: 7}, "head", legDir, leg.Worktree, nodeDir, 3200, 9400, "/private/tmp/lt-fixture")
	is(t, "")(nil, err)
	requireNoDaemonSecrets(t, appEnv, slices.DeleteFunc(slices.Clone(keys), func(k string) bool { return k == "NODE_OPTIONS" }))
	if envValue(appEnv, "NODE_OPTIONS") != "--require "+filepath.Join(legDir, windowLabelScriptName) {
		t.Fatal("launch inherited daemon Node options")
	}
	for key, want := range r.Env {
		if envValue(appEnv, key) != want {
			t.Errorf("app lost recipe variable %s", key)
		}
	}

	for _, env := range runner.envs {
		// Install and build retain the owner HOME for toolchains.
		if envValue(env, "HOME") != os.Getenv("HOME") {
			t.Error("prepare changed HOME")
		}
		// They receive only the allowlisted environment and recipe values.
		requireNoDaemonSecrets(t, env, keys)
		for key, want := range r.Env {
			if envValue(env, key) != want {
				t.Errorf("explicit recipe value %s did not reach install/build", key)
			}
		}
		if !strings.HasPrefix(envValue(env, "PATH"), nodeDir+string(os.PathListSeparator)) {
			t.Error("install/build did not receive the pinned Node PATH")
		}
	}
	for _, env := range runner.envs {
		if envValue(env, "NODE_OPTIONS") != "" {
			t.Error("script inherited the daemon's Node startup options")
		}
	}
}

func TestPrepareTrustedGitEnvRetainsDaemonAuthentication(t *testing.T) {
	keys := []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GH_CONFIG_DIR", "SSH_AUTH_SOCK"}
	for _, key := range keys {
		t.Setenv(key, "private-git-value")
	}
	t.Setenv("ELECTRON_RUN_AS_NODE", "1")
	env := gitEnv()
	for _, key := range keys {
		if envValue(env, key) != "private-git-value" {
			t.Errorf("trusted Git operation lost its %s authentication setting", key)
		}
	}
	if envValue(env, "ELECTRON_RUN_AS_NODE") != "" || envValue(env, "GIT_TERMINAL_PROMPT") != "0" {
		t.Error("trusted Git environment lost its noninteractive or native-process settings")
	}
}
