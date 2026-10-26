package systeminstall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestUpdateAdvisoryComparesKnownNPMInstallationAndCachesResult(t *testing.T) {
	s := newTestService("darwin", "npm")
	s.jobs[TargetCodex] = &Job{Target: TargetCodex, Status: StatusSucceeded, Method: "npm"}
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/opt/bin/codex", Output: "codex-cli 1.2.3\n"}, nil
	})
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return true, nil }
	calls := 0
	var gotMethod, gotPackage string
	var gotCask bool
	s.managedVersion = func(_ context.Context, plan Plan) (string, error) {
		calls++
		gotMethod, gotPackage, gotCask = plan.Method, packageWithoutLatest(plan.Package), plan.PackageCask
		return "1.3.0", nil
	}
	for range 2 {
		advisory, err := s.UpdateAdvisory(context.Background(), TargetCodex, false)
		if err != nil {
			t.Fatal(err)
		}
		if advisory.Status != UpdateStatusBehindLatest || advisory.CurrentVersion != "1.2.3" || advisory.LatestVersion != "1.3.0" || advisory.Source != "npm" || advisory.MaintenanceMethod != "npm" || advisory.Reason != "" {
			t.Fatalf("advisory = %+v", advisory)
		}
	}
	if calls != 1 {
		t.Fatalf("latest lookup calls = %d, want cached 1", calls)
	}
	if gotMethod != "npm" || gotPackage != "@openai/codex" || gotCask {
		t.Fatalf("lookup = %s %s cask=%t", gotMethod, gotPackage, gotCask)
	}
}

func TestOfficialReleaseExposesVendorMaintenanceOnlyOutsidePackageTools(t *testing.T) {
	for _, tt := range []struct {
		name   string
		target Target
		path   string
		output string
		method string
	}{
		{"native Claude", TargetClaudeCode, ".local/share/claude/versions/2.1.291", "Claude Code 2.1.291", "official-installer"},
		{"Claude anywhere outside package tools", TargetClaudeCode, "opt/bin/claude", "Claude Code 2.1.291", "official-installer"},
		{"Grok download", TargetGrok, ".grok/downloads/grok-1.0.13-macos-aarch64", "grok 1.0.13", "official-installer"},
		{"Pi managed launcher", TargetPi, ".pi/agent/bin/pi", "0.84.4", "official-installer"},
		{"inside Homebrew", TargetClaudeCode, "opt/homebrew/Cellar/claude/.local/share/claude/versions/2.1.291", "Claude Code 2.1.291", ""},
		{"behind a version manager", TargetGrok, ".asdf/shims/grok", "grok 1.0.13", ""},
		{"no self-update command", TargetMuse, ".local/bin/muse", "Muse Code 1.4.2 (1.4.2-R4900.1)", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), filepath.FromSlash(tt.path))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, nil, 0o755); err != nil {
				t.Fatal(err)
			}
			s := newTestService("darwin", "bash", "sh")
			s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
				return VerifyResult{ResolvedPath: path, Output: tt.output}, nil
			})
			s.officialVersion = func(context.Context, Target) (string, error) { return "9.9.9", nil }
			advisory, err := s.UpdateAdvisory(context.Background(), tt.target, false)
			if err != nil || advisory.MaintenanceMethod != tt.method {
				t.Fatalf("advisory=%+v err=%v, want maintenance %q", advisory, err, tt.method)
			}
		})
	}
}

func TestUpdateAdvisoryExplicitRefreshReprobesCurrentBinary(t *testing.T) {
	s := newTestService("darwin", "npm")
	version := "1.2.3"
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/opt/bin/codex", Output: version}, nil
	})
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return true, nil }
	s.managedVersion = fixedManagedVersion("1.3.0", nil)
	if result, err := s.UpdateAdvisory(context.Background(), TargetCodex, false); err != nil || result.Status != UpdateStatusBehindLatest {
		t.Fatalf("initial=%+v err=%v", result, err)
	}
	version = "1.3.0"
	if result, err := s.UpdateAdvisory(context.Background(), TargetCodex, true); err != nil || result.Status != UpdateStatusCurrent || result.CurrentVersion != "1.3.0" {
		t.Fatalf("fresh=%+v err=%v", result, err)
	}
}

