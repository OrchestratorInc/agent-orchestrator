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
			root, err := managerPath(ctx, commands, []string{"npm", "root", "-g"})
			if err != nil {
				return false, err
			}
			return nodePackageOwnsBinary(binaryPath, root, pkg)
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
			output, err := runOwnershipCommand(ctx, commands, []string{"uv", "tool", "list", "--show-paths", "--color", "never"})
			if err != nil {
				return false, err
			}
			return toolListContains(output, pkg, binaryPath), nil
		case "pipx":
			if packageLayout(binaryPath) != layoutPipx {
				return false, nil
			}
			output, err := runOwnershipCommand(ctx, commands, []string{"pipx", "list", "--json"})
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
			output, err := runOwnershipCommand(ctx, commands, argv)
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
	output, err := runOwnershipCommand(ctx, commands, argv)
	if err != nil {
		return "", err
	}
	root := strings.TrimSpace(output)
	if root == "" || !filepath.IsAbs(root) {
		return "", fmt.Errorf("manager returned a non-absolute path")
	}
	return filepath.Clean(root), nil
}

func runOwnershipCommand(ctx context.Context, commands ports.CommandRunner, argv []string) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output := &capturedOutput{max: maxOutputBytes}
	if err := commands.Run(probeCtx, argv, output, output); err != nil {
		return output.String(), err
	}
	return output.String(), nil
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
	switch strings.ToLower(filepath.Ext(binaryPath)) {
	case "", ".cmd", ".ps1":
	default:
		return false, nil
	}
	file, err := os.Open(binaryPath)
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, maxShimBytes+1))
	if err != nil {
		return false, err
	}
	if len(body) > maxShimBytes {
		return false, fmt.Errorf("manager shim exceeds %d bytes", maxShimBytes)
	}
	found := false
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
			if !filepath.IsAbs(argument) || !strings.Contains(strings.ReplaceAll(argument, `\`, "/"), "/node_modules/") {
				continue
			}
			owned, err := pathWithin(packageRoot, argument)
			if err != nil || !owned {
				return false, err
			}
			found = true
		}
	}
	return found, nil
}

func nodeShimLaunchLine(line string) bool {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "exec "), "& "))
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	command := fields[0]
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
			return name == strings.ToLower(pkg) || strings.HasPrefix(name, strings.ToLower(pkg)+"_")
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
