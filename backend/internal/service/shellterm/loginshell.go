package shellterm

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func resolveUserLoginShell(preference string) (argv []string, usedFallback bool) {
	if runtime.GOOS == "windows" {
		return resolveWindowsShell(preference)
	}
	preference = strings.TrimSpace(preference)
	if preference == "" || strings.EqualFold(preference, "auto") {
		return defaultUnixShell(), false
	}
	if _, ok := windowsShellSelectors[strings.ToLower(preference)]; ok {
		if path, err := exec.LookPath(preference); err == nil {
			return []string{path}, false
		}
		return defaultUnixShell(), false
	}
	if path, err := CheckShellPath(preference); err == nil {
		return []string{path}, false
	}
	return defaultUnixShell(), true
}

var windowsShellSelectors = map[string]struct{}{
	"git-bash": {}, "pwsh": {}, "powershell": {}, "cmd": {}, "wsl": {},
}

func defaultUnixShell() []string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return []string{shell}
	}
	for _, candidate := range []string{"zsh", "bash", "sh"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return []string{path}
		}
	}
	return nil
}

func resolveWindowsShell(preference string) (argv []string, usedFallback bool) {
	preference = strings.TrimSpace(preference)
	if preference == "" || strings.EqualFold(preference, "auto") {
		return resolveAutomaticWindowsShell(), false
	}

	var resolved []string
	switch strings.ToLower(preference) {
	case "git-bash":
		resolved = resolveGitBash()
	case "pwsh":
		resolved = resolveKnownWindowsShell("pwsh.exe")
	case "powershell":
		resolved = resolveKnownWindowsShell("powershell.exe")
	case "wsl":
		resolved = resolveKnownWindowsShell("wsl.exe")
	case "cmd":
		if comSpec := strings.TrimSpace(os.Getenv("ComSpec")); comSpec != "" {
			if path, err := exec.LookPath(comSpec); err == nil {
				resolved = []string{path}
			}
		}
		if len(resolved) == 0 {
			resolved = resolveKnownWindowsShell("cmd.exe")
		}
	default:
		if path, err := CheckShellPath(preference); err == nil {
			resolved = windowsShellArgv(path)
		}
	}
	if len(resolved) > 0 {
		return resolved, false
	}
	return resolveAutomaticWindowsShell(), true
}

func resolveAutomaticWindowsShell() []string {
	for _, candidate := range []string{"pwsh.exe", "powershell.exe"} {
		if argv := resolveKnownWindowsShell(candidate); len(argv) > 0 {
			return argv
		}
	}
	if comSpec := os.Getenv("ComSpec"); comSpec != "" {
		return []string{comSpec}
	}
	if path, err := exec.LookPath("cmd.exe"); err == nil {
		return []string{path}
	}
	return nil
}

func resolveKnownWindowsShell(candidate string) []string {
	path, err := exec.LookPath(candidate)
	if err != nil {
		return nil
	}
	return windowsShellArgv(path)
}

func windowsShellArgv(path string) []string {
	switch strings.ToLower(filepath.Base(path)) {
	case "pwsh.exe", "powershell.exe":
		return []string{path, "-NoLogo"}
	case "bash.exe", "sh.exe":
		return []string{path, "--login", "-i"}
	default:
		return []string{path}
	}
}

func resolveGitBash() []string {
	seen := map[string]struct{}{}
	for _, candidate := range gitBashCandidates() {
		if candidate == "" {
			continue
		}
		key := strings.ToLower(filepath.Clean(candidate))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return []string{candidate, "--login", "-i"}
		}
	}
	if path, err := exec.LookPath("bash.exe"); err == nil {
		return []string{path, "--login", "-i"}
	}
	return nil
}

func gitBashCandidates() []string {
	candidates := make([]string, 0, 8)
	if gitPath, err := exec.LookPath("git.exe"); err == nil {
		gitDir := filepath.Dir(gitPath)
		gitRoot := filepath.Dir(gitDir)
		candidates = append(candidates,
			filepath.Join(gitRoot, "bin", "bash.exe"),
			filepath.Join(gitRoot, "usr", "bin", "bash.exe"),
		)
	}
	for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		if root != "" {
			candidates = append(candidates, filepath.Join(root, "Git", "bin", "bash.exe"))
		}
	}
	if localAppData := os.Getenv("LocalAppData"); localAppData != "" {
		candidates = append(candidates, filepath.Join(localAppData, "Programs", "Git", "bin", "bash.exe"))
	}
	return candidates
}
