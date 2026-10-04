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
	s.latestVersion = func(_ context.Context, method, pkg string, cask bool) (string, error) {
		calls++
		gotMethod, gotPackage, gotCask = method, pkg, cask
		return "1.3.0", nil
	}
	for range 2 {
		advisory, err := s.UpdateAdvisory(context.Background(), TargetCodex)
		if err != nil {
			t.Fatal(err)
		}
		if advisory.Status != UpdateStatusBehindLatest || advisory.CurrentVersion != "1.2.3" || advisory.LatestVersion != "1.3.0" || advisory.Source != "npm" {
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
	s.latestVersion = func(context.Context, string, string, bool) (string, error) {
		if lookups.Add(1) == 1 {
			close(started)
		}
		<-release
		return "1.3.0", nil
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
	}{
		{name: "no owned method", output: "codex 1.2.3", latest: "1.3.0"},
		{name: "unparseable installed", job: &Job{Status: StatusSucceeded, Method: "npm"}, output: "codex development", latest: "1.3.0"},
		{name: "registry failure", job: &Job{Status: StatusSucceeded, Method: "npm"}, output: "codex 1.2.3", err: errors.New("offline")},
		{name: "ahead of registry", job: &Job{Status: StatusSucceeded, Method: "npm"}, output: "codex 1.4.0", latest: "1.3.0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService("darwin", "npm")
			if tt.job != nil {
				s.jobs[TargetCodex] = tt.job
			}
			s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) { return VerifyResult{Output: tt.output}, nil })
			s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return true, nil }
			s.latestVersion = func(context.Context, string, string, bool) (string, error) { return tt.latest, tt.err }
			advisory, err := s.UpdateAdvisory(context.Background(), TargetCodex)
			if err != nil {
				t.Fatal(err)
			}
			if advisory.Status != UpdateStatusUnknown {
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
	s.latestVersion = func(_ context.Context, method, pkg string, cask bool) (string, error) {
		gotMethod, gotPackage, gotCask = method, pkg, cask
		return "1.3.0", nil
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

func TestUpdateAdvisoryRequiresVerifiedPackageOwnership(t *testing.T) {
	s := newTestService("darwin", "npm")
	s.jobs[TargetCodex] = &Job{Target: TargetCodex, Status: StatusSucceeded, Method: "npm"}
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/other/codex", Output: "codex 1.2.3"}, nil
	})
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return false, nil }
	var latestCalled atomic.Bool
	s.latestVersion = func(context.Context, string, string, bool) (string, error) {
		latestCalled.Store(true)
		return "1.3.0", nil
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

func TestPackageOwnsBinaryTracesSymlinkIntoNPMPackage(t *testing.T) {
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
	lookup := packageOwnsBinary(commandRunnerFunc(func(_ context.Context, argv []string, stdout, _ io.Writer) error {
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

func TestLatestAvailableVersionParsesHomebrewMetadata(t *testing.T) {
	for _, tt := range []struct {
		name string
		cask bool
		json string
	}{
		{name: "formula", json: `{"formulae":[{"versions":{"stable":"1.3.0"}}]}`},
		{name: "cask", cask: true, json: `{"casks":[{"version":"1.3.0"}]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			lookup := latestAvailableVersion(commandRunnerFunc(func(_ context.Context, argv []string, stdout, _ io.Writer) error {
				if argv[0] != "brew" || argv[len(argv)-1] != "codex" {
					t.Fatalf("argv=%v", argv)
				}
				_, err := io.WriteString(stdout, tt.json)
				return err
			}))
			version, err := lookup(context.Background(), "homebrew", "codex", tt.cask)
			if err != nil || version != "1.3.0" {
				t.Fatalf("version=%q err=%v", version, err)
			}
		})
	}
}