func TestUpdateAdvisoryRefreshKeepsServingPreviousResult(t *testing.T) {
	s := newTestService("darwin", "npm")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/opt/bin/codex", Output: "1.2.3"}, nil
	})
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return true, nil }
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	s.managedVersion = func(context.Context, Plan) (string, error) {
		started <- struct{}{}
		<-release
		return "1.4.0", nil
	}
	previous := UpdateAdvisory{AgentID: string(TargetCodex), Status: UpdateStatusBehindLatest, CurrentVersion: "1.2.3", LatestVersion: "1.3.0", CheckedAt: time.Now().Add(-time.Hour)}
	s.updateAdvisories = map[Target]UpdateAdvisory{TargetCodex: previous}
	refreshed := make(chan UpdateAdvisory, 1)
	go func() {
		advisory, _ := s.UpdateAdvisory(context.Background(), TargetCodex, true)
		refreshed <- advisory
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start a check")
	}
	if advisory, err := s.UpdateAdvisory(context.Background(), TargetCodex, false); err != nil || advisory != previous {
		t.Fatalf("read during refresh = %+v err=%v, want previous %+v", advisory, err, previous)
	}
	close(release)
	if advisory := <-refreshed; advisory.LatestVersion != "1.4.0" {
		t.Fatalf("refresh = %+v, want latest 1.4.0", advisory)
	}
	if advisory, err := s.UpdateAdvisory(context.Background(), TargetCodex, false); err != nil || advisory.LatestVersion != "1.4.0" {
		t.Fatalf("read after refresh = %+v err=%v", advisory, err)
	}
}

func TestUpdateAdvisoryPageRequestCanCancelWhileStartupJoinsSameCheck(t *testing.T) {
	s := newTestService("darwin", "npm")
	s.jobs[TargetCodex] = &Job{Target: TargetCodex, Status: StatusSucceeded, Method: "npm"}
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/opt/bin/codex", Output: "codex 1.2.3"}, nil
	})
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return true, nil }
	started := make(chan struct{})
	release := make(chan struct{})
	var lookups atomic.Int32
	s.managedVersion = func(context.Context, Plan) (string, error) {
		if lookups.Add(1) == 1 {
			close(started)
		}
		<-release
		return "1.3.0", nil
	}
	pageCtx, cancelPage := context.WithCancel(context.Background())
	pageDone := make(chan error, 1)
	go func() {
		_, err := s.UpdateAdvisory(pageCtx, TargetCodex, false)
		pageDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("page update check did not start")
	}
	cancelPage()
	if err := <-pageDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled page request error = %v, want context.Canceled", err)
	}
	startupDone := make(chan UpdateAdvisory, 1)
	go func() {
		advisory, _ := s.UpdateAdvisory(context.Background(), TargetCodex, false)
		startupDone <- advisory
	}()
	select {
	case <-startupDone:
		t.Fatal("startup call returned before shared version lookup completed")
	case <-time.After(50 * time.Millisecond):
	}
	if count := lookups.Load(); count != 1 {
		t.Fatalf("latest lookup calls = %d, want one shared call", count)
	}
	close(release)
	select {
	case advisory := <-startupDone:
		if advisory.Status != UpdateStatusBehindLatest {
			t.Fatalf("startup advisory = %+v", advisory)
		}
	case <-time.After(time.Second):
		t.Fatal("startup call did not finish")
	}
}

