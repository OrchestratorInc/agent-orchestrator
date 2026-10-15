package systeminstall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// AO-managed versioned installs.
//
// npm, Bun, uv and pipx update a package by rewriting its directory in place,
// so a session still running the old release can fail on its next lazy load
// (a JS chunk, a bundled helper such as ripgrep, a Python module), and on
// Windows the rewrite fails outright while the release runs. AO therefore
// installs each update into a fresh directory of its own and switches to it
// atomically, the way Claude Code's native installer keeps each release apart:
//
//	<root>/<target>/versions/v12/   one release, never modified after install
//	<root>/<target>/current          → versions/v12 (symlink; a junction on Windows)
//	<root>/<target>/managed.json     method, package, CLI name and version
//	<root>/bin/<cli>                 launcher → ../<target>/current/<bin>
//
// Harness resolvers check <root>/bin first (binaryutil.ManagedBinary), so AO's
// sessions start the current release, while a running session keeps the
// files of the release it started from until it moves and the old directory
// is removed. The user's own install, on PATH for their terminal, is left alone.

// ManagedReleaseStore records which managed release each session launch runs.
// Launches record the harness's current release themselves (a database
// trigger), so the store only needs the current release and the ones in use.
type ManagedReleaseStore interface {
	SetManagedHarnessRelease(ctx context.Context, harness domain.AgentHarness, release string, at time.Time) error
	ListHarnessReleasesInUse(ctx context.Context, harness domain.AgentHarness) ([]string, error)
	PruneSessionHarnessReleases(ctx context.Context) error
}

// managedMethods are the update methods AO installs side by side.
var managedMethods = map[string]bool{"npm": true, "bun": true, "uv": true, "pipx": true}

// managedRecord is <root>/<target>/managed.json.
type managedRecord struct {
	Method  string `json:"method"`
	Package string `json:"package"`
	CLI     string `json:"cli"`
	Version string `json:"version"`
	Dir     string `json:"dir"`
	BinRel  string `json:"binRel"`
}

// ManagedBinDir is where AO's managed launchers live, or "" when managed
// installs are off. The daemon passes it to binaryutil.SetManagedBinDir.
func (s *Service) ManagedBinDir() string {
	if s.managedRoot == "" {
		return ""
	}
	return filepath.Join(s.managedRoot, "bin")
}

// SetBinaryInvalidator late-binds the callback that makes a harness adapter
// resolve its executable again, so the first managed install is picked up
// without a daemon restart.
func (s *Service) SetBinaryInvalidator(invalidate func(Target)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidateBinary = invalidate
}

// managedEligible reports whether an update through plan installs side by side.
func (s *Service) managedEligible(plan Plan) bool {
	return s.managedRoot != "" && s.installCommands != nil && managedMethods[plan.Method] &&
		plan.Package != "" && plan.Target != TargetOpencodeV2 && !plan.Unsupported
}

func (s *Service) managedTargetDir(target Target) string {
	return filepath.Join(s.managedRoot, string(target))
}

