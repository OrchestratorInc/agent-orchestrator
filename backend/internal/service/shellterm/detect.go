package shellterm

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type AvailableShell struct {
	ID   string
	Name string
	Path string
}

type ShellCatalog struct {
	Default AvailableShell
	Shells  []AvailableShell
}

var unixShellDirs = []string{"/opt/homebrew/bin", "/usr/local/bin", "/bin", "/usr/bin"}

var unixShellNames = []string{"zsh", "bash", "fish", "nu", "pwsh", "sh", "dash", "ksh", "tcsh", "csh", "elvish", "xonsh"}

var shellDisplayNames = map[string]string{
	"nu":             "Nushell",
	"pwsh":           "PowerShell",
	"pwsh.exe":       "PowerShell",
	"powershell.exe": "Windows PowerShell",
	"cmd.exe":        "Command Prompt",
	"bash.exe":       "Git Bash",
	"wsl.exe":        "WSL",
}

func DetectShells() ShellCatalog {
	if runtime.GOOS == "windows" {
		return detectWindowsShells()
	}
	return detectUnixShells(readEtcShells("/etc/shells"), unixShellDirs, unixShellNames)
}

func CheckShellPath(candidate string) (string, error) {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return "", errors.New("enter a shell path")
	}
	if candidate == "~" || strings.HasPrefix(candidate, "~/") || strings.HasPrefix(candidate, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		candidate = filepath.Join(home, candidate[1:])
	}
	if strings.ContainsAny(candidate, `/\`) && !filepath.IsAbs(candidate) {
		return "", errors.New("use an absolute path")
	}
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return "", errors.New("this is a folder, not an executable")
	}
	path, err := exec.LookPath(candidate)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, exec.ErrNotFound) {
			return "", errors.New("no executable found at this path")
		}
		return "", errors.New("this file is not executable")
	}
	return path, nil
}

func readEtcShells(file string) []string {
	f, err := os.Open(file) // #nosec G304 -- fixed system path
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var shells []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		shells = append(shells, line)
	}
	return shells
}

func detectUnixShells(listed, dirs, names []string) ShellCatalog {
	candidates := append([]string{}, listed...)
	for _, dir := range dirs {
		for _, name := range names {
			candidates = append(candidates, filepath.Join(dir, name))
		}
	}
	seen := map[string]struct{}{}
	var shells []AvailableShell
	for _, candidate := range candidates {
		if !filepath.IsAbs(candidate) || !isExecutableFile(candidate) {
			continue
		}
		key := candidate
		if resolved, err := filepath.EvalSymlinks(candidate); err == nil {
			key = resolved
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		shells = append(shells, AvailableShell{ID: candidate, Name: shellDisplayName(candidate), Path: candidate})
	}
	sort.SliceStable(shells, func(i, j int) bool {
		if shells[i].Name != shells[j].Name {
			return shells[i].Name < shells[j].Name
		}
		return shells[i].Path < shells[j].Path
	})
	catalog := ShellCatalog{Shells: shells, Default: AvailableShell{ID: "auto"}}
	if argv := defaultUnixShell(); len(argv) > 0 {
		catalog.Default.Path = argv[0]
		catalog.Default.Name = shellDisplayName(argv[0])
	}
	return catalog
}

func detectWindowsShells() ShellCatalog {
	var shells []AvailableShell
	add := func(id string, argv []string) {
		if len(argv) == 0 {
			return
		}
		shells = append(shells, AvailableShell{ID: id, Name: shellDisplayName(argv[0]), Path: argv[0]})
	}
	add("pwsh", resolveKnownWindowsShell("pwsh.exe"))
	add("powershell", resolveKnownWindowsShell("powershell.exe"))
	cmd, _ := resolveWindowsShell("cmd")
	if len(cmd) > 0 && strings.EqualFold(filepath.Base(cmd[0]), "cmd.exe") {
		add("cmd", cmd)
	}
	add("git-bash", resolveGitBash())
	add("wsl", resolveKnownWindowsShell("wsl.exe"))
	catalog := ShellCatalog{Shells: shells, Default: AvailableShell{ID: "auto"}}
	if argv := resolveAutomaticWindowsShell(); len(argv) > 0 {
		catalog.Default.Path = argv[0]
		catalog.Default.Name = shellDisplayName(argv[0])
	}
	return catalog
}

func shellDisplayName(path string) string {
	base := filepath.Base(path)
	if name, ok := shellDisplayNames[strings.ToLower(base)]; ok {
		return name
	}
	if strings.EqualFold(filepath.Ext(base), ".exe") {
		return base[:len(base)-len(".exe")]
	}
	return base
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}
