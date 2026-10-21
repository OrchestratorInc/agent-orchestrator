package systeminstall

import (
	"encoding/json"
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
	// pnpm, Yarn and Bun keep node_modules too; only npm's own layout is
	// derived, and only the outermost package installed the binary.
	match := layoutOf(resolved)
	if match.pkg == "" {
		return derivedOwner{}, false
	}
	switch match.layout {
	case layoutHomebrew:
		return derivedOwner{method: "homebrew", pkg: homebrewTappedName(match), cask: match.cask}, true
	case layoutNPM:
		return derivedOwner{method: "npm", pkg: match.pkg}, true
	case layoutUV:
		return derivedOwner{method: "uv", pkg: match.pkg}, true
	case layoutPipx:
		return derivedOwner{method: "pipx", pkg: match.pkg}, true
	default:
		return derivedOwner{}, false
	}
}

// homebrewTappedName qualifies a formula or cask from a third-party tap with
// that tap (owner/tap/name), read from Homebrew's install receipt. The short
// name would otherwise resolve to a different core package of the same name.
func homebrewTappedName(match pathLayout) string {
	body, err := os.ReadFile(match.receipt)
	if err != nil {
		return match.pkg
	}
	var receipt struct {
		Source struct {
			Tap string `json:"tap"`
		} `json:"source"`
	}
	if json.Unmarshal(body, &receipt) != nil {
		return match.pkg
	}
	switch tap := strings.ToLower(strings.TrimSpace(receipt.Source.Tap)); tap {
	case "", "homebrew/core", "homebrew/cask":
		return match.pkg
	default:
		if strings.Count(tap, "/") != 1 {
			return match.pkg
		}
		return tap + "/" + match.pkg
	}
}

// nodeShimTarget returns the package file a Windows (.cmd/.ps1) or POSIX npm
// shim launches, or "" when path is not such a shim.
func nodeShimTarget(path string) string {
	targets, err := nodeShimTargets(path)
	if err != nil || len(targets) == 0 {
		return ""
	}
	return targets[0]
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
