package systeminstall

import (
	"os"
	"path/filepath"
	"strings"
)

// derivedOwner is the package manager and package that installed a harness
// binary, read from where that manager keeps its files rather than from AO's
// fixed method list, so a user's own install through a supported manager is
// maintainable even when AO does not list that package for the harness.
type derivedOwner struct {
	method string
	pkg    string
	cask   bool
}

// npmInstallerTargets install through npm from a vendor release tarball that
// the npm registry does not carry. Their npm layout belongs to the vendor
// installer, which updates through the harness's own command.
var npmInstallerTargets = map[Target]bool{
	TargetPrimeAgent: true,
}

// deriveOwnerFromPath names the package manager and package whose layout holds
// binaryPath. Shims are followed to the file they launch. The result is only a
// claim: callers must confirm it with the manager before acting on it.
func deriveOwnerFromPath(binaryPath string) (derivedOwner, bool) {
	if binaryPath == "" || packageLayout(binaryPath) == layoutVersionManager {
		return derivedOwner{}, false
	}
	resolved, err := filepath.EvalSymlinks(binaryPath)
	if err != nil {
		return derivedOwner{}, false
	}
	if target := nodeShimTarget(resolved); target != "" {
		resolved = target
	}
	segments := strings.Split(strings.ReplaceAll(filepath.ToSlash(resolved), `\`, "/"), "/")
	for index := 0; index+1 < len(segments); index++ {
		next := segments[index+1]
		switch strings.ToLower(segments[index]) {
		case "cellar":
			return derivedOwner{method: "homebrew", pkg: next}, true
		case "caskroom":
			return derivedOwner{method: "homebrew", pkg: next, cask: true}, true
		case "node_modules":
			// pnpm, Yarn and Bun keep node_modules too; only npm's own layout
			// is derived, and only the outermost package installed the binary.
			if packageLayout(resolved) != layoutNPM {
				return derivedOwner{}, false
			}
			pkg := next
			if strings.HasPrefix(pkg, "@") && index+2 < len(segments) {
				pkg += "/" + segments[index+2]
			}
			return derivedOwner{method: "npm", pkg: pkg}, true
		case "tools":
			if index > 0 && strings.EqualFold(segments[index-1], "uv") {
				return derivedOwner{method: "uv", pkg: next}, true
			}
		case "venvs":
			if index > 0 && strings.EqualFold(segments[index-1], "pipx") {
				return derivedOwner{method: "pipx", pkg: next}, true
			}
		}
	}
	return derivedOwner{}, false
}

// nodeShimTarget returns the package file a Windows (.cmd/.ps1) or POSIX npm
// shim launches, or "" when path is not such a shim.
func nodeShimTarget(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case "", ".cmd", ".ps1":
	default:
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxShimBytes {
		return ""
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "@"))
		if !nodeShimLaunchLine(line) {
			continue
		}
		for _, match := range quotedShimArgument.FindAllStringSubmatch(line, -1) {
			argument := strings.ReplaceAll(match[1], `\`, "/")
			for _, prefix := range []string{"%~dp0", "%dp0%", "$basedir", "${basedir}", "$PSScriptRoot"} {
				if strings.HasPrefix(strings.ToLower(argument), strings.ToLower(prefix)+"/") {
					argument = filepath.Join(filepath.Dir(path), filepath.FromSlash(argument[len(prefix)+1:]))
					break
				}
			}
			if filepath.IsAbs(argument) && strings.Contains(filepath.ToSlash(argument), "/node_modules/") {
				return argument
			}
		}
	}
	return ""
}

// derivedOwnerPlan builds the install plan for a derived owner through the
// same builders as listed methods, so availability checks still apply.
func (s requestPlanner) derivedOwnerPlan(target Target, owner derivedOwner) (Plan, bool) {
	switch owner.method {
	case "homebrew":
		if owner.cask {
			return s.planBrewCask(target, owner.pkg), true
		}
		return s.planBrew(target, owner.pkg), true
	case "npm":
		return s.planNPM(target, owner.pkg), true
	case "uv":
		return s.planUV(target, owner.pkg), true
	case "pipx":
		return s.planPipx(target, owner.pkg), true
	default:
		return Plan{}, false
	}
}

// withDerivedOwner lists a confirmed derived owner among a harness's methods.
// It replaces a listed plan for the same manager, whose package is not the one
// installed, so method ids stay unique.
func withDerivedOwner(plans []Plan, derived Plan) []Plan {
	out := make([]Plan, 0, len(plans)+1)
	replaced := false
	for _, plan := range plans {
		if plan.Method == derived.Method {
			if !replaced {
				out = append(out, derived)
				replaced = true
			}
			continue
		}
		out = append(out, plan)
	}
	if !replaced {
		out = append(out, derived)
	}
	return out
}

func (s *Service) derivedOwnerFor(target Target) (derivedOwner, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	owner, ok := s.derivedOwners[target]
	return owner, ok
}

func (s *Service) setDerivedOwner(target Target, owner *derivedOwner) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if owner == nil {
		delete(s.derivedOwners, target)
		return
	}
	if s.derivedOwners == nil {
		s.derivedOwners = make(map[Target]derivedOwner)
	}
	s.derivedOwners[target] = *owner
}
