package systeminstall

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const maxShimBytes = 64 << 10

// managerOwnsBinary proves that the named package manager owns the exact
// executable selected by the harness adapter.
func managerOwnsBinary(commands ports.CommandRunner) func(context.Context, string, string, string, bool) (bool, error) {
	return func(ctx context.Context, binaryPath, method, pkg string, cask bool) (bool, error) {
		if commands == nil || binaryPath == "" || pkg == "" || packageLayout(binaryPath) == layoutVersionManager {
			return false, nil
		}
		switch method {
		case "npm":
			root, rootErr := managerPath(ctx, commands, []string{"npm", "root", "-g"})
			if rootErr == nil {
				if owned, err := nodePackageOwnsBinary(binaryPath, root, pkg); err == nil && owned {
					return true, nil
				}
			}
			// The daemon's npm is one Node among many: nvm, fnm, Node tarballs
			// and vendor installers keep their own global prefix.
			if _, ok := npmPrefixOwningBinary(binaryPath, pkg); ok {
				return true, nil
			}
			return false, rootErr
		case "pnpm":
			root, err := managerPath(ctx, commands, []string{"pnpm", "root", "-g"})
			if err != nil {
				return false, err
			}
			return nodePackageOwnsBinary(binaryPath, root, pkg)
		case "yarn":
			globalDir, err := managerPath(ctx, commands, []string{"yarn", "global", "dir"})
			if err != nil {
				return false, err
			}
			return nodePackageOwnsBinary(binaryPath, filepath.Join(globalDir, "node_modules"), pkg)
		case "homebrew":
			root, err := managerPath(ctx, commands, []string{"brew", "--prefix"})
			if err != nil {
				return false, err
			}
			name := filepath.Base(pkg)
			if cask {
				return pathWithin(filepath.Join(root, "Caskroom", name), binaryPath)
			}
			return pathWithin(filepath.Join(root, "Cellar", name), binaryPath)
		case "bun":
			binRoot, err := managerPath(ctx, commands, []string{"bun", "pm", "bin", "-g"})
			if err != nil {
				return false, err
			}
			packageRoot := filepath.Join(filepath.Dir(binRoot), "install", "global", "node_modules", filepath.FromSlash(pkg))
			if owned, err := pathWithin(packageRoot, binaryPath); err != nil || owned {
				return owned, err
			}
			if samePath(filepath.Dir(binaryPath), binRoot) {
				return nodeShimTargetsPackage(binaryPath, packageRoot)
			}
			return false, nil
		case "uv":
			if packageLayout(binaryPath) != layoutUV {
				return false, nil
			}
			output, err := runProbe(ctx, commands, localProbeTimeout, true, []string{"uv", "tool", "list", "--show-paths", "--color", "never"})
			if err != nil {
				return false, err
			}
			return toolListContains(output, pkg, binaryPath), nil
		case "pipx":
			if packageLayout(binaryPath) != layoutPipx {
				return false, nil
			}
			output, err := runProbe(ctx, commands, localProbeTimeout, false, []string{"pipx", "list", "--json"})
			if err != nil {
				return false, err
			}
			var inventory struct {
				Venvs map[string]struct {
					Metadata struct {
						MainPackage struct {
							Package  string            `json:"package"`
							AppPaths []json.RawMessage `json:"app_paths"`
						} `json:"main_package"`
					} `json:"metadata"`
				} `json:"venvs"`
			}
			if err := json.Unmarshal([]byte(output), &inventory); err != nil {
				return false, err
			}
			for _, venv := range inventory.Venvs {
				main := venv.Metadata.MainPackage
				if main.Package != pkg {
					continue
				}
				for _, raw := range main.AppPaths {
					var appPath string
					if json.Unmarshal(raw, &appPath) != nil {
						// pipx serializes pathlib.Path using this tagged object.
						var tagged struct {
							Type string `json:"__type__"`
							Path string `json:"__Path__"`
						}
						if json.Unmarshal(raw, &tagged) != nil || tagged.Type != "Path" {
							continue
						}
						appPath = tagged.Path
					}
					if sameExecutable(appPath, binaryPath) {
						return true, nil
					}
				}
			}
			return false, nil
		case "winget":
			if !wingetPackageContains(binaryPath, pkg) {
				return false, nil
			}
			argv := []string{"winget", "list", "--id", pkg, "--exact", "--source", "winget", "--accept-source-agreements", "--disable-interactivity"}
			output, err := runProbe(ctx, commands, localProbeTimeout, true, argv)
			if err != nil {
				return false, err
			}
			return outputHasExactField(output, pkg), nil
		default:
			return false, nil
		}
	}
}

func managerPath(ctx context.Context, commands ports.CommandRunner, argv []string) (string, error) {
	output, err := runProbe(ctx, commands, localProbeTimeout, false, argv)
	if err != nil {
		return "", err
	}
	root := strings.TrimSpace(output)
	if root == "" || !filepath.IsAbs(root) {
		return "", fmt.Errorf("manager returned a non-absolute path")
	}
	return filepath.Clean(root), nil
}