func TestUpdateAdvisoryRunsAtMostTwoChecksAtOnce(t *testing.T) {
	s := newTestService("darwin")
	var running, peak, verified atomic.Int32
	release := make(chan struct{})
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		now := running.Add(1)
		for {
			old := peak.Load()
			if now <= old || peak.CompareAndSwap(old, now) {
				break
			}
		}
		verified.Add(1)
		<-release
		running.Add(-1)
		return VerifyResult{ResolvedPath: "/opt/bin/harness", Output: "1.0.0"}, nil
	})
	s.officialVersion = func(context.Context, Target) (string, error) { return "1.0.0", nil }
	targets := []Target{TargetCodex, TargetClaudeCode, TargetGrok, TargetPi, TargetMuse, TargetGemini}
	done := make(chan error, len(targets))
	for _, target := range targets {
		go func() {
			_, err := s.UpdateAdvisory(context.Background(), target, false)
			done <- err
		}()
	}
	deadline := time.After(time.Second)
	for verified.Load() < maxConcurrentUpdateChecks {
		select {
		case <-deadline:
			t.Fatalf("only %d checks started", verified.Load())
		case <-time.After(time.Millisecond):
		}
	}
	time.Sleep(50 * time.Millisecond)
	if got := verified.Load(); got != maxConcurrentUpdateChecks {
		t.Fatalf("checks started while slots were full = %d, want %d", got, maxConcurrentUpdateChecks)
	}
	close(release)
	for range targets {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("update checks did not finish")
		}
	}
	if got := peak.Load(); got != maxConcurrentUpdateChecks {
		t.Fatalf("peak concurrent checks = %d, want %d", got, maxConcurrentUpdateChecks)
	}
	if got := verified.Load(); got != int32(len(targets)) {
		t.Fatalf("checks run = %d, want %d", got, len(targets))
	}
}

func TestUpdateAdvisoryServesOldResultsAndRetriesOnlyStaleLookupFailures(t *testing.T) {
	now := time.Now().UTC()
	for _, tt := range []struct {
		name       string
		cached     UpdateAdvisory
		wantChecks int32
	}{
		{"definitive result from days ago", UpdateAdvisory{Status: UpdateStatusCurrent, CheckedAt: now.Add(-72 * time.Hour)}, 0},
		{"permanent unknown from days ago", UpdateAdvisory{Status: UpdateStatusUnknown, Reason: UpdateReasonUnsupportedSource, CheckedAt: now.Add(-72 * time.Hour)}, 0},
		{"recent lookup failure", UpdateAdvisory{Status: UpdateStatusUnknown, Reason: UpdateReasonLookupFailed, CheckedAt: now.Add(-lookupFailedRetryAfter + time.Minute)}, 0},
		{"stale lookup failure", UpdateAdvisory{Status: UpdateStatusUnknown, Reason: UpdateReasonLookupFailed, CheckedAt: now.Add(-lookupFailedRetryAfter - time.Minute)}, 1},
		{"recent probe failure", UpdateAdvisory{Status: UpdateStatusUnknown, Reason: UpdateReasonOwnershipUnconfirmed, probeFailed: true, CheckedAt: now.Add(-lookupFailedRetryAfter + time.Minute)}, 0},
		{"stale probe failure", UpdateAdvisory{Status: UpdateStatusUnknown, Reason: UpdateReasonOwnershipUnconfirmed, probeFailed: true, CheckedAt: now.Add(-lookupFailedRetryAfter - time.Minute)}, 1},
		{"stale unparseable probe", UpdateAdvisory{Status: UpdateStatusUnknown, Reason: UpdateReasonVersionUnparseable, probeFailed: true, CheckedAt: now.Add(-lookupFailedRetryAfter - time.Minute)}, 1},
		{"stale unproven ownership", UpdateAdvisory{Status: UpdateStatusUnknown, Reason: UpdateReasonOwnershipUnconfirmed, CheckedAt: now.Add(-lookupFailedRetryAfter - time.Minute)}, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService("darwin")
			var checks atomic.Int32
			s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
				checks.Add(1)
				return VerifyResult{ResolvedPath: "/opt/bin/codex", Output: "1.0.0"}, nil
			})
			s.officialVersion = func(context.Context, Target) (string, error) { return "1.0.0", nil }
			tt.cached.AgentID = string(TargetCodex)
			s.updateAdvisories = map[Target]UpdateAdvisory{TargetCodex: tt.cached}
			advisory, err := s.UpdateAdvisory(context.Background(), TargetCodex, false)
			if err != nil {
				t.Fatal(err)
			}
			if got := checks.Load(); got != tt.wantChecks {
				t.Fatalf("checks = %d, want %d", got, tt.wantChecks)
			}
			if tt.wantChecks == 0 && advisory != tt.cached {
				t.Fatalf("advisory = %+v, want cached %+v", advisory, tt.cached)
			}
			if tt.wantChecks == 1 && !advisory.CheckedAt.After(tt.cached.CheckedAt) {
				t.Fatalf("advisory = %+v, want a fresh check", advisory)
			}
		})
	}
}

