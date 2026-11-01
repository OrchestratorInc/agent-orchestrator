//go:build !windows

package agentlaunch

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// loginShellHome lays out a pinned ao, a stale one, and a home whose every
// zsh and bash startup file prepends the stale one's directory and records
// that it ran.
func loginShellHome(t *testing.T) (pin, stale, home string) {
	t.Helper()
	root := t.TempDir()
	pin = filepath.Join(root, "pin")
	stale = filepath.Join(root, "stale bin")
	home = filepath.Join(root, "home")
	writeExecutableFixture(t, filepath.Join(pin, "ao"))
	writeExecutableFixture(t, filepath.Join(stale, "ao"))
	if err := os.MkdirAll(home, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".zshenv", ".zprofile", ".zshrc", ".zlogin", ".bash_profile", ".bashrc"} {
		script := "export PATH='" + stale + "':\"$PATH\"\nexport RAN=\"$RAN " + name + "\"\n"
		if err := os.WriteFile(filepath.Join(home, name), []byte(script), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return pin, stale, home
}

func runLoginShell(t *testing.T, env map[string]string, stdin string, argv ...string) string {
	t.Helper()
	if _, err := os.Stat(argv[0]); err != nil {
		t.Skipf("%s not installed", argv[0])
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		cmd.Env = append(cmd.Env, key+"="+env[key])
	}
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%q: %v\n%s", argv, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestLoginShellEnvKeepsThePinFirst is the #3562 reproduction: every shell
// starts with the pin first, the user's startup files prepend a stale ao, and
// with LoginShellEnv the pinned ao still wins after all of them ran.
func TestLoginShellEnvKeepsThePinFirst(t *testing.T) {
	pin, stale, home := loginShellHome(t)
	path := pin + ":/usr/bin:/bin"
	loginEnv, err := LoginShellEnv(path, func(string) string { return "" }, nil, filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	const probe = `printf '%s|%s\n' "$(command -v ao)" "$RAN"`
	for _, tc := range []struct {
		name  string
		argv  []string
		stdin string
		ran   string
	}{
		// Codex runs agent commands this way.
		{"zsh -lc", []string{"/bin/zsh", "-l", "-c", probe}, "", ".zshenv .zprofile .zlogin"},
		{"bash -lc", []string{"/bin/bash", "-l", "-c", probe}, "", ".bash_profile"},
		{"zsh -c", []string{"/bin/zsh", "-c", probe}, "", ".zshenv"},
		// AO terminals start interactive login shells.
		{"zsh --login -i", []string{"/bin/zsh", "--login", "-i"}, probe + "\nexit\n", ".zshenv .zprofile .zshrc .zlogin"},
		{"bash --login -i", []string{"/bin/bash", "--login", "-i"}, "\n" + probe + "\nexit\n", ".bash_profile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := map[string]string{"HOME": home, "PATH": path, "TERM": "dumb"}
			if got := runLoginShell(t, base, tc.stdin, tc.argv...); !strings.HasPrefix(got, filepath.Join(stale, "ao")+"|") {
				t.Fatalf("without the wrappers = %q, want the stale ao (the reproduction)", got)
			}
			env := map[string]string{"HOME": home, "PATH": path, "TERM": "dumb"}
			for key, value := range loginEnv {
				env[key] = value
			}
			want := filepath.Join(pin, "ao") + "| " + tc.ran
			if got := runLoginShell(t, env, tc.stdin, tc.argv...); got != want {
				t.Fatalf("with the wrappers = %q, want %q: the pinned ao after every startup file ran", got, want)
			}
		})
	}
}

// TestLoginShellEnvRunsTheUsersOwnHooks checks the user's own ZDOTDIR, one
// set from ~/.zshenv, and BASH_ENV all still run, and that a daemon started
// inside an AO shell does not take AO's wrappers for the user's.
func TestLoginShellEnvRunsTheUsersOwnHooks(t *testing.T) {
	pin, _, home := loginShellHome(t)
	config := filepath.Join(home, ".config", "zsh")
	if err := os.MkdirAll(config, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".zshenv"), []byte("export ZDOTDIR='"+config+"'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, ".zprofile"), []byte("export RAN=config-zprofile\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	userBashEnv := filepath.Join(home, "bash_env")
	if err := os.WriteFile(userBashEnv, []byte("export RAN=user-bash-env\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(t.TempDir(), "data")
	path := pin + ":/usr/bin:/bin"
	user := map[string]string{"BASH_ENV": userBashEnv, "PROMPT_COMMAND": "history -a"}
	loginEnv, err := LoginShellEnv(path, func(key string) string { return user[key] }, nil, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	// The daemon itself started from a shell carrying these wrappers.
	again, err := LoginShellEnv(path, func(key string) string { return loginEnv[key] }, nil, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if again[envUserBashEnv] != userBashEnv || again[envUserZDOTDIR] != "" || again["PROMPT_COMMAND"] != loginEnv["PROMPT_COMMAND"] ||
		!strings.HasPrefix(again["PROMPT_COMMAND"], "history -a; ") {
		t.Fatalf("wrapped daemon env = %v, want the user's own BASH_ENV %s, no ZDOTDIR, and their PROMPT_COMMAND extended once", again, userBashEnv)
	}
	env := map[string]string{"HOME": home, "PATH": path}
	for key, value := range again {
		env[key] = value
	}
	const probe = `printf '%s|%s\n' "$(command -v ao)" "$RAN"`
	if got, want := runLoginShell(t, env, "", "/bin/zsh", "-l", "-c", probe), filepath.Join(pin, "ao")+"|config-zprofile"; got != want {
		t.Fatalf("zsh with ZDOTDIR from ~/.zshenv = %q, want %q", got, want)
	}
	if got, want := runLoginShell(t, env, "", "/bin/bash", "-c", probe), filepath.Join(pin, "ao")+"|user-bash-env"; got != want {
		t.Fatalf("bash with the user's BASH_ENV = %q, want %q", got, want)
	}
}

// TestLoginShellEnvHandsInteractiveShellsBackTheirZDOTDIR covers a terminal:
// /etc/zshrc (macOS) names the history file after ZDOTDIR before .zshrc, and
// tools like p10k configure write to ${ZDOTDIR:-~}, so an interactive zsh must
// end on the user's own directory. An agent's non-interactive shell keeps the
// wrappers, so the commands it starts stay pinned.
func TestLoginShellEnvHandsInteractiveShellsBackTheirZDOTDIR(t *testing.T) {
	pin, _, home := loginShellHome(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	path := pin + ":/usr/bin:/bin"
	env, err := LoginShellEnv(path, func(string) string { return "" }, nil, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	env["HOME"], env["PATH"], env["TERM"] = home, path, "dumb"
	const probe = `print -r -- "$ZDOTDIR|$HISTFILE"`
	for _, argv := range [][]string{{"/bin/zsh", "--login", "-i"}, {"/bin/zsh", "-i"}} {
		got := runLoginShell(t, env, probe+"\nexit\n", argv...)
		zdotdir, histfile, _ := strings.Cut(got, "|")
		if zdotdir != home || strings.HasPrefix(histfile, dataDir) {
			t.Fatalf("%q ends with ZDOTDIR|HISTFILE = %q, want ZDOTDIR %s and no history inside AO's wrappers", argv, got, home)
		}
	}
	if got := runLoginShell(t, env, "", "/bin/zsh", "-l", "-c", `print -r -- "$ZDOTDIR"`); got != env["ZDOTDIR"] {
		t.Fatalf("zsh -lc ends with ZDOTDIR = %q, want AO's wrappers %s", got, env["ZDOTDIR"])
	}
}

func TestLoginShellEnvRejectsARelativePin(t *testing.T) {
	if _, err := LoginShellEnv("relative:/usr/bin", func(string) string { return "" }, nil, t.TempDir()); err == nil {
		t.Fatal("relative pin accepted")
	}
}