// Probe timeouts. Manager metadata commands may reach the network; ownership
// and runtime probes only read local state.
const (
	localProbeTimeout   = 5 * time.Second
	managerProbeTimeout = 10 * time.Second
)

// runProbe runs a bounded read-only command and returns its output. Stderr is
// kept only when keepStderr is set: manager warnings would corrupt JSON output.
func runProbe(ctx context.Context, commands ports.CommandRunner, timeout time.Duration, keepStderr bool, argv []string) (string, error) {
	if commands == nil {
		return "", fmt.Errorf("command runner unavailable")
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output := &capturedOutput{max: maxOutputBytes}
	stderr := io.Discard
	if keepStderr {
		stderr = output
	}
	err := commands.Run(probeCtx, argv, output, stderr)
	return output.String(), err
}

func nodePackageOwnsBinary(binaryPath, modulesRoot, pkg string) (bool, error) {
	packageRoot := filepath.Join(modulesRoot, filepath.FromSlash(pkg))
	if owned, err := pathWithin(packageRoot, binaryPath); err != nil || owned {
		return owned, err
	}
	if samePath(filepath.Dir(binaryPath), filepath.Dir(modulesRoot)) {
		return nodeShimTargetsPackage(binaryPath, packageRoot)
	}
	return false, nil
}

var quotedShimArgument = regexp.MustCompile(`["']([^"'\r\n]+)["']`)

func nodeShimTargetsPackage(binaryPath, packageRoot string) (bool, error) {
	targets, err := nodeShimTargets(binaryPath)
	if err != nil {
		return false, err
	}
	found := false
	for _, target := range targets {
		owned, err := pathWithin(packageRoot, target)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !owned {
			return false, err
		}
		found = true
	}
	return found, nil
}

// nodeShimTargets returns the node_modules files a Windows (.cmd/.ps1) or
// POSIX npm shim passes to node, in order; none when path is not such a shim.
func nodeShimTargets(binaryPath string) ([]string, error) {
	switch strings.ToLower(filepath.Ext(binaryPath)) {
	case "", ".cmd", ".ps1":
	default:
		return nil, nil
	}
	file, err := os.Open(binaryPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, maxShimBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxShimBytes {
		return nil, fmt.Errorf("manager shim exceeds %d bytes", maxShimBytes)
	}
	var targets []string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "@"))
		// Accept launcher arguments, never mentions in comments or assignments.
		if !nodeShimLaunchLine(line) {
			continue
		}
		for _, match := range quotedShimArgument.FindAllStringSubmatch(line, -1) {
			argument := strings.ReplaceAll(match[1], `\`, "/")
			for _, prefix := range []string{"%~dp0", "%dp0%", "$basedir", "${basedir}", "$PSScriptRoot"} {
				if strings.HasPrefix(strings.ToLower(argument), strings.ToLower(prefix)+"/") {
					argument = filepath.Join(filepath.Dir(binaryPath), filepath.FromSlash(argument[len(prefix)+1:]))
					break
				}
			}
			if filepath.IsAbs(argument) && strings.Contains(strings.ReplaceAll(argument, `\`, "/"), "/node_modules/") {
				targets = append(targets, argument)
			}
		}
	}
	return targets, nil
}

func nodeShimLaunchLine(line string) bool {
	// npm's cmd shim chains its launch after other commands:
	// endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & "%_prog%" ...
	for _, command := range strings.FieldsFunc(line, func(r rune) bool { return r == '&' || r == '|' }) {
		if nodeShimLaunchCommand(command) {
			return true
		}
	}
	return false
}

func nodeShimLaunchCommand(line string) bool {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "exec "))
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	command := fields[0]
	if strings.Contains(command, "=") {
		return false
	}
	if strings.HasPrefix(line, `"`) || strings.HasPrefix(line, "'") {
		match := quotedShimArgument.FindStringSubmatch(line)
		if match == nil {
			return false
		}
		command = match[1]
	}
	command = strings.ToLower(strings.ReplaceAll(command, `\`, "/"))
	// npm's cmd shim resolves node into %_prog%; its PS shim uses node$exe.
	base := command[strings.LastIndex(command, "/")+1:]
	return command == "%_prog%" || base == "node" || base == "node.exe" || base == "node$exe" || strings.Contains(command, "/node_modules/")
}

func pathWithin(root, path string) (bool, error) {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false, err
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil {
		return false, err
	}
	return relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)), nil
}

func samePath(left, right string) bool {
	return normalizeManagerPath(left) == normalizeManagerPath(right)
}

func normalizeManagerPath(path string) string {
	return strings.ToLower(strings.ReplaceAll(filepath.ToSlash(filepath.Clean(path)), `\`, "/"))
}

func toolListContains(output, pkg, binaryPath string) bool {
	selected := false
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "- ") {
			fields := strings.Fields(trimmed)
			selected = len(fields) >= 2 && fields[0] == pkg
			continue
		}
		if selected {
			start := strings.Index(trimmed, " (")
			if start >= 0 && strings.HasSuffix(trimmed, ")") && sameExecutable(trimmed[start+2:len(trimmed)-1], binaryPath) {
				return true
			}
		}
	}
	return false
}

func sameExecutable(left, right string) bool {
	if !filepath.IsAbs(left) || !filepath.IsAbs(right) {
		return false
	}
	l, leftErr := os.Stat(left)
	r, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && !l.IsDir() && !r.IsDir() && os.SameFile(l, r)
}

func wingetPackageContains(binaryPath, pkg string) bool {
	resolved, err := filepath.EvalSymlinks(binaryPath)
	if err != nil {
		return false
	}
	parts := strings.Split(normalizeManagerPath(resolved), "/")
	for i := 0; i+3 < len(parts); i++ {
		if parts[i] == "winget" && parts[i+1] == "packages" {
			name := parts[i+2]
			return strings.EqualFold(name, pkg) || strings.HasPrefix(name, strings.ToLower(pkg)+"_")
		}
	}
	return false
}

func outputHasExactField(output, value string) bool {
	for _, line := range strings.Split(output, "\n") {
		for _, field := range strings.Fields(line) {
			if strings.EqualFold(field, value) {
				return true
			}
		}
	}
	return false
}

func advisoryPackageSources(plans []Plan, preferred, layout string) []Plan {
	if layout == layoutVersionManager {
		return nil
	}
	sources := make([]Plan, 0, len(plans))
	for _, plan := range plans {
		if plan.Package == "" {
			continue
		}
		if (layout == layoutPNPM || layout == layoutYarn) && plan.Method == "npm" {
			advisoryPlan := plan
			advisoryPlan.Method = layout
			sources = append([]Plan{advisoryPlan}, sources...)
			continue
		}
		switch plan.Method {
		case "npm", "homebrew", "winget", "bun", "uv", "pipx":
		default:
			continue
		}
		if plan.Method == preferred {
			sources = append([]Plan{plan}, sources...)
		} else {
			sources = append(sources, plan)
		}
	}
	return sources
}

// npmPrefixOwningBinary finds the npm global prefix that installed pkg and
// owns binaryPath, from the binary's own location rather than the daemon's
// npm. A harness installed with another Node (nvm, fnm, a Node tarball, or an
// installer's private prefix such as ~/.local) lives under
// <prefix>/lib/node_modules/<pkg> on Unix and <prefix>\node_modules\<pkg> on
// Windows, where the prefix also holds the command shim.
func npmPrefixOwningBinary(binaryPath, pkg string) (string, bool) {
	if binaryPath == "" || pkg == "" {
		return "", false
	}
	// Bun, pnpm and Yarn also keep global packages under node_modules; npm
	// must not claim, and then rewrite, a copy another manager installed.
	switch packageLayout(binaryPath) {
	case layoutBun, layoutPNPM, layoutYarn, layoutVersionManager:
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(binaryPath)
	if err != nil {
		return "", false
	}
	slashed := filepath.ToSlash(resolved)
	for _, marker := range []string{"/lib/node_modules/" + pkg + "/", "/node_modules/" + pkg + "/"} {
		index := strings.LastIndex(slashed, marker)
		if index <= 0 {
			continue
		}
		prefix := filepath.FromSlash(slashed[:index])
		if npmPackageNamed(filepath.Join(prefix, filepath.FromSlash(strings.TrimPrefix(marker, "/"))), pkg) {
			return prefix, true
		}
	}
	// Windows npm shims (<prefix>\pi.cmd) are files in the prefix itself.
	prefix := filepath.Dir(binaryPath)
	packageRoot := filepath.Join(prefix, "node_modules", filepath.FromSlash(pkg))
	if owned, err := nodeShimTargetsPackage(binaryPath, packageRoot); err == nil && owned && npmPackageNamed(packageRoot, pkg) {
		return prefix, true
	}
	return "", false
}

func npmPackageNamed(packageRoot, pkg string) bool {
	body, err := os.ReadFile(filepath.Join(packageRoot, "package.json"))
	if err != nil {
		return false
	}
	var manifest struct {
		Name string `json:"name"`
	}
	return json.Unmarshal(body, &manifest) == nil && manifest.Name == pkg
}

// npmCommandForPrefix runs the npm that belongs to prefix with that prefix's
// Node when the prefix is a Node installation (nvm, fnm, a tarball), so an
// update uses the same Node the harness was installed with. Other prefixes,
// such as ~/.local or %APPDATA%\npm, use npm from PATH.
func npmCommandForPrefix(goos, prefix string) []string {
	node, cli := filepath.Join(prefix, "bin", "node"), filepath.Join(prefix, "lib", "node_modules", "npm", "bin", "npm-cli.js")
	if goos == "windows" {
		node, cli = filepath.Join(prefix, "node.exe"), filepath.Join(prefix, "node_modules", "npm", "bin", "npm-cli.js")
	}
	if regularFile(node) && regularFile(cli) {
		return []string{node, cli}
	}
	return []string{"npm"}
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