// isManagedPath reports whether path is one of AO's managed launchers or
// installs, which no package manager's ownership check recognises.
func (s *Service) isManagedPath(path string) bool {
	if s.managedRoot == "" || path == "" {
		return false
	}
	rel, err := filepath.Rel(s.managedRoot, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// readManagedRecord returns target's managed install, if AO manages it.
func (s *Service) readManagedRecord(target Target) (managedRecord, bool) {
	if s.managedRoot == "" {
		return managedRecord{}, false
	}
	data, err := os.ReadFile(filepath.Join(s.managedTargetDir(target), "managed.json"))
	if err != nil {
		return managedRecord{}, false
	}
	var record managedRecord
	if json.Unmarshal(data, &record) != nil || record.Method == "" || record.Dir == "" {
		return managedRecord{}, false
	}
	return record, true
}

// managedCLIName is the executable name sessions launch, taken from the
// binary AO resolved before the update.
func managedCLIName(baseline *installedBaseline) string {
	if baseline == nil || baseline.path == "" {
		return ""
	}
	name := filepath.Base(baseline.path)
	for _, ext := range []string{".cmd", ".exe", ".bat", ".ps1"} {
		name = strings.TrimSuffix(name, ext)
	}
	return name
}

// managedBinRel is where method puts the CLI inside one release directory.
func managedBinRel(goos, method, cli string) string {
	if goos == "windows" {
		if method == "npm" {
			return cli + ".cmd"
		}
		return filepath.Join("bin", cli+".exe")
	}
	return filepath.Join("bin", cli)
}

// managedInstallCommand builds the install of pkgSpec into dir for method.
func managedInstallCommand(method, pkg, pin, dir string) (argv, env []string) {
	switch method {
	case "npm":
		spec := pkg + "@latest"
		if pin != "" {
			spec = pkg + "@" + pin
		}
		// npm 12 skips install scripts by default and still exits 0, which
		// leaves a package that finishes installing in postinstall broken.
		return []string{"npm", "install", "-g", "--prefix", dir, "--allow-scripts=" + pkg, spec}, nil
	case "bun":
		spec := pkg + "@latest"
		if pin != "" {
			spec = pkg + "@" + pin
		}
		return []string{"bun", "install", "-g", spec}, []string{"BUN_INSTALL=" + dir}
	case "uv":
		spec := pkg
		if pin != "" {
			spec = pkg + "==" + pin
		}
		return []string{"uv", "tool", "install", "--force", spec},
			[]string{"UV_TOOL_DIR=" + filepath.Join(dir, "tools"), "UV_TOOL_BIN_DIR=" + filepath.Join(dir, "bin")}
	case "pipx":
		spec := pkg
		if pin != "" {
			spec = pkg + "==" + pin
		}
		return []string{"pipx", "install", "--force", spec},
			[]string{"PIPX_HOME=" + filepath.Join(dir, "pipx"), "PIPX_BIN_DIR=" + filepath.Join(dir, "bin")}
	}
	return nil, nil
}

// nextManagedDir allocates the next unused release directory name (v1, v2, …).
// Short names keep Windows paths well under MAX_PATH.
func nextManagedDir(versionsDir string) string {
	highest := 0
	entries, _ := os.ReadDir(versionsDir)
	for _, entry := range entries {
		if n, err := strconv.Atoi(strings.TrimPrefix(entry.Name(), "v")); err == nil && strings.HasPrefix(entry.Name(), "v") && n > highest {
			highest = n
		}
	}
	return "v" + strconv.Itoa(highest+1)
}

// runManagedInstall installs the update into a new release directory,
// verifies it, and switches the target's current release to it. Nothing a
// running session uses is touched; on any failure the new directory is
// removed and the current release stays as it was.
func (s *Service) runManagedInstall(ctx context.Context, plan Plan, baseline *installedBaseline, env []string, out io.Writer) error {
	cli := managedCLIName(baseline)
	if cli == "" {
		return errors.New("AO could not tell which executable sessions run, so it cannot install a managed copy")
	}
	targetDir := s.managedTargetDir(plan.Target)
	versionsDir := filepath.Join(targetDir, "versions")
	if err := os.MkdirAll(versionsDir, 0o755); err != nil {
		return fmt.Errorf("create managed install directory: %w", err)
	}
	name := nextManagedDir(versionsDir)
	dir := filepath.Join(versionsDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create release directory: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(dir)
		}
	}()

	pkg := packageWithoutLatest(plan.Package)
	pin := ""
	if baseline != nil {
		pin = baseline.expected
		if pin == "" {
			pin = baseline.latest
		}
	}
	argv, extraEnv := managedInstallCommand(plan.Method, pkg, pin, dir)
	if argv == nil {
		return fmt.Errorf("%s cannot install a managed copy", installMethodLabel(plan.Method))
	}
	_, _ = fmt.Fprintf(out, "Installing %s into %s\n", pkg, dir)
	if err := s.installCommands.RunInstall(ctx, ports.InstallCommand{Argv: argv, Env: append(append([]string(nil), env...), extraEnv...)}, out, out); err != nil {
		return err
	}

	binRel := managedBinRel(s.goos, plan.Method, cli)
	binary := filepath.Join(dir, binRel)
	if _, err := os.Stat(binary); err != nil {
		return fmt.Errorf("the install finished but %s is missing", binary)
	}
	version, err := s.probeManagedVersion(ctx, plan.Target, binary)
	if err != nil {
		return err
	}
	scheme := versionSchemeFor(plan.Target)
	if pin != "" {
		installed, okInstalled := scheme.parse(version)
		wanted, okWanted := scheme.parse(pin)
		if comparison, comparable := scheme.compare(installed, wanted); okInstalled && okWanted && comparable && comparison < 0 {
			return fmt.Errorf("the new copy reports %s, not %s", version, pin)
		}
	}
	if err := s.switchManagedCurrent(ctx, targetDir, name); err != nil {
		return err
	}
	keep = true
	if s.releases != nil {
		// Still under the harness lock: every launch from here on records this
		// release, and every earlier launch recorded the one it started from.
		if err := s.releases.SetManagedHarnessRelease(ctx, domain.AgentHarness(plan.Target), name, time.Now().UTC()); err != nil {
			return fmt.Errorf("record the new release: %w", err)
		}
	}
	record := managedRecord{Method: plan.Method, Package: pkg, CLI: cli, Version: version, Dir: name, BinRel: binRel}
	if err := s.ensureManagedLauncher(plan.Target, record); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(targetDir, "managed.json"), mustJSON(record)); err != nil {
		return fmt.Errorf("record managed install: %w", err)
	}
	_, _ = fmt.Fprintf(out, "AO now runs %s %s from %s\n", cli, version, dir)
	s.mu.Lock()
	invalidate := s.invalidateBinary
	s.mu.Unlock()
	if invalidate != nil {
		invalidate(plan.Target)
	}
	return nil
}

