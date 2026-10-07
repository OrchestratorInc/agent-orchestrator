package systeminstall

import (
	"context"
	"errors"
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
	s.managedVersion = func(_ context.Context, plan Plan, _ updateVersion) (managedVersionResult, error) {
		calls++
		gotMethod, gotPackage, gotCask = plan.Method, packageWithoutLatest(plan.Package), plan.PackageCask
		return managedVersionResult{Latest: "1.3.0", Channel: "latest"}, nil
	}
	for range 2 {
		advisory, err := s.UpdateAdvisory(context.Background(), TargetCodex)
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

func TestOfficialReleaseOnlyExposesMaintenanceForRecognisedNativeInstall(t *testing.T) {
	for _, tt := range []struct{ path, method string }{
		{"/Users/me/.local/share/claude/versions/2.1.291", "official-installer"},
		{"/opt/bin/claude", ""},
		{"/Users/me/.local/share/claude/versions/2.1.290", ""},
		{"/opt/homebrew/Cellar/claude/.local/share/claude/versions/2.1.291", ""},
	} {
		t.Run(tt.path, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tt.path)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, nil, 0o755); err != nil {
				t.Fatal(err)
			}
			if tt.method != "" {
				link := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(path))), "bin", "claude")
				if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path, link); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
				path = link
			}
			s := newTestService("darwin", "bash")
			s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
				return VerifyResult{ResolvedPath: path, Output: "Claude Code 2.1.291"}, nil
			})
			s.officialVersion = func(context.Context, Target) (string, error) { return "2.1.292", nil }
			advisory, err := s.UpdateAdvisory(context.Background(), TargetClaudeCode)
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
	if result, err := s.UpdateAdvisory(context.Background(), TargetCodex); err != nil || result.Status != UpdateStatusBehindLatest {
		t.Fatalf("initial=%+v err=%v", result, err)
	}
	version = "1.3.0"
	if result, err := s.UpdateAdvisory(context.Background(), TargetCodex, true); err != nil || result.Status != UpdateStatusCurrent || result.CurrentVersion != "1.3.0" {
		t.Fatalf("fresh=%+v err=%v", result, err)
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
	s.managedVersion = func(context.Context, Plan, updateVersion) (managedVersionResult, error) {
		if lookups.Add(1) == 1 {
			close(started)
		}
		<-release
		return managedVersionResult{Latest: "1.3.0", Channel: "latest"}, nil
	}
	pageCtx, cancelPage := context.WithCancel(context.Background())
	pageDone := make(chan error, 1)
	go func() {
		_, err := s.UpdateAdvisory(pageCtx, TargetCodex)
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
		advisory, _ := s.UpdateAdvisory(context.Background(), TargetCodex)
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
		{name: "ahead of registry", job: &Job{Status: StatusSucceeded, Method: "npm"}, output: "codex 1.4.0", latest: "1.3.0", reason: "channel_unconfirmed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService("darwin", "npm")
			if tt.job != nil {
				s.jobs[TargetCodex] = tt.job
			}
			s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) { return VerifyResult{Output: tt.output}, nil })
			s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return true, nil }
			s.managedVersion = fixedManagedVersion(tt.latest, tt.err)
			advisory, err := s.UpdateAdvisory(context.Background(), TargetCodex)
			if err != nil {
				t.Fatal(err)
			}
			if advisory.Status != UpdateStatusUnknown || advisory.Reason != tt.reason {
				t.Fatalf("advisory = %+v", advisory)
			}
		})
	}
}

func TestUpdateAdvisoryCurrentAndHomebrewPackage(t *testing.T) {
	s := newTestService("darwin", "brew")
	s.jobs[TargetCodex] = &Job{Target: TargetCodex, Status: StatusSucceeded, Method: "homebrew"}
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) { return VerifyResult{Output: "codex 1.3.0"}, nil })
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return true, nil }
	var gotMethod, gotPackage string
	var gotCask bool
	s.managedVersion = func(_ context.Context, plan Plan, _ updateVersion) (managedVersionResult, error) {
		gotMethod, gotPackage, gotCask = plan.Method, packageWithoutLatest(plan.Package), plan.PackageCask
		return managedVersionResult{Latest: "1.3.0", Channel: "latest"}, nil
	}
	advisory, err := s.UpdateAdvisory(context.Background(), TargetCodex)
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
	s.managedVersion = func(_ context.Context, plan Plan, _ updateVersion) (managedVersionResult, error) {
		gotMethod, gotPackage = plan.Method, packageWithoutLatest(plan.Package)
		return managedVersionResult{Latest: "1.3.0", Channel: "latest"}, nil
	}
	advisory, err := s.UpdateAdvisory(context.Background(), TargetCodex)
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
	advisory, err := s.UpdateAdvisory(context.Background(), TargetCodex)
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
	s.managedVersion = func(context.Context, Plan, updateVersion) (managedVersionResult, error) {
		latestCalled.Store(true)
		return managedVersionResult{Latest: "1.3.0", Channel: "latest"}, nil
	}
	advisory, err := s.UpdateAdvisory(context.Background(), TargetCodex)
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
	return func(context.Context, Plan, updateVersion) (managedVersionResult, error) {
		return managedVersionResult{Latest: latest, Channel: "latest"}, err
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
	advisory, err := s.UpdateAdvisory(context.Background(), TargetCodex)
	if err != nil {
		t.Fatal(err)
	}
	if advisory.Status != UpdateStatusUnknown || advisory.Reason != UpdateReasonDisabled || advisory.AgentID != string(TargetCodex) {
		t.Fatalf("advisory = %+v", advisory)
	}
}
