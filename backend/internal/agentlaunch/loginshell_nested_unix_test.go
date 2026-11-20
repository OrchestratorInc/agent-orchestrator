//go:build !windows

package agentlaunch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoginShellEnvUnwrapsForeignWrappers(t *testing.T) {
	for _, tc := range []struct {
		name     string
		user     bool
		layers   int
		poisoned bool
		clear    bool
	}{
		{name: "outer wrapper without user hooks", layers: 1},
		{name: "double nesting without user hooks", layers: 2},
		{name: "real user hooks survive double nesting", user: true, layers: 2},
		{name: "real user hooks survive triple nesting", user: true, layers: 3},
		{name: "saved outer wrapper is not a user hook", layers: 2, poisoned: true},
		{name: "explicit empty hooks override inherited hooks", user: true, layers: 2, clear: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "pin") + ":/usr/bin:/bin"
			userBash, userZsh := "", ""
			if tc.user {
				userBash = filepath.Join(root, "user", "bash_env")
				userZsh = filepath.Join(root, "user", "zsh")
			}
			inherited := map[string]string{"BASH_ENV": userBash, "ZDOTDIR": userZsh, "PROMPT_COMMAND": "history -a"}
			outer, err := LoginShellEnv(path, func(key string) string { return inherited[key] }, nil, filepath.Join(root, "outer"))
			if err != nil {
				t.Fatal(err)
			}
			inherited = outer
			if tc.poisoned {
				// Older nested daemons could already have saved a wrapper as
				// the user hook. There is no real user path left to recover.
				inherited[envUserBashEnv] = outer["BASH_ENV"]
				inherited[envUserZDOTDIR] = outer["ZDOTDIR"]
			}
			for layer := range tc.layers {
				var configured map[string]string
				if tc.clear && layer == 0 {
					configured = map[string]string{"BASH_ENV": "", "ZDOTDIR": ""}
					userBash, userZsh = "", ""
				}
				dataDir := filepath.Join(t.TempDir(), "inner")
				env, err := LoginShellEnv(path, func(key string) string { return inherited[key] }, configured, dataDir)
				if err != nil {
					t.Fatal(err)
				}
				if env[envUserBashEnv] != userBash || env[envUserZDOTDIR] != userZsh {
					t.Fatalf("layer %d saved hooks = %q, %q; want %q, %q", layer, env[envUserBashEnv], env[envUserZDOTDIR], userBash, userZsh)
				}
				if env["BASH_ENV"] != filepath.Join(dataDir, "runtime", "login-shell", "bash_env") || env["ZDOTDIR"] != filepath.Join(dataDir, "runtime", "login-shell") {
					t.Fatal("nested launch did not use its own wrappers")
				}
				if strings.Count(env["PROMPT_COMMAND"], repinPromptHook) != 1 {
					t.Fatal("nesting duplicated the prompt hook")
				}
				inherited = env
			}
		})
	}
}

func TestNestedLoginShellRunsWithoutWrapperRecursion(t *testing.T) {
	for _, shell := range []string{"/bin/bash", "/bin/zsh"} {
		for _, userHooks := range []bool{false, true} {
			name := filepath.Base(shell) + "/empty"
			if userHooks {
				name = filepath.Base(shell) + "/user-hooks"
			}
			t.Run(name, func(t *testing.T) {
				root, home := t.TempDir(), t.TempDir()
				user := map[string]string{}
				want := ""
				if userHooks {
					user["BASH_ENV"] = filepath.Join(home, "user_bash_env")
					user["ZDOTDIR"] = filepath.Join(home, "user_zsh")
					writeNestedShellFile(t, user["BASH_ENV"], "export RAN=\"${RAN}x\"\n")
					writeNestedShellFile(t, filepath.Join(user["ZDOTDIR"], ".zshenv"), "export RAN=\"${RAN}x\"\n")
					want = "x"
				}
				path := filepath.Join(root, "pin") + ":/usr/bin:/bin"
				for range 3 {
					env, err := LoginShellEnv(path, func(key string) string { return user[key] }, nil, t.TempDir())
					if err != nil {
						t.Fatal(err)
					}
					user = env
				}
				user["HOME"], user["PATH"] = home, path
				if got := runNestedLoginShell(t, shell, user, `printf '%s' "$RAN"`); got != want {
					t.Fatalf("nested hook output = %q, want %q", got, want)
				}
			})
		}
	}
}