func TestUpdateAdvisoryUnknownWhenOwnershipVersionOrLookupUnproven(t *testing.T) {
	for _, tt := range []struct {
		name   string
		job    *Job
		output string
		latest string
		err    error
		reason UpdateUnknownReason
	}{
		{name: "unparseable installed", job: &Job{Status: StatusSucceeded, Method: "npm"}, output: "codex development", latest: "1.3.0", reason: "version_unparseable"},
		{name: "registry failure", job: &Job{Status: StatusSucceeded, Method: "npm"}, output: "codex 1.2.3", err: errors.New("offline"), reason: "lookup_failed"},
		{name: "different prerelease channels", job: &Job{Status: StatusSucceeded, Method: "npm"}, output: "codex 1.3.0-beta.1", latest: "1.3.0-rc.1", reason: "channel_unconfirmed"},
		{name: "unparseable latest", job: &Job{Status: StatusSucceeded, Method: "npm"}, output: "codex 1.2.3", err: errLatestUnparseable, reason: "version_unparseable"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService("darwin", "npm")
			if tt.job != nil {
				s.jobs[TargetCodex] = tt.job
			}
			s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) { return VerifyResult{Output: tt.output}, nil })
			s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return true, nil }
			s.managedVersion = fixedManagedVersion(tt.latest, tt.err)
			advisory, err := s.UpdateAdvisory(context.Background(), TargetCodex, false)
			if err != nil {
				t.Fatal(err)
			}
			if advisory.Status != UpdateStatusUnknown || advisory.Reason != tt.reason {
				t.Fatalf("advisory = %+v", advisory)
			}
		})
	}
}

func TestUpdateAdvisoryReportsInstallAheadOfLatest(t *testing.T) {
	s := newTestService("darwin", "npm")
	s.jobs[TargetCodex] = &Job{Status: StatusSucceeded, Method: "npm"}
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{Output: "codex 1.4.0-beta.2"}, nil
	})
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return true, nil }
	s.managedVersion = fixedManagedVersion("1.3.0", nil)
	advisory, err := s.UpdateAdvisory(context.Background(), TargetCodex, false)
	if err != nil {
		t.Fatal(err)
	}
	// Updates install latest, so offering one here would be a downgrade.
	if advisory.Status != UpdateStatusAheadOfLatest || advisory.LatestVersion != "1.3.0" || advisory.Reason != "" {
		t.Fatalf("advisory = %+v", advisory)
	}
}

func TestUpdateAdvisoryCurrentAndHomebrewPackage(t *testing.T) {
	s := newTestService("darwin", "brew")
	s.jobs[TargetCodex] = &Job{Target: TargetCodex, Status: StatusSucceeded, Method: "homebrew"}
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) { return VerifyResult{Output: "codex 1.3.0"}, nil })
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return true, nil }
	var gotMethod, gotPackage string
	var gotCask bool
	s.managedVersion = func(_ context.Context, plan Plan) (string, error) {
		gotMethod, gotPackage, gotCask = plan.Method, packageWithoutLatest(plan.Package), plan.PackageCask
		return "1.3.0", nil
	}
	advisory, err := s.UpdateAdvisory(context.Background(), TargetCodex, false)
	if err != nil {
		t.Fatal(err)
	}
	if advisory.Status != UpdateStatusCurrent {
		t.Fatalf("advisory = %+v", advisory)
	}
	if gotMethod != "homebrew" || gotPackage != "codex" || !gotCask {
		t.Fatalf("lookup = %s %s cask=%t", gotMethod, gotPackage, gotCask)
	}
}

