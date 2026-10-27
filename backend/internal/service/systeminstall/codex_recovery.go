package systeminstall

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrInstallationChanged refuses to update a different installation than the
// one approved by the caller or captured before an installer wait.
var ErrInstallationChanged = errors.New("installation changed; refresh and try again")

// StartAgentUpdate binds a user-initiated update to the advisory's installation.
// An empty identity preserves compatibility with older clients; the worker
// still rechecks the installation it selected before executing any command.
func (s *Service) StartAgentUpdate(ctx context.Context, target Target, method, expectedVersion, expectedInstallation string) (Job, error) {
	return s.startAgentOperation(ctx, target, method, AgentOperationUpdate, expectedVersion, expectedInstallation)
}

func installationIdentity(ctx context.Context, target Target, result VerifyResult, plan Plan) string {
	if ctx.Err() != nil || result.ResolvedPath == "" {
		return ""
	}
	identity := identifyBinary(result.ResolvedPath)
	version, _ := versionSchemeFor(target).find(installedVersionText(target, result.ResolvedPath, result.Output))
	return fmt.Sprintf("%x", sha256.Sum256(fmt.Appendf(nil, "%s\x00%+v\x00%s\x00%s\x00%s", target, identity, version.display, plan.Method, packageWithoutLatest(plan.Package))))
}

func (s *Service) recheckUpdateInstallation(ctx context.Context, plan Plan, baseline *installedBaseline) error {
	if baseline == nil || baseline.identity == "" || s.verifier == nil {
		return nil
	}
	verified, _ := s.verifier.Verify(ctx, plan.Target)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if installationIdentity(ctx, plan.Target, verified, plan) != baseline.identity || !s.methodOwnsBinary(ctx, plan, verified.ResolvedPath) {
		return ErrInstallationChanged
	}
	return nil
}

// nativeCodexUpdateHome proves the selected executable is in a standalone
// installation and advertises its native updater. A random executable outside
// package-manager directories is not evidence of vendor ownership.
func (s *Service) nativeCodexUpdateHome(ctx context.Context, binary string) (string, bool) {
	if ctx.Err() != nil {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(binary)
	if err != nil {
		return "", false
	}
	parts := strings.Split(filepath.ToSlash(resolved), "/")
	for index := 1; index+2 < len(parts); index++ {
		if parts[index] != "packages" || parts[index+1] != "standalone" {
			continue
		}
		home := filepath.FromSlash(strings.Join(parts[:index], "/"))
		info, err := os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() || !filepath.IsAbs(home) {
			return "", false
		}
		output, err := runProbe(ctx, s.commands, localProbeTimeout, false, []string{binary, "--help"})
		if err != nil {
			return "", false
		}
		for _, line := range strings.Split(output, "\n") {
			if fields := strings.Fields(line); len(fields) > 0 && fields[0] == "update" {
				return home, true
			}
		}
		return "", false
	}
	return "", false
}

func (s *Service) vendorMaintenanceSupported(ctx context.Context, target Target, path, home string) bool {
	if !vendorMaintainable(s.goos, target, path, home) {
		return false
	}
	if target == TargetCodex {
		_, ok := s.nativeCodexUpdateHome(ctx, path)
		return ok
	}
	return true
}

// lockInstaller serializes mutable package-manager scopes. Managed releases
// have independent destination directories and retain their per-harness gate.
func (s *Service) lockInstaller(ctx context.Context, plan Plan, job *Job) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if plan.managed {
		return func() {}, nil
	}
	var scope string
	switch plan.Method {
	case "npm":
		prefix := filepath.Clean(plan.PackagePrefix)
		if resolved, err := filepath.EvalSymlinks(prefix); err == nil {
			prefix = resolved
		}
		scope = "npm:" + prefix
	case "homebrew", "bun", "uv", "pipx":
		tool := plan.Method
		if tool == "homebrew" {
			tool = "brew"
		}
		path, err := s.executables.LookPath(tool)
		if err != nil {
			return nil, err
		}
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = resolved
		}
		scope = plan.Method + ":" + path
	default:
		return func() {}, nil
	}
	if s.goos == "windows" {
		scope = strings.ToLower(strings.ReplaceAll(scope, `\`, "/"))
	}
	s.mu.Lock()
	if s.installerGates == nil {
		s.installerGates = make(map[string]chan struct{})
	}
	gate := s.installerGates[scope]
	if gate == nil {
		gate = make(chan struct{}, 1)
		s.installerGates[scope] = gate
	}
	s.mu.Unlock()
	select {
	case gate <- struct{}{}:
		return func() { <-gate }, nil
	default:
	}
	if err := s.transitionAgentJob(job, StatusRunning, "Waiting for another installer operation to finish.", "", ""); err != nil {
		return nil, err
	}
	select {
	case gate <- struct{}{}:
		if err := s.transitionAgentJob(job, StatusInstalling, "", "", ""); err != nil {
			<-gate
			return nil, err
		}
		return func() { <-gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