// probeManagedVersion runs the new copy's --version.
func (s *Service) probeManagedVersion(ctx context.Context, target Target, binary string) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, defaultVerifyTimeout)
	defer cancel()
	var output strings.Builder
	if err := s.commands.Run(probeCtx, []string{binary, "--version"}, &output, &output); err != nil {
		return "", fmt.Errorf("the new copy did not start: %w", err)
	}
	version, ok := versionSchemeFor(target).find(output.String())
	if !ok {
		return "", fmt.Errorf("the new copy reported no version: %q", strings.TrimSpace(output.String()))
	}
	return version.display, nil
}

// switchManagedCurrent points <targetDir>/current at versions/<name>. It runs
// while the update holds the harness, so no session starts mid-switch.
func (s *Service) switchManagedCurrent(ctx context.Context, targetDir, name string) error {
	current := filepath.Join(targetDir, "current")
	if s.goos == "windows" {
		// Symlinks need a privilege on Windows; a junction does not. Replacing
		// it is not atomic, which the harness lock makes harmless.
		_ = os.Remove(current)
		return s.commands.Run(ctx, []string{"cmd", "/c", "mklink", "/J", current, filepath.Join(targetDir, "versions", name)}, io.Discard, io.Discard)
	}
	tmp := filepath.Join(targetDir, ".current-"+name)
	_ = os.Remove(tmp)
	if err := os.Symlink(filepath.Join("versions", name), tmp); err != nil {
		return fmt.Errorf("prepare release switch: %w", err)
	}
	if err := os.Rename(tmp, current); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("switch release: %w", err)
	}
	return nil
}

// ensureManagedLauncher creates <root>/bin/<cli>, a stable path into the
// current release. It never changes once written, so resolvers may cache it.
func (s *Service) ensureManagedLauncher(target Target, record managedRecord) error {
	binDir := s.ManagedBinDir()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("create launcher directory: %w", err)
	}
	if s.goos == "windows" {
		launcher := filepath.Join(binDir, record.CLI+".cmd")
		script := fmt.Sprintf("@\"%%~dp0..\\%s\\current\\%s\" %%*\r\n", target, record.BinRel)
		return writeFileAtomic(launcher, []byte(script))
	}
	launcher := filepath.Join(binDir, record.CLI)
	want := filepath.Join("..", string(target), "current", record.BinRel)
	if existing, err := os.Readlink(launcher); err == nil && existing == want {
		return nil
	}
	tmp := launcher + ".tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink(want, tmp); err != nil {
		return fmt.Errorf("create launcher: %w", err)
	}
	return os.Rename(tmp, launcher)
}

// RemoveUnusedManagedReleases deletes every release directory that is neither
// current nor run by a live session. What a session runs is recorded per
// launch in the database, so a daemon restart, or a Chat that reconnected to
// its old process, never lets a release in use be deleted. A directory a
// process still holds (Windows refuses the delete) is left for the next call.
// Without the release store nothing is deleted.
func (s *Service) RemoveUnusedManagedReleases(ctx context.Context) {
	if s.managedRoot == "" || s.releases == nil {
		return
	}
	_ = s.releases.PruneSessionHarnessReleases(ctx)
	entries, err := os.ReadDir(s.managedRoot)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "bin" || ctx.Err() != nil {
			continue
		}
		target := Target(entry.Name())
		record, ok := s.readManagedRecord(target)
		if !ok {
			continue
		}
		gate := s.harnessGate(domain.AgentHarness(target))
		if !gate.TryLock() {
			continue
		}
		// Read with launches held off, so none can start on a release between
		// this check and its deletion.
		inUse, err := s.releases.ListHarnessReleasesInUse(ctx, domain.AgentHarness(target))
		if err != nil {
			gate.Unlock()
			continue
		}
		keep := map[string]bool{record.Dir: true}
		for _, release := range inUse {
			keep[release] = true
		}
		versionsDir := filepath.Join(s.managedTargetDir(target), "versions")
		releases, _ := os.ReadDir(versionsDir)
		names := make([]string, 0, len(releases))
		for _, release := range releases {
			if !keep[release.Name()] {
				names = append(names, release.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			_ = os.RemoveAll(filepath.Join(versionsDir, name))
		}
		gate.Unlock()
	}
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func mustJSON(value any) []byte {
	data, _ := json.MarshalIndent(value, "", "  ")
	return data
}
