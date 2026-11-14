package systeminstall

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

const (
	layoutVersionManager = "version-manager"
	layoutHomebrew       = "homebrew"
	layoutNPM            = "npm"
	layoutPNPM           = "pnpm"
	layoutYarn           = "yarn"
	layoutBun            = "bun"
	layoutUV             = "uv"
	layoutPipx           = "pipx"
	layoutWinget         = "winget"
	layoutSystem         = "system"
)

var systemPathPrefixes = []string{
	"/usr/bin/", "/usr/sbin/", "/usr/lib/", "/usr/libexec/", "/usr/share/", "/bin/", "/sbin/",
	"/nix/store/", "/run/current-system/", "/snap/", "/var/lib/snapd/", "/var/lib/flatpak/", "/opt/local/",
}

func packageLayout(path string) string {
	if path == "" {
		return ""
	}
	if match := layoutOf(path); match.layout == layoutVersionManager {
		return match.layout
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		resolved = path
	}
	return layoutOf(resolved).layout
}

type pathLayout struct {
	layout  string
	pkg     string
	cask    bool
	receipt string
}

func layoutOf(path string) pathLayout {
	slashed := strings.ReplaceAll(filepath.ToSlash(path), `\`, "/")
	lowered := strings.ToLower(slashed)
	for _, prefix := range systemPathPrefixes {
		if strings.HasPrefix(lowered, prefix) {
			return pathLayout{layout: layoutSystem}
		}
	}
	segments := strings.Split(slashed, "/")
	segment := func(index int) string {
		if index < 0 || index >= len(segments) {
			return ""
		}
		return strings.ToLower(segments[index])
	}
	original := func(index int) string {
		if index >= len(segments) {
			return ""
		}
		return segments[index]
	}
	windowsNPM := false
	for i := range segments {
		lower := segment(i)
		if windowsNPM {
			if lower == "node_modules" {
				return pathLayout{layout: layoutNPM, pkg: nodePackageAt(segments, i+1)}
			}
			continue
		}
		if lower == "npm" && segment(i-1) == "roaming" && segment(i-2) == "appdata" {
			windowsNPM = true
			continue
		}
		switch lower {
		case "shims", ".volta", ".asdf", "mise":
			return pathLayout{layout: layoutVersionManager}
		case ".bun":
			return pathLayout{layout: layoutBun}
		case "cellar":
			match := pathLayout{layout: layoutHomebrew, pkg: original(i + 1)}
			if i+2 < len(segments) {
				match.receipt = filepath.FromSlash(strings.Join(segments[:i+3], "/") + "/INSTALL_RECEIPT.json")
			}
			return match
		case "caskroom":
			match := pathLayout{layout: layoutHomebrew, pkg: original(i + 1), cask: true}
			if match.pkg != "" {
				match.receipt = filepath.FromSlash(strings.Join(segments[:i+2], "/") + "/.metadata/INSTALL_RECEIPT.json")
			}
			return match
		case "pipx":
			match := pathLayout{layout: layoutPipx}
			if segment(i+1) == "venvs" {
				match.pkg = original(i + 2)
			}
			return match
		case "winget":
			return pathLayout{layout: layoutWinget}
		case "pnpm":
			return pathLayout{layout: layoutPNPM}
		case "yarn":
			return pathLayout{layout: layoutYarn}
		case "node_modules":
			return pathLayout{layout: layoutNPM, pkg: nodePackageAt(segments, i+1)}
		case "uv":
			if segment(i+1) == "tools" {
				return pathLayout{layout: layoutUV, pkg: original(i + 2)}
			}
		}
	}
	if windowsNPM {
		return pathLayout{layout: layoutNPM}
	}
	return pathLayout{}
}

func nodePackageAt(segments []string, index int) string {
	if index >= len(segments) {
		return ""
	}
	pkg := segments[index]
	if strings.HasPrefix(pkg, "@") {
		if index+1 >= len(segments) {
			return ""
		}
		pkg += "/" + segments[index+1]
	}
	return pkg
}

func officialChannelFits(kind officialSourceKind, layout string) bool {
	switch layout {
	case "":
		return true
	case layoutNPM:
		return kind == officialNPM
	case layoutUV, layoutPipx:
		return kind == officialPyPI
	default:
		return false
	}
}

type installedBaseline struct {
	target   Target
	path     string
	version  string
	latest   string
	expected string
	scheme   versionScheme
}

func (s *Service) confirmInstalledOwner(ctx context.Context, target Target, plan Plan) (*installedBaseline, error) {
	if s.verifier == nil || plan.Unsupported {
		return nil, nil
	}
	verified, err := s.verifier.Verify(ctx, target)
	scheme := versionSchemeFor(target)
	var baseline *installedBaseline
	switch {
	case err != nil && verified.ResolvedPath != "":
		job, statusErr := s.Status(ctx, target)
		if statusErr != nil || job.Status != StatusSucceeded || job.Method != plan.ownershipMethod() ||
			!s.methodOwnsBinary(ctx, plan, verified.ResolvedPath) {
			return nil, fmt.Errorf("%w: Orchestrator could not confirm which %s installation is in use (%w); update it manually", ErrInstallOwner, target, err)
		}
		version, _ := scheme.find(installedVersionText(target, verified.ResolvedPath, job.Output))
		baseline = &installedBaseline{target: target, path: verified.ResolvedPath, version: version.display, scheme: scheme}
	case err != nil:
		return nil, fmt.Errorf("%w: Orchestrator could not confirm which %s installation is in use (%w); update it manually", ErrInstallOwner, target, err)
	case verified.ResolvedPath == "":
		return nil, fmt.Errorf("%w: Orchestrator could not resolve the %s that sessions run; update it manually", ErrInstallOwner, target)
	default:
		if !s.methodOwnsBinary(ctx, plan, verified.ResolvedPath) {
			return nil, fmt.Errorf("%w: the %s that Orchestrator runs (%s) was not installed with %s; update it manually with the tool that installed it", ErrInstallOwner, target, verified.ResolvedPath, installMethodLabel(plan.ownershipMethod()))
		}
		version, _ := scheme.find(installedVersionText(target, verified.ResolvedPath, verified.Output))
		baseline = &installedBaseline{target: target, path: verified.ResolvedPath, version: version.display, scheme: scheme}
	}
	s.mu.Lock()
	if advisory, ok := s.updateAdvisories[target]; ok && advisory.Status == UpdateStatusBehindLatest {
		baseline.latest = advisory.LatestVersion
	}
	s.mu.Unlock()
	return baseline, nil
}

func (s *Service) methodOwnsBinary(ctx context.Context, plan Plan, path string) bool {
	if plan.sourceMethod != "" {
		plan.Method, plan.Package = plan.sourceMethod, plan.sourcePackage
	}
	if s.isManagedPath(path) {
		record, ok := s.readManagedRecord(plan.Target)
		return ok && s.managedRecordMatchesPlan(record, plan)
	}
	switch plan.Method {
	case "npm", "homebrew", "bun", "uv", "pipx", "winget":
		if s.ownsInstallation == nil || plan.Package == "" {
			return false
		}
		owned, err := s.ownsInstallation(ctx, path, plan.Method, packageWithoutLatest(plan.Package), plan.PackageCask)
		return err == nil && owned
	default:
		return vendorLayout(plan.Target, packageLayout(path))
	}
}

func updateOutcome(baseline *installedBaseline, result VerifyResult) (failure, note string) {
	if baseline == nil {
		return "", ""
	}
	if baseline.path != "" && result.ResolvedPath != "" && baseline.path != result.ResolvedPath {
		note = fmt.Sprintf("Orchestrator now runs %s (was %s).", result.ResolvedPath, baseline.path)
	}
	after, afterOK := baseline.scheme.find(installedVersionText(baseline.target, result.ResolvedPath, result.Output))
	before, beforeOK := baseline.scheme.parse(baseline.version)
	if !afterOK {
		return "The update finished, but Orchestrator could not read the new version.", note
	}
	if baseline.expected != "" {
		expected, ok := baseline.scheme.parse(baseline.expected)
		comparison, canCompare := baseline.scheme.compare(after, expected)
		if !ok || !canCompare || comparison < 0 {
			return fmt.Sprintf("The update finished, but it reports %s, not the requested version %s.", after.display, baseline.expected), note
		}
	}
	latest, latestOK := baseline.scheme.parse(baseline.latest)
	if baseline.latest != "" && !latestOK {
		return "The update finished, but Orchestrator could not read the target version.", note
	}
	if comparison, versionsComparable := baseline.scheme.compare(after, latest); latestOK && versionsComparable && comparison < 0 {
		hint := "The update may have changed a different installation."
		if result.ResolvedPath != "" {
			hint = fmt.Sprintf("The update may have changed a different installation than %s.", result.ResolvedPath)
		}
		return fmt.Sprintf("The update finished, but it still reports %s (latest is %s).", after.display, latest.display), strings.TrimSpace(note + " " + hint)
	}
	if _, canCompare := baseline.scheme.compare(after, latest); latestOK && !canCompare {
		return "The update finished, but on a different release channel.", note
	}
	if !beforeOK {
		return "", strings.TrimSpace(fmt.Sprintf("Verified version %s. %s", after.display, note))
	}
	comparison, canCompare := baseline.scheme.compare(after, before)
	if !canCompare && latestOK && after.display == latest.display {
		return "", strings.TrimSpace(fmt.Sprintf("Updated %s to %s. %s", before.display, after.display, note))
	}
	if !canCompare || comparison < 0 {
		return "The update finished, but the version went back or changed release channel.", note
	}
	if comparison > 0 {
		return "", strings.TrimSpace(fmt.Sprintf("Updated %s to %s. %s", before.display, after.display, note))
	}
	return "", strings.TrimSpace(fmt.Sprintf("Version unchanged at %s. %s", after.display, note))
}