func TestUpdateAdvisoryDetectsPackageOwnerWithoutAOInstallRecord(t *testing.T) {
	s := newTestService("darwin", "npm", "brew")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/opt/bin/codex", Output: "codex-cli 1.2.3\n"}, nil
	})
	s.ownsInstallation = func(_ context.Context, _ string, method, _ string, _ bool) (bool, error) { return method == "npm", nil }
	var gotMethod, gotPackage string
	s.managedVersion = func(_ context.Context, plan Plan) (string, error) {
		gotMethod, gotPackage = plan.Method, packageWithoutLatest(plan.Package)
		return "1.3.0", nil
	}
	advisory, err := s.UpdateAdvisory(context.Background(), TargetCodex, false)
	if err != nil {
		t.Fatal(err)
	}
	if advisory.Status != UpdateStatusBehindLatest || advisory.Source != "npm" || advisory.CurrentVersion != "1.2.3" || advisory.LatestVersion != "1.3.0" {
		t.Fatalf("advisory = %+v", advisory)
	}
	if gotMethod != "npm" || gotPackage != "@openai/codex" {
		t.Fatalf("lookup = %s %s", gotMethod, gotPackage)
	}
}

func TestUpdateAdvisoryFollowsBinaryOwnerOverRecordedMethod(t *testing.T) {
	s := newTestService("darwin", "npm", "brew")
	s.jobs[TargetCodex] = &Job{Target: TargetCodex, Status: StatusSucceeded, Method: "npm"}
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) { return VerifyResult{Output: "codex 1.3.0"}, nil })
	var checked []string
	s.ownsInstallation = func(_ context.Context, _ string, method, _ string, _ bool) (bool, error) {
		checked = append(checked, method)
		return method == "homebrew", nil
	}
	s.managedVersion = fixedManagedVersion("1.3.0", nil)
	advisory, err := s.UpdateAdvisory(context.Background(), TargetCodex, false)
	if err != nil {
		t.Fatal(err)
	}
	if advisory.Status != UpdateStatusCurrent || advisory.Source != "homebrew" {
		t.Fatalf("advisory = %+v", advisory)
	}
	if len(checked) != 2 || checked[0] != "npm" {
		t.Fatalf("ownership checks = %v, want recorded npm first", checked)
	}
}

func TestUpdateAdvisoryRequiresVerifiedPackageOwnership(t *testing.T) {
	s := newTestService("darwin", "npm")
	s.jobs[TargetCodex] = &Job{Target: TargetCodex, Status: StatusSucceeded, Method: "npm"}
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/other/codex", Output: "codex 1.2.3"}, nil
	})
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return false, nil }
	var latestCalled atomic.Bool
	s.managedVersion = func(context.Context, Plan) (string, error) {
		latestCalled.Store(true)
		return "1.3.0", nil
	}
	advisory, err := s.UpdateAdvisory(context.Background(), TargetCodex, false)
	if err != nil {
		t.Fatal(err)
	}
	if advisory.Status != UpdateStatusUnknown {
		t.Fatalf("advisory = %+v", advisory)
	}
	if latestCalled.Load() {
		t.Fatal("latest was queried without package ownership")
	}
}

func fixedManagedVersion(latest string, err error) managedVersionChecker {
	return func(context.Context, Plan) (string, error) {
		return latest, err
	}
}

func TestManagerOwnsBinaryTracesSymlinkIntoNPMPackage(t *testing.T) {
	root := t.TempDir()
	packageDir := filepath.Join(root, "node_modules", "@openai", "codex", "bin")
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	actual := filepath.Join(packageDir, "codex.js")
	if err := os.WriteFile(actual, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "codex")
	if err := os.Symlink(actual, link); err != nil {
		t.Fatal(err)
	}
	lookup := managerOwnsBinary(commandRunnerFunc(func(_ context.Context, argv []string, stdout, _ io.Writer) error {
		if len(argv) != 3 || argv[0] != "npm" || argv[1] != "root" || argv[2] != "-g" {
			t.Fatalf("argv=%v", argv)
		}
		_, err := io.WriteString(stdout, filepath.Join(root, "node_modules")+"\n")
		return err
	}))
	owned, err := lookup(context.Background(), link, "npm", "@openai/codex", false)
	if err != nil || !owned {
		t.Fatalf("owned=%t err=%v", owned, err)
	}
	owned, err = lookup(context.Background(), actual, "npm", "@anthropic-ai/claude-code", false)
	if err != nil || owned {
		t.Fatalf("wrong package owned=%t err=%v", owned, err)
	}
}

