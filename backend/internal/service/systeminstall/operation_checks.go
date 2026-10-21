package systeminstall

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// Layout names returned by packageLayout.
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
	// layoutSystem is a directory owned by the operating system or a system
	// package manager (apt, dnf, Nix, Snap, Flatpak, MacPorts). Neither AO nor
	// the vendor's self-update maintains a copy there.
	layoutSystem = "system"
)

// systemPathPrefixes are the slash-separated, lower-case roots of layoutSystem.
var systemPathPrefixes = []string{
	"/usr/bin/", "/usr/sbin/", "/usr/lib/", "/usr/libexec/", "/usr/share/", "/bin/", "/sbin/",
	"/nix/store/", "/run/current-system/", "/snap/", "/var/lib/snapd/", "/var/lib/flatpak/", "/opt/local/",
}

// packageLayout names the package tool whose directory layout holds the
// executable at path, or "" when none does. Version-manager shims are
// checked on the unresolved path because they symlink to the manager's own
// binary rather than the harness.
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

// pathLayout is the package tool whose directory layout holds a path and,
// where that layout names it, the package directory the path belongs to.
type pathLayout struct {
	layout string
	pkg    string
	cask   bool
	// receipt is Homebrew's install receipt for the keg or cask, which names
	// the tap a formula came from.
	receipt string
}

func layoutOf(path string) pathLayout {
	// Windows separators are normalised explicitly: filepath.ToSlash only
	// rewrites the host separator, and paths may come from either platform.
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
	// npm's Windows global prefix (%APPDATA%\npm) names the layout before the
	// node_modules directory that names the package.
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

// nodePackageAt reads the package name that starts at segments[index] below a
// node_modules directory, including a scope such as @openai/codex.
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

// officialChannelFits reports whether an official release channel describes
// a binary in this layout. A binary inside another tool's layout follows that
// tool's channel, which can lag or lead the vendor's, so comparing it with
// the vendor channel could raise a permanent false update warning.
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

// installedBaseline is the harness binary as it was before an update, used to
// confirm the update changed the copy sessions actually run.
type installedBaseline struct {
	path     string
	version  string
	latest   string
	expected string
	scheme   versionScheme
}

// confirmInstalledOwner refuses an update through a method that
// does not own the binary the harness adapter runs, so AO never changes a
// different copy than the one sessions launch.
func (s *Service) confirmInstalledOwner(ctx context.Context, target Target, plan Plan) (*installedBaseline, error) {
	if s.verifier == nil || plan.Unsupported {
		return nil, nil
	}
	verified, err := s.verifier.Verify(ctx, target)
	scheme := versionSchemeFor(target)
	var baseline *installedBaseline
	switch {
	case err != nil && verified.ResolvedPath != "":
		// A broken binary cannot report its version, but AO's own successful
		// install through this method still identifies it, as long as the
		// method still owns the binary sessions resolve: the user may have
		// installed another copy since. The update runs on the resolved path,
		// never on whichever copy PATH finds first.
		job, statusErr := s.Status(ctx, target)
		if statusErr != nil || job.Status != StatusSucceeded || job.Method != plan.Method ||
			!s.methodOwnsBinary(ctx, plan, verified.ResolvedPath) {
			return nil, fmt.Errorf("%w: AO could not confirm which %s installation is in use (%w); update it manually", ErrInstallOwner, target, err)
		}
		version, _ := scheme.find(job.Output)
		baseline = &installedBaseline{path: verified.ResolvedPath, version: version.display, scheme: scheme}
	case err != nil:
		return nil, fmt.Errorf("%w: AO could not confirm which %s installation is in use (%w); update it manually", ErrInstallOwner, target, err)
	case verified.ResolvedPath == "":
		return nil, fmt.Errorf("%w: AO could not resolve the %s that sessions run; update it manually", ErrInstallOwner, target)
	default:
		if !s.methodOwnsBinary(ctx, plan, verified.ResolvedPath) {
			return nil, fmt.Errorf("%w: the %s that AO runs (%s) was not installed with %s; update it manually with the tool that installed it", ErrInstallOwner, target, verified.ResolvedPath, installMethodLabel(plan.Method))
		}
		version, _ := scheme.find(verified.Output)
		baseline = &installedBaseline{path: verified.ResolvedPath, version: version.display, scheme: scheme}
	}
	s.mu.Lock()
	if advisory, ok := s.updateAdvisories[target]; ok && advisory.Status == UpdateStatusBehindLatest {
		baseline.latest = advisory.LatestVersion
	}
	s.mu.Unlock()
	return baseline, nil
}

func (s *Service) methodOwnsBinary(ctx context.Context, plan Plan, path string) bool {
	if s.isManagedPath(path) {
		// AO's own versioned install: the method that installed it owns it.
		record, ok := s.readManagedRecord(plan.Target)
		return ok && record.Method == plan.Method
	}
	switch plan.Method {
	case "npm", "homebrew", "bun", "uv", "pipx", "winget":
		if s.ownsInstallation == nil || plan.Package == "" {
			return false
		}
		owned, err := s.ownsInstallation(ctx, path, plan.Method, packageWithoutLatest(plan.Package), plan.PackageCask)
		return err == nil && owned
	default:
		// Vendor installers write their own locations; any package tool's
		// layout means another installer owns this binary.
		return vendorLayout(plan.Target, packageLayout(path))
	}
}

// updateOutcome checks the re-probed binary against the pre-update baseline.
// It returns a failure message when a newer release was known and the binary
// sessions run did not reach it, and a note for the job output otherwise.
func updateOutcome(baseline *installedBaseline, result VerifyResult) (failure, note string) {
	if baseline == nil {
		return "", ""
	}
	if baseline.path != "" && result.ResolvedPath != "" && baseline.path != result.ResolvedPath {
		note = fmt.Sprintf("AO now runs %s (was %s).", result.ResolvedPath, baseline.path)
	}
	after, afterOK := baseline.scheme.find(result.Output)
	before, beforeOK := baseline.scheme.parse(baseline.version)
	if !afterOK {
		return "The update finished, but AO could not read the new version.", note
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
		return "The update finished, but AO could not read the target version.", note
	}
	if comparison, versionsComparable := baseline.scheme.compare(after, latest); latestOK && versionsComparable && comparison < 0 {
		// The path stays in diagnostics; the notice only needs the versions.
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
		// A same-version rebuild (e.g. Cursor's same-day build) has no order
		// against the old build, but reaching the announced build is an update.
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
