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

type ManagedReleaseStore interface {
	SetManagedHarnessRelease(ctx context.Context, harness domain.AgentHarness, release string, at time.Time) error
	ListHarnessReleasesInUse(ctx context.Context, harness domain.AgentHarness) ([]string, error)
	PruneSessionHarnessReleases(ctx context.Context) error
}

var managedMethods = map[string]bool{"npm": true, "bun": true, "uv": true, "pipx": true}

type managedRecord struct {
	Method  string `json:"method"`
	Package string `json:"package"`
	CLI     string `json:"cli"`
	Version string `json:"version"`
	Dir     string `json:"dir"`
	BinRel  string `json:"binRel"`
}

func (s *Service) ManagedBinDir() string {
	if s.managedRoot == "" {
		return ""
	}
	return filepath.Join(s.managedRoot, "bin")
}

func (s *Service) SetBinaryInvalidator(invalidate func(Target)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidateBinary = invalidate
}

func (s *Service) managedEligible(plan Plan) bool {
	if _, ok := s.officialManagedRecipeFor(plan); ok {
		return s.managedRoot != "" && !plan.Unsupported
	}
	return s.managedRoot != "" && s.installCommands != nil && managedMethods[plan.Method] &&
		plan.Package != "" && !plan.Unsupported &&
		(plan.Method != "npm" || !plan.daemonRuntimeRejected)
}

func (s *Service) managedTargetDir(target Target) string {
	return filepath.Join(s.managedRoot, string(target))
}

func (s *Service) isManagedPath(path string) bool {
	if s.managedRoot == "" || path == "" {
		return false
	}
	root, err := filepath.EvalSymlinks(s.managedRoot)
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, resolved)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

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

func (s *Service) managedRecordMatchesPlan(record managedRecord, plan Plan) bool {
	method := plan.ownershipMethod()
	return record.Method == method || plan.Target == TargetOMP && method == "bun" &&
		record.Method == "official-installer" && (s.goos == "darwin" || s.goos == "linux")
}

var managedCLINames = map[Target]string{
	TargetOpencodeV2: "opencode",
	TargetCopilot:    "copilot",
}

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

func managedBinRel(goos, method, cli string) string {
	if goos == "windows" {
		if method == "npm" {
			return cli + ".cmd"
		}
		return filepath.Join("bin", cli+".exe")
	}
	return filepath.Join("bin", cli)
}

func managedInstallCommand(method, pkg, pin, dir string) (argv, env []string) {
	switch method {
	case "npm":
		spec := pkg + "@latest"
		if pin != "" {
			spec = pkg + "@" + pin
		}
		return []string{"npm", "install", "-g", "--prefix", dir, "--allow-scripts=" + pkg, spec}, nil
	case "bun":
		spec := pkg + "@latest"
		if pin != "" {
			spec = pkg + "@" + pin
		}
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
	setupFailed  = "Switched to the new version, but could not finish setting it up."
	recordFailed = "Switched to the new version, but could not record it."
)

func (s *Service) runManagedInstall(ctx context.Context, plan Plan, baseline *installedBaseline, env []string, out io.Writer) error {
	cli := managedCLINames[plan.Target]
	if cli == "" {
		cli = managedCLIName(baseline)
	}
	if cli == "" {
		if record, ok := s.readManagedRecord(plan.Target); ok {
			cli = record.CLI
		}
	}
	if cli == "" {
		return errors.New("cannot install a managed copy: Orchestrator could not tell which executable sessions run")
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
	marker := filepath.Join(targetDir, unsettledMarker)
	_, statErr := os.Stat(marker)
	wasUnsettled := statErr == nil
	settle := func() {
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
			keep = true
		}
		return installFailed(switchFailed, err)
	}
	if s.releases != nil {
		if err := s.releases.SetManagedHarnessRelease(context.WithoutCancel(ctx), domain.AgentHarness(plan.Target), name, time.Now().UTC()); err != nil {
			recordErr := fmt.Errorf("record the new release: %w", err)
			if rollbackErr := s.restoreManagedCurrent(context.WithoutCancel(ctx), targetDir, previous); rollbackErr != nil {
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
	_, _ = fmt.Fprintf(out, "Orchestrator now runs %s %s from %s\n", cli, version, dir)
	s.mu.Lock()
	invalidate := s.invalidateBinary
	s.mu.Unlock()
	if invalidate != nil {
		invalidate(plan.Target)
	}
	return nil
}

func (s *Service) probeManagedVersion(ctx context.Context, target Target, cli, binary string) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, defaultVerifyTimeout)
	defer cancel()
	output := &capturedOutput{max: maxOutputBytes}
	if err := s.commands.Run(probeCtx, []string{binary, "--version"}, output, output); err != nil {
		detail := fmt.Errorf("the new copy did not start: %w", err)
		if text := strings.TrimSpace(output.String()); text != "" {
			detail = fmt.Errorf("%w\n%s", detail, text)
		}
		return "", installFailed(fmt.Sprintf("The new %s did not start.", cli), detail)
	}
	version, ok := versionSchemeFor(target).find(output.String())
	if !ok {
		return "", installFailed(fmt.Sprintf("The new %s did not report its version.", cli), fmt.Errorf("the new copy reported no version: %q", strings.TrimSpace(output.String())))
	}
	return version.display, nil
}

const unsettledMarker = ".unsettled"

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

func (s *Service) currentManagedRelease(targetDir string) string {
	current, err := os.Readlink(filepath.Join(targetDir, "current"))
	if err != nil {
		return ""
	}
	return filepath.Base(current)
}

func (s *Service) restoreManagedCurrent(ctx context.Context, targetDir, previous string) error {
	if previous == "" {
		if err := os.Remove(filepath.Join(targetDir, "current")); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return s.switchManagedCurrent(ctx, targetDir, previous)
}

func (s *Service) switchManagedCurrent(ctx context.Context, targetDir, name string) error {
	current := filepath.Join(targetDir, "current")
	if s.goos == "windows" {
		previous, _ := os.Readlink(current)
		_ = os.Remove(current)
		err := s.commands.Run(ctx, []string{"cmd", "/c", "mklink", "/J", current, filepath.Join(targetDir, "versions", name)}, io.Discard, io.Discard)
		if err != nil && previous != "" {
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

func (s *Service) RemoveUnusedManagedReleases(ctx context.Context) {
	if s.managedRoot == "" || s.releases == nil {
		return
	}
	processes, err := s.snapshotManagedProcesses(ctx)
	if err != nil {
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
		sessions, reviewers, err := s.harnessInUse(ctx, target)
		if err != nil || len(reviewers) > 0 {
			gate.Unlock()
			continue
		}
		marker := filepath.Join(s.managedTargetDir(target), unsettledMarker)
		if _, err := os.Stat(marker); err == nil {
			if len(sessions) > 0 {
				gate.Unlock()
				continue
			}
			_ = os.Remove(marker)
		}
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
