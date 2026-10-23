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
	if _, ok := s.officialManagedRecipeFor(plan); ok {
		return s.managedRoot != "" && !plan.Unsupported
	}
	return s.managedRoot != "" && s.installCommands != nil && managedMethods[plan.Method] &&
		plan.Package != "" && !plan.Unsupported &&
		// A managed npm copy installs and runs with the npm and Node on PATH,
		// the daemon's; when that Node is too old, the update keeps validating
		// the installed copy's own Node instead.
		(plan.Method != "npm" || !plan.daemonRuntimeRejected)
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

// managedCLINames fixes the executable a managed install puts in bin/ for
// targets whose resolved binary has another name. OpenCode v2 resolves its own
// opencode-v2 launcher but installs bin/opencode, and the Copilot resolver
// follows the npm loader to its platform binary (copilot-darwin-arm64).
var managedCLINames = map[Target]string{
	TargetOpencodeV2: "opencode",
	TargetCopilot:    "copilot",
}

// managedCLIName is the executable name sessions launch, taken from the
// binary AO resolved before the update. managedCLINames overrides it.
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
		// BUN_INSTALL alone loses to a global or bin directory the user set
		// (env or bunfig), which would install over the copy sessions run.
		return []string{"bun", "install", "-g", spec}, []string{
			"BUN_INSTALL=" + dir,
			"BUN_INSTALL_GLOBAL_DIR=" + filepath.Join(dir, "install", "global"),
			"BUN_INSTALL_BIN=" + filepath.Join(dir, "bin"),
		}
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

const (
	switchFailed = "Installed, but could not switch to it."
	// After current moved, new sessions already run the new release.
	setupFailed  = "Switched to the new version, but could not finish setting it up."
	recordFailed = "Switched to the new version, but could not record it."
)