func TestBashWrapperDoesNotRecursivelySourceOuterWrapper(t *testing.T) {
	for _, tc := range []struct {
		name  string
		outer bool
	}{
		{name: "saved outer wrapper", outer: true},
		{name: "empty saved value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outerDir := filepath.Join(t.TempDir(), "runtime", "login-shell")
			innerDir := filepath.Join(t.TempDir(), "runtime", "login-shell")
			for _, dir := range []string{outerDir, innerDir} {
				if err := writeLoginShellFiles(dir); err != nil {
					t.Fatal(err)
				}
			}
			saved := ""
			if tc.outer {
				saved = filepath.Join(outerDir, "bash_env")
			}
			// Bypass LoginShellEnv deliberately: this is the exact inherited
			// environment that crashed bash before the wrapper's own guard.
			env := map[string]string{
				"HOME":         t.TempDir(),
				"PATH":         "/usr/bin:/bin",
				"BASH_ENV":     filepath.Join(innerDir, "bash_env"),
				envUserBashEnv: saved,
			}
			if got := runNestedLoginShell(t, "/bin/bash", env, "printf ok", "--noprofile", "--norc"); got != "ok" {
				t.Fatalf("bash startup output = %q, want ok", got)
			}
		})
	}
}

func TestBashWrapperSourcesUserHookOncePerShell(t *testing.T) {
	root := t.TempDir()
	userHook := filepath.Join(root, "user_bash_env")
	// Re-enter the AO wrapper from the real user hook, then again from the
	// command. Neither should source the user hook for a second time.
	writeNestedShellFile(t, userHook, "export RAN=\"${RAN}x\"\n. \"$BASH_ENV\"\n")
	user := map[string]string{"BASH_ENV": userHook}
	env, err := LoginShellEnv(root+":/usr/bin:/bin", func(key string) string { return user[key] }, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env["HOME"], env["PATH"] = root, root+":/usr/bin:/bin"
	probe := `. "$BASH_ENV"; printf '%s|' "$RAN"; /bin/bash -c 'printf "%s" "$RAN"'; :`
	if got := runNestedLoginShell(t, "/bin/bash", env, probe); got != "x|xx" {
		t.Fatalf("re-entry/child hook output = %q, want x|xx", got)
	}
}

func TestZshWrappersGuardEachStartupFile(t *testing.T) {
	root := t.TempDir()
	userZsh := filepath.Join(root, "user_zsh")
	for _, name := range zshStartupFiles {
		writeNestedShellFile(t, filepath.Join(userZsh, name), "export RAN=\"${RAN}"+name+"\"\nsource \"$AO_TEST_WRAPPER/"+name+"\"\n")
	}
	user := map[string]string{"ZDOTDIR": userZsh}
	env, err := LoginShellEnv(root+":/usr/bin:/bin", func(key string) string { return user[key] }, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env["HOME"], env["PATH"], env["AO_TEST_WRAPPER"] = root, root+":/usr/bin:/bin", env["ZDOTDIR"]
	probe := `source "$AO_TEST_WRAPPER/.zshenv"; source "$AO_TEST_WRAPPER/.zprofile"; source "$AO_TEST_WRAPPER/.zshrc"; source "$AO_TEST_WRAPPER/.zlogin"; source "$AO_TEST_WRAPPER/.zlogout"; printf '%s' "$RAN"`
	if got := runNestedLoginShell(t, "/bin/zsh", env, probe); got != ".zshenv.zprofile.zshrc.zlogin.zlogout" {
		t.Fatalf("reentrant zsh startup output = %q", got)
	}
}

func writeNestedShellFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runNestedLoginShell(t *testing.T, shell string, env map[string]string, command string, flags ...string) string {
	t.Helper()
	if _, err := os.Stat(shell); err != nil {
		t.Skipf("%s not installed", shell)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, append(flags, "-c", command)...)
	cmd.WaitDelay = time.Second
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("nested %s: %v (context: %v)\n%s", shell, err, ctx.Err(), out)
	}
	return string(out)
}