func TestParseHomebrewVersionMetadata(t *testing.T) {
	for _, tt := range []struct {
		name string
		cask bool
		json string
	}{
		{name: "formula", json: `{"formulae":[{"name":"codex","versions":{"stable":"1.3.0"}}]}`},
		{name: "cask", cask: true, json: `{"casks":[{"token":"codex","version":"1.3.0"}]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			version := parseHomebrewVersion(tt.json, "codex", tt.cask, false)
			if version != "1.3.0" {
				t.Fatalf("version=%q", version)
			}
		})
	}
}

func TestUpdateAdvisoryDisabledSkipsAllLookups(t *testing.T) {
	s := newTestService("darwin", "npm")
	s.updateChecksDisabled = true
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		t.Fatal("disabled update check probed the harness binary")
		return VerifyResult{}, nil
	})
	s.officialVersion = func(context.Context, Target) (string, error) {
		t.Fatal("disabled update check reached the network")
		return "", nil
	}
	advisory, err := s.UpdateAdvisory(context.Background(), TargetCodex, false)
	if err != nil {
		t.Fatal(err)
	}
	if advisory.Status != UpdateStatusUnknown || advisory.Reason != UpdateReasonDisabled || advisory.AgentID != string(TargetCodex) {
		t.Fatalf("advisory = %+v", advisory)
	}
}

func TestUpdateAdvisoryReportsSessionExecutableEvenWhenProbeFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, tc := range []struct {
		name, path, want string
		probeErr         error
	}{
		{"home path", filepath.Join(home, ".local", "bin", "codex"), filepath.Join("~", ".local", "bin", "codex"), nil},
		{"outside home", filepath.Join(home+"-other", "codex"), filepath.Join(home+"-other", "codex"), nil},
		{"failed probe", filepath.Join(home, "bin", "codex"), filepath.Join("~", "bin", "codex"), errors.New("cannot execute")},
		{"missing binary", "", "", errors.New("not found")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestService("darwin", "npm")
			s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
				return VerifyResult{ResolvedPath: tc.path, Output: "codex development"}, tc.probeErr
			})
			s.officialVersion = func(context.Context, Target) (string, error) {
				t.Fatal("unparseable version must not fetch releases")
				return "", nil
			}
			got, err := s.UpdateAdvisory(context.Background(), TargetCodex, false)
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err := json.Unmarshal(data, &wire); err != nil {
				t.Fatal(err)
			}
			path, _ := wire["binaryPath"].(string)
			if path != tc.want {
				t.Fatalf("binaryPath = %q, want %q", path, tc.want)
			}
		})
	}
}

type resolveOnlyAdvisoryVerifier struct {
	t    *testing.T
	path string
}

func (v resolveOnlyAdvisoryVerifier) Resolve(context.Context, Target) (string, error) {
	return v.path, nil
}
func (v resolveOnlyAdvisoryVerifier) Verify(context.Context, Target) (VerifyResult, error) {
	v.t.Fatal("local path lookup must not execute a version probe")
	return VerifyResult{}, nil
}
func TestUpdateAdvisoryKeepsExecutableWithoutReleaseChecks(t *testing.T) {
	for _, disabled := range []bool{true, false} {
		t.Run(fmt.Sprint(disabled), func(t *testing.T) {
			s := newTestService("darwin")
			s.updateChecksDisabled = disabled
			s.verifier = resolveOnlyAdvisoryVerifier{t: t, path: "/opt/bin/codex"}
			got, err := s.UpdateAdvisory(context.Background(), TargetCodex, false)
			if err != nil || got.BinaryPath != "/opt/bin/codex" || got.Status != UpdateStatusUnknown {
				t.Fatalf("advisory = %+v, err=%v", got, err)
			}
		})
	}
}