// runManagedInstall installs the update into a new release directory,
// verifies it, and switches the target's current release to it. Nothing a
// running session uses is touched; on any failure the new directory is
// removed and the current release stays as it was.
func (s *Service) runManagedInstall(ctx context.Context, plan Plan, baseline *installedBaseline, env []string, out io.Writer) error {
	cli := managedCLINames[plan.Target]
	if cli == "" {
		cli = managedCLIName(baseline)
	}
	if cli == "" {
		// A reinstall has no baseline; it repairs the copy AO already manages.
		if record, ok := s.readManagedRecord(plan.Target); ok {
			cli = record.CLI
		}
	}
	if cli == "" {
		return errors.New("AO could not tell which executable sessions run, so it cannot install a managed copy")
	}
	targetDir := s.managedTargetDir(plan.Target)
	versionsDir := filepath.Join(targetDir, "versions")
	if err := os.MkdirAll(versionsDir, 0o750); err != nil {
		return installFailed("Could not create the install folder.", fmt.Errorf("create managed install directory: %w", err))
	}
	name := nextManagedDir(versionsDir)
	dir := filepath.Join(versionsDir, name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return installFailed("Could not create the install folder.", fmt.Errorf("create release directory: %w", err))
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(dir)
		}
	}()

	pkg := packageWithoutLatest(plan.Package)
	pin := plan.managedPin
	if pin == "" && baseline != nil {
		pin = baseline.expected
		if pin == "" {
			pin = baseline.latest
		}
	}
	binRel := managedBinRel(s.goos, plan.Method, cli)
	if recipe, ok := s.officialManagedRecipeFor(plan); ok {
		if err := s.runOfficialManagedInstall(ctx, plan, recipe, pin, dir, env, out); err != nil {
			return err
		}
		binRel = recipe.binRel
	} else {
		argv, extraEnv := managedInstallCommand(plan.Method, pkg, pin, dir)
		if argv == nil {
			return installFailed(fmt.Sprintf("%s cannot install a side-by-side copy.", installMethodLabel(plan.Method)), nil)
		}
		_, _ = fmt.Fprintf(out, "Installing %s into %s\n", pkg, dir)
		if err := s.installCommands.RunInstall(ctx, ports.InstallCommand{Argv: argv, Env: append(append([]string(nil), env...), extraEnv...)}, out, out); err != nil {
			return installFailed("The install command failed.", err)
		}
	}

	binary := filepath.Join(dir, binRel)
	if _, err := os.Stat(binary); err != nil {
		return installFailed(fmt.Sprintf("Installed, but %s was not found.", cli), fmt.Errorf("%s is missing", binary))
	}
	version, err := s.probeManagedVersion(ctx, plan.Target, cli, binary)
	if err != nil {
		return err
	}
	scheme := versionSchemeFor(plan.Target)
	if pin != "" {
		installed, okInstalled := scheme.parse(version)
		wanted, okWanted := scheme.parse(pin)
		if comparison, okCompare := scheme.compare(installed, wanted); okInstalled && okWanted && okCompare && comparison < 0 {
			return installFailed(fmt.Sprintf("The new %s reports %s, not %s.", cli, version, pin), nil)
		}
	}
	previous := s.currentManagedRelease(targetDir)
	// The switch is marked unsettled before current moves, so a daemon that
	// dies before recording it leaves the mark for startup to reconcile.
	marker := filepath.Join(targetDir, unsettledMarker)
	_, statErr := os.Stat(marker)
	wasUnsettled := statErr == nil
	settle := func() {
		// An earlier unsettled switch keeps its mark until its sessions end.
		if !wasUnsettled {
			_ = os.Remove(marker)
		}
	}
	if s.releases != nil && !wasUnsettled {
		if err := writeFileAtomic(marker, []byte(name)); err != nil {
			return installFailed(switchFailed, fmt.Errorf("prepare release switch: %w", err))
		}
	}
	if err := s.switchManagedCurrent(ctx, targetDir, name); err != nil {
		if s.currentManagedRelease(targetDir) == previous {
			settle()
		} else {
			// Windows may have removed current and failed both mklink attempts.
			// Keep the intent and candidate so startup can repair the junction.
			keep = true
		}
		return installFailed(switchFailed, err)
	}
	if s.releases != nil {
		// Still under the harness lock: every launch from here on records this
		// release, and every earlier launch recorded the one it started from.
		// Recorded even if the update is cancelled now, so current and the
		// database never disagree because of the cancel.
		if err := s.releases.SetManagedHarnessRelease(context.WithoutCancel(ctx), domain.AgentHarness(plan.Target), name, time.Now().UTC()); err != nil {
			recordErr := fmt.Errorf("record the new release: %w", err)
			// Launches would run the new files while recording the old
			// release, so switch back before anything launches.
			if rollbackErr := s.restoreManagedCurrent(context.WithoutCancel(ctx), targetDir, previous); rollbackErr != nil {
				// current and the database disagree: the mark stays, and
				// every release is kept until no session that may have
				// launched meanwhile is left.
				keep = true
				return installFailed(recordFailed, errors.Join(recordErr, fmt.Errorf("restore the previous release: %w", rollbackErr)))
			}
			settle()
			return installFailed(switchFailed, recordErr)
		}
		settle()
	}
	keep = true
	record := managedRecord{Method: plan.Method, Package: pkg, CLI: cli, Version: version, Dir: name, BinRel: binRel}
	if err := s.ensureManagedLauncher(plan.Target, record); err != nil {
		return installFailed(setupFailed, err)
	}
	if err := writeFileAtomic(filepath.Join(targetDir, "managed.json"), mustJSON(record)); err != nil {
		return installFailed(setupFailed, fmt.Errorf("record managed install: %w", err))
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
func (s *Service) probeManagedVersion(ctx context.Context, target Target, cli, binary string) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, defaultVerifyTimeout)
	defer cancel()
	var output strings.Builder
	if err := s.commands.Run(probeCtx, []string{binary, "--version"}, &output, &output); err != nil {
		return "", installFailed(fmt.Sprintf("The new %s did not start.", cli), fmt.Errorf("the new copy did not start: %w", err))
	}
	version, ok := versionSchemeFor(target).find(output.String())
	if !ok {
		return "", installFailed(fmt.Sprintf("The new %s did not report its version.", cli), fmt.Errorf("the new copy reported no version: %q", strings.TrimSpace(output.String())))
	}
	return version.display, nil
}

