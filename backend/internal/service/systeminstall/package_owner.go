package systeminstall

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type derivedOwner struct {
	method string
	pkg    string
	cask   bool
}

var npmInstallerTargets = map[Target]bool{
	TargetPrimeAgent: true,
}

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

func nodeShimTarget(path string) string {
	targets, err := nodeShimTargets(path)
	if err != nil || len(targets) == 0 {
		return ""
	}
	return targets[0]
}

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
