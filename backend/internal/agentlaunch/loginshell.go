package agentlaunch

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// A login shell runs the user's startup files after AO pins its ao first on
// PATH, so a ~/.zprofile or ~/.bash_profile that prepends a directory holding
// another ao (an old npm or Homebrew install, the scripts/daemon-build.sh link)
// wins. Codex runs every agent command through one, and every AO terminal is
// one (#3562). zsh reads its startup files from ZDOTDIR, and a non-interactive
// bash reads BASH_ENV after its profile, so AO points both at wrappers that
// run the user's own files first and then put the pin back in front.
const (
	envPathPin     = "AO_PATH_PIN"
	envUserZDOTDIR = "AO_USER_ZDOTDIR"
	envUserBashEnv = "AO_USER_BASH_ENV"
)

var zshStartupFiles = []string{".zshenv", ".zprofile", ".zshrc", ".zlogin", ".zlogout"}

const repinPOSIX = `if [ -n "$AO_PATH_PIN" ]; then case ":$PATH:" in ":$AO_PATH_PIN:"*) ;; *) PATH="$AO_PATH_PIN:$PATH" ;; esac; fi`

const repinPromptHook = `if [ -z "$_AO_PATH_REPINNED" ]; then _AO_PATH_REPINNED=1; ` + repinPOSIX + `; fi`

const bashEnvWrapper = "# Written by AO: runs your BASH_ENV, then keeps AO's ao first on PATH.\n" +
	`if [ -n "$AO_USER_BASH_ENV" ] && [ -r "$AO_USER_BASH_ENV" ]; then . "$AO_USER_BASH_ENV"; fi` + "\n" +
	repinPOSIX + "\n"

// LoginShellEnv returns the variables that keep the first directory of the
// pinned PATH first in zsh and bash once the user's startup files have run.
// configured and getenv supply the user's own ZDOTDIR, BASH_ENV and
// PROMPT_COMMAND, which still run. It returns nil on Windows.
func LoginShellEnv(path string, getenv func(string) string, configured map[string]string, dataDir string) (map[string]string, error) {
	if runtime.GOOS == "windows" {
		return nil, nil
	}
	pin, _, _ := strings.Cut(path, string(os.PathListSeparator))
	if !filepath.IsAbs(pin) {
		return nil, fmt.Errorf("pinned PATH must start with an absolute directory, got %q", pin)
	}
	if !filepath.IsAbs(dataDir) {
		return nil, fmt.Errorf("AO login-shell data directory must be absolute, got %q", dataDir)
	}
	dir := filepath.Join(dataDir, "runtime", "login-shell")
	if err := writeLoginShellFiles(dir); err != nil {
		return nil, fmt.Errorf("write AO login-shell wrappers: %w", err)
	}
	bashEnv := filepath.Join(dir, "bash_env")
	lookup := func(key string) string {
		if value, ok := configured[key]; ok {
			return value
		}
		return getenv(key)
	}
	// A daemon started from an AO shell already sees these wrappers; the
	// user's own value is the one they saved.
	userValue := func(key, saved, ours string) string {
		if value := lookup(key); value != ours {
			return value
		}
		return lookup(saved)
	}
	// An interactive bash reads no BASH_ENV: before its first prompt, after
	// every startup file, this puts the pin back once.
	prompt := lookup("PROMPT_COMMAND")
	switch {
	case strings.Contains(prompt, repinPromptHook):
	case prompt == "":
		prompt = repinPromptHook
	default:
		prompt += "; " + repinPromptHook
	}
	return map[string]string{
		envPathPin:       pin,
		"ZDOTDIR":        dir,
		envUserZDOTDIR:   userValue("ZDOTDIR", envUserZDOTDIR, dir),
		"BASH_ENV":       bashEnv,
		envUserBashEnv:   userValue("BASH_ENV", envUserBashEnv, bashEnv),
		"PROMPT_COMMAND": prompt,
	}, nil
}

func zshWrapper(name string) string {
	return "# Written by AO: runs your " + name + ", then keeps AO's ao first on PATH.\n" +
		"_ao_zdotdir=\"$ZDOTDIR\"\n" +
		"ZDOTDIR=\"${AO_USER_ZDOTDIR:-$HOME}\"\n" +
		// /etc/zshrc names the history file after ZDOTDIR before .zshrc runs.
		"[[ \"$HISTFILE\" == \"$_ao_zdotdir/.zsh_history\" ]] && HISTFILE=\"$ZDOTDIR/.zsh_history\"\n" +
		"[[ -r \"$ZDOTDIR/" + name + "\" ]] && source \"$ZDOTDIR/" + name + "\"\n" +
		"AO_USER_ZDOTDIR=\"$ZDOTDIR\"\n" +
		"ZDOTDIR=\"$_ao_zdotdir\"\n" +
		"unset _ao_zdotdir\n" +
		"[[ -n \"$AO_PATH_PIN\" ]] && path=(\"$AO_PATH_PIN\" \"${(@)path:#$AO_PATH_PIN}\")\n" +
		lastInteractiveFile[name]
}

// lastInteractiveFile hands an interactive zsh its user's ZDOTDIR back after
// its last startup file, so tools that write to ${ZDOTDIR:-~} (p10k configure,
// compinit) keep using the user's directory. Agent shells keep the wrappers.
var lastInteractiveFile = map[string]string{
	".zshrc":  "[[ -o interactive && ! -o login ]] && ZDOTDIR=\"$AO_USER_ZDOTDIR\"\n",
	".zlogin": "[[ -o interactive ]] && ZDOTDIR=\"$AO_USER_ZDOTDIR\"\n",
}

func writeLoginShellFiles(dir string) error {
	files := map[string]string{"bash_env": bashEnvWrapper}
	for _, name := range zshStartupFiles {
		files[name] = zshWrapper(name)
	}
	pinMu.Lock()
	defer pinMu.Unlock()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for name, content := range files {
		target := filepath.Join(dir, name)
		if current, err := os.ReadFile(target); err == nil && bytes.Equal(current, []byte(content)) { //nolint:gosec // AO's own wrapper under the data dir
			continue
		}
		if err := writeFileAtomic(dir, target, content); err != nil {
			return err
		}
	}
	return nil
}

func writeFileAtomic(dir, target, content string) error {
	temp, err := os.CreateTemp(dir, ".ao-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if _, err := temp.WriteString(content); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, target)
}