// unsettledMarker, in a target's directory, says an update is switching
// current, or switched it but could neither record the switch nor undo it
// (or the daemon died first), so the releases sessions recorded may not be
// the ones they run. Cleanup keeps every release of that target until none of
// its sessions is left.
const unsettledMarker = ".unsettled"

// ReconcileManagedReleases runs at daemon start, before any launch. A switch
// the daemon died in the middle of left its mark: the release new launches
// record is set to the one current points at, the files they run. The mark
// stays, so launches recorded before this keep every release alive.
func (s *Service) ReconcileManagedReleases(ctx context.Context) error {
	if s.managedRoot == "" || s.releases == nil {
		return nil
	}
	entries, err := os.ReadDir(s.managedRoot)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "bin" {
			continue
		}
		target := Target(entry.Name())
		targetDir := s.managedTargetDir(target)
		intent, err := os.ReadFile(filepath.Join(targetDir, unsettledMarker))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		current := s.currentManagedRelease(targetDir)
		if current == "" {
			// Windows removes the old junction before mklink. A crash in that gap
			// leaves the durable intent, but no launcher target. Prefer its intended
			// installed release, falling back to the last published managed record.
			intended := strings.TrimSpace(string(intent))
			record, ok := s.readManagedRecord(target)
			if !ok {
				return fmt.Errorf("recover %s: missing managed record", target)
			}
			candidates := []string{intended, record.Dir}
			for _, candidate := range candidates {
				if candidate == "" || filepath.Base(candidate) != candidate || !strings.HasPrefix(candidate, "v") {
					continue
				}
				binary := filepath.Join(targetDir, "versions", candidate, record.BinRel)
				if info, err := os.Stat(binary); err != nil || !info.Mode().IsRegular() {
					continue
				}
				if err := s.switchManagedCurrent(ctx, targetDir, candidate); err != nil {
					return fmt.Errorf("recover %s junction: %w", target, err)
				}
				current = candidate
				break
			}
			if current == "" {
				return fmt.Errorf("recover %s: no valid release for missing current", target)
			}
		}
		if err := s.releases.SetManagedHarnessRelease(ctx, domain.AgentHarness(entry.Name()), current, time.Now().UTC()); err != nil {
			return fmt.Errorf("recover %s release: %w", target, err)
		}
	}
	return nil
}

// currentManagedRelease names the release <targetDir>/current points at, or
// "" when there is none yet.
func (s *Service) currentManagedRelease(targetDir string) string {
	current, err := os.Readlink(filepath.Join(targetDir, "current"))
	if err != nil {
		return ""
	}
	return filepath.Base(current)
}

