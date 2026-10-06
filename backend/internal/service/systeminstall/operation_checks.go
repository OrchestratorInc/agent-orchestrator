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
)

// packageLayout names the package tool whose directory layout holds the
// executable at path, or "" when none does. Version-manager shims are
// checked on the unresolved path because they symlink to the manager's own
// binary rather than the harness.
func packageLayout(path string) string {
	if path == "" {
		return ""
	}
	if layout := layoutOf(path); layout == layoutVersionManager {
		return layout
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		resolved = path
	}
	return layoutOf(resolved)
}

func layoutOf(path string) string {
	// Windows separators are normalised explicitly: filepath.ToSlash only
	// rewrites the host separator, and paths may come from either platform.
	segments := strings.Split(strings.ToLower(strings.ReplaceAll(filepath.ToSlash(path), `\`, "/")), "/")
	for i, segment := range segments {
		switch segment {
		case "shims", ".volta", ".asdf", "mise":
			return layoutVersionManager
		case ".bun":
			return layoutBun
		case "cellar", "caskroom":
			return layoutHomebrew
		case "pipx":
			return layoutPipx
		case "winget":
			return layoutWinget
		case "pnpm":
			return layoutPNPM
		case "yarn":
			return layoutYarn
		case "node_modules":
			return layoutNPM
		case "uv":
			if i+1 < len(segments) && segments[i+1] == "tools" {
				return layoutUV
			}
		}
	}
	return ""
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
	path    string
	version string
	latest  string
}

// confirmInstalledOwner refuses an update or uninstall through a method that
// does not own the binary the harness adapter runs, so AO never changes a
// different copy than the one sessions launch.
func (s *Service) confirmInstalledOwner(ctx context.Context, target Target, plan Plan) (*installedBaseline, error) {
	if s.verifier == nil || plan.Unsupported {
		return nil, nil
	}
	verified, err := s.verifier.Verify(ctx, target)
	if err != nil {
		// A broken binary cannot reveal its owner, but AO's own successful
		// install through this method still identifies it.
		job, statusErr := s.Status(ctx, target)
		if statusErr == nil && job.Status == StatusSucceeded && job.Method == plan.Method {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: AO could not confirm which %s installation is in use (%w); update or remove it manually", ErrInstallOwner, target, err)
	}
	if !s.methodOwnsBinary(ctx, plan, verified.ResolvedPath) {
		return nil, fmt.Errorf("%w: the %s that AO runs (%s) was not installed with %s; update or remove it manually with the tool that installed it", ErrInstallOwner, target, verified.ResolvedPath, installMethodLabel(plan.Method))
	}
	version, _ := findUpdateVersion(verified.Output)
	baseline := &installedBaseline{path: verified.ResolvedPath, version: version.display}
	s.mu.Lock()
	if advisory, ok := s.updateAdvisories[target]; ok && advisory.Status == UpdateStatusBehindLatest {
		baseline.latest = advisory.LatestVersion
	}
	s.mu.Unlock()
	return baseline, nil
}

func (s *Service) methodOwnsBinary(ctx context.Context, plan Plan, path string) bool {
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
		return packageLayout(path) == ""
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
	after, afterOK := findUpdateVersion(result.Output)
	before, beforeOK := parseUpdateVersion(baseline.version)
	if !afterOK || !beforeOK {
		return "", note
	}
	if comparison, comparable := compareUpdateVersions(after, before); comparable && comparison > 0 {
		return "", strings.TrimSpace(fmt.Sprintf("Updated %s to %s. %s", before.display, after.display, note))
	}
	latest, latestOK := parseUpdateVersion(baseline.latest)
	if comparison, comparable := compareUpdateVersions(after, latest); latestOK && comparable && comparison < 0 {
		return fmt.Sprintf("the update finished, but %s still reports %s (latest is %s); it may have changed a different installation", result.ResolvedPath, after.display, latest.display), note
	}
	return "", strings.TrimSpace(fmt.Sprintf("Version unchanged at %s. %s", after.display, note))
}

// uninstallOutcome reports a failure when the exact binary sessions ran
// before the uninstall still runs afterwards.
func uninstallOutcome(baseline *installedBaseline, result VerifyResult, verifyErr error) string {
	if baseline == nil || verifyErr != nil || baseline.path == "" || result.ResolvedPath != baseline.path {
		return ""
	}
	return fmt.Sprintf("the uninstall finished, but %s still runs", baseline.path)
}
