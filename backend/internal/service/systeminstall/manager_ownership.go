package systeminstall

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
							Package string `json:"package"`
						} `json:"main_package"`
					} `json:"metadata"`
				} `json:"venvs"`
			}
			if err := json.Unmarshal([]byte(output), &inventory); err != nil {
				return false, err
			}
			venv, ok := inventory.Venvs[pkg]
			return ok && venv.Metadata.MainPackage.Package == pkg, nil
		case "winget":
			if packageLayout(binaryPath) != layoutWinget {
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
	contents := normalizeManagerPath(string(body))
	absolute := normalizeManagerPath(packageRoot)
	if strings.Contains(contents, absolute) {
		return true, nil
	}
	marker := "/node_modules/"
	if index := strings.LastIndex(absolute, marker); index >= 0 {
		return strings.Contains(contents, absolute[index+1:]), nil
	}
	return false, nil
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
	packageFound := false
	pathFound := false
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, pkg+" ") || trimmed == pkg {
			packageFound = true
		}
		if strings.Contains(normalizeManagerPath(trimmed), normalizeManagerPath(binaryPath)) {
			pathFound = true
		}
	}
	return packageFound && pathFound
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