// restoreManagedCurrent points current back at previous after a switch that
// could not be recorded. With no previous release it removes current: nothing
// launched from it, since the launcher is written only after the record.
func (s *Service) restoreManagedCurrent(ctx context.Context, targetDir, previous string) error {
	if previous == "" {
		if err := os.Remove(filepath.Join(targetDir, "current")); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return s.switchManagedCurrent(ctx, targetDir, previous)
}

// switchManagedCurrent points <targetDir>/current at versions/<name>. It runs
// while the update holds the harness, so no session starts mid-switch.
func (s *Service) switchManagedCurrent(ctx context.Context, targetDir, name string) error {
	current := filepath.Join(targetDir, "current")
	if s.goos == "windows" {
		// Symlinks need a privilege on Windows; a junction does not. Replacing
		// it is not atomic, which the harness lock makes harmless.
		previous, _ := os.Readlink(current)
		_ = os.Remove(current)
		err := s.commands.Run(ctx, []string{"cmd", "/c", "mklink", "/J", current, filepath.Join(targetDir, "versions", name)}, io.Discard, io.Discard)
		if err != nil && previous != "" {
			// A failed or cancelled switch must not leave the launcher
			// pointing through a missing current: put the old junction back.
			_ = os.Remove(current)
			if restoreErr := s.commands.Run(context.WithoutCancel(ctx), []string{"cmd", "/c", "mklink", "/J", current, previous}, io.Discard, io.Discard); restoreErr != nil {
				err = errors.Join(err, fmt.Errorf("restore the previous release: %w", restoreErr))
			}
		}
		return err
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
	if err := os.MkdirAll(binDir, 0o750); err != nil {
		return fmt.Errorf("create launcher directory: %w", err)
	}
	launcherCLI := record.CLI
	if target == TargetOpencodeV2 {
		launcherCLI = "opencode-v2"
	}
	if s.goos == "windows" {
		launcher := filepath.Join(binDir, launcherCLI+".cmd")
		script := fmt.Sprintf("@\"%%~dp0..\\%s\\current\\%s\" %%*\r\n", target, record.BinRel)
		return writeFileAtomic(launcher, []byte(script))
	}
	launcher := filepath.Join(binDir, launcherCLI)
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
// current (per managed.json or the current link) nor run by a live session. What a session runs is recorded per
// launch in the database, so a daemon restart, or a Chat that reconnected to
// its old process, never lets a release in use be deleted. A directory a
// process still holds (Windows refuses the delete) is left for the next call.
// Without the release store nothing is deleted.
func (s *Service) RemoveUnusedManagedReleases(ctx context.Context) {
	if s.managedRoot == "" || s.releases == nil {
		return
	}
	processes, err := s.snapshotManagedProcesses(ctx)
	if err != nil {
		// Unknown process state is never proof that an old release is unused.
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
		gate := s.harnessGate(domain.AgentHarness(target))
		if !gate.TryLock() {
			continue
		}
		// Read with launches and updates held off, so neither the current
		// release nor what sessions run can change before the deletion.
		record, ok := s.readManagedRecord(target)
		if !ok {
			gate.Unlock()
			continue
		}
		inUse, err := s.releases.ListHarnessReleasesInUse(ctx, domain.AgentHarness(target))
		if err != nil {
			gate.Unlock()
			continue
		}
		// Reviewers launch outside sessions, so no release row says which
		// copy they run; keep every release until none is live.
		sessions, reviewers, err := s.harnessInUse(ctx, target)
		if err != nil || len(reviewers) > 0 {
			gate.Unlock()
			continue
		}
		// After a switch that was neither recorded nor undone, the release
		// rows of launches since then cannot be trusted; wait for those
		// sessions to end.
		marker := filepath.Join(s.managedTargetDir(target), unsettledMarker)
		if _, err := os.Stat(marker); err == nil {
			if len(sessions) > 0 {
				gate.Unlock()
				continue
			}
			_ = os.Remove(marker)
		}
		// current can be ahead of managed.json when an update switched it but
		// failed before writing the file; it is what launches run, so it is
		// kept too. Without it, nothing is known to be safe.
		current, err := os.Readlink(filepath.Join(s.managedTargetDir(target), "current"))
		if err != nil {
			gate.Unlock()
			continue
		}
		keep := map[string]bool{record.Dir: true, filepath.Base(current): true}
		for _, release := range inUse {
			keep[release] = true
		}
		versionsDir := filepath.Join(s.managedTargetDir(target), "versions")
		releases, _ := os.ReadDir(versionsDir)
		names := make([]string, 0, len(releases))
		for _, release := range releases {
			if !keep[release.Name()] && !s.managedReleaseHasProcess(processes, target, record, release.Name()) {
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
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
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
