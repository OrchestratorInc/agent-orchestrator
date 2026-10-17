package systeminstall

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestLayoutOfRecognisesPackageToolDirectories(t *testing.T) {
	for path, want := range map[string]string{
		"/Users/me/.local/bin/claude":                                         "",
		"/Users/me/.local/share/claude/versions/2.1.291":                      "",
		"/opt/homebrew/Cellar/opencode/1.18.34/bin/opencode":                  layoutHomebrew,
		"/opt/homebrew/Caskroom/codex/0.160.1/codex":                          layoutHomebrew,
		"/Users/me/.nvm/versions/node/v22.1.0/lib/node_modules/@openai/codex": layoutNPM,
		"/Users/me/.bun/install/global/node_modules/omp/bin/omp":              layoutBun,
		"/Users/me/.local/share/uv/tools/aider-chat/bin/aider":                layoutUV,
		"/Users/me/.local/pipx/venvs/mistral-vibe/bin/vibe":                   layoutPipx,
		"/Users/me/.local/share/mise/shims/codex":                             layoutVersionManager,
		"/Users/me/.asdf/shims/codex":                                         layoutVersionManager,
		"/Users/me/.volta/bin/codex":                                          layoutVersionManager,
		`C:\Users\me\AppData\Local\Microsoft\WinGet\Packages\copilot.exe`:     layoutWinget,
		`C:\Users\me\AppData\Roaming\npm\codex.cmd`:                           layoutNPM,
		"/usr/bin/opencode":                                                   layoutSystem,
		"/nix/store/abc123-opencode-1.18.34/bin/opencode":                     layoutSystem,
		"/snap/opencode/12/bin/opencode":                                      layoutSystem,
		"/Users/me/Library/pnpm/global/5/node_modules/@openai/codex":          layoutPNPM,
		"/Users/me/.config/yarn/global/node_modules/@openai/codex/bin/codex":  layoutYarn,
	} {
		if got := layoutOf(path).layout; got != want {
			t.Errorf("layoutOf(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestOfficialChannelFitsOnlyMatchingLayouts(t *testing.T) {
	for _, tt := range []struct {
		kind   officialSourceKind
		layout string
		want   bool
	}{
		{officialText, "", true},
		{officialText, layoutNPM, false},
		{officialGitHub, layoutHomebrew, false},
		{officialNPM, layoutNPM, true},
		{officialPyPI, layoutUV, true},
		{officialPyPI, layoutPipx, true},
		{officialJSON, layoutVersionManager, false},
	} {
		if got := officialChannelFits(tt.kind, tt.layout); got != tt.want {
			t.Errorf("officialChannelFits(%d, %q) = %t, want %t", tt.kind, tt.layout, got, tt.want)
		}
	}
}

func TestUpdateAdvisoryIgnoresOfficialChannelForUnownedPackageLayout(t *testing.T) {
	s := newTestService("darwin", "npm")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/Users/me/.nvm/versions/node/v22.1.0/lib/node_modules/@openai/codex/bin/codex.js", Output: "codex-cli 0.150.0"}, nil
	})
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return false, nil }
	s.managedVersion = fixedManagedVersion("0.160.1", nil)
	s.officialVersion = func(context.Context, Target) (string, error) {
		t.Fatal("official channel queried for a binary inside another npm installation")
		return "", nil
	}
	advisory, err := s.UpdateAdvisory(context.Background(), TargetCodex, false)
	if err != nil {
		t.Fatal(err)
	}
	if advisory.Status != UpdateStatusUnknown || advisory.Reason != "ownership_unconfirmed" {
		t.Fatalf("advisory = %+v", advisory)
	}
}

func TestConfirmInstalledOwnerRefusesMethodThatDidNotInstallBinary(t *testing.T) {
	s := newTestService("darwin", "npm")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/opt/homebrew/Caskroom/codex/0.150.0/codex", Output: "codex-cli 0.150.0"}, nil
	})
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return false, nil }
	if _, err := s.confirmInstalledOwner(context.Background(), TargetCodex, Plan{Method: "npm", Package: "@openai/codex"}); !errors.Is(err, ErrInstallOwner) {
		t.Fatalf("npm err = %v, want ErrInstallOwner", err)
	}
	if _, err := s.confirmInstalledOwner(context.Background(), TargetCodex, Plan{Method: "official-installer"}); !errors.Is(err, ErrInstallOwner) {
		t.Fatalf("official installer err = %v, want ErrInstallOwner for a Homebrew-owned binary", err)
	}
}

func TestConfirmInstalledOwnerRecordsBaselineForOwnedBinary(t *testing.T) {
	s := newTestService("darwin", "npm")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/usr/local/bin/codex", Output: "codex-cli 0.150.0"}, nil
	})
	var gotPackage string
	s.ownsInstallation = func(_ context.Context, _ string, _ string, pkg string, _ bool) (bool, error) {
		gotPackage = pkg
		return true, nil
	}
	s.updateAdvisories = map[Target]UpdateAdvisory{TargetCodex: {Status: UpdateStatusBehindLatest, LatestVersion: "0.160.1", CheckedAt: time.Now()}}
	baseline, err := s.confirmInstalledOwner(context.Background(), TargetCodex, Plan{Method: "npm", Package: "@openai/codex@latest"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPackage != "@openai/codex" || baseline == nil || baseline.path != "/usr/local/bin/codex" || baseline.version != "0.150.0" || baseline.latest != "0.160.1" {
		t.Fatalf("package = %q baseline = %+v", gotPackage, baseline)
	}
}

func TestConfirmInstalledOwnerTrustsAOInstallWhenBinaryCannotRun(t *testing.T) {
	s := newTestService("darwin", "npm")
	s.jobs[TargetCodex] = &Job{Target: TargetCodex, Status: StatusSucceeded, Method: "npm"}
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/Users/test/.npm/bin/codex"}, errors.New("exec format error")
	})
	s.updateAdvisories = map[Target]UpdateAdvisory{TargetCodex: {Status: UpdateStatusBehindLatest, LatestVersion: "1.3.0"}}
	baseline, err := s.confirmInstalledOwner(context.Background(), TargetCodex, Plan{Method: "npm", Package: "@openai/codex"})
	if err != nil {
		t.Fatalf("recorded AO npm install should allow repair, got %v", err)
	}
	if baseline == nil || baseline.latest != "1.3.0" || baseline.path != "/Users/test/.npm/bin/codex" {
		t.Fatalf("recovery baseline = %+v", baseline)
	}
	for _, output := range []string{"development", "codex 1.2.3"} {
		if failure, _ := updateOutcome(baseline, VerifyResult{Output: output}); failure == "" {
			t.Fatalf("recovery accepted %q", output)
		}
	}
	if _, err := s.confirmInstalledOwner(context.Background(), TargetCodex, Plan{Method: "homebrew", Package: "codex", PackageCask: true}); !errors.Is(err, ErrInstallOwner) {
		t.Fatalf("other method err = %v, want ErrInstallOwner", err)
	}
}

func TestUpdateOutcomeRequiresKnownNewerReleaseToReachRunningBinary(t *testing.T) {
	baseline := &installedBaseline{path: "/usr/local/bin/codex", version: "0.150.0", latest: "0.160.1"}

	failure, note := updateOutcome(baseline, VerifyResult{ResolvedPath: "/usr/local/bin/codex", Output: "codex-cli 0.160.1"})
	if failure != "" || !strings.Contains(note, "Updated 0.150.0 to 0.160.1") {
		t.Fatalf("upgraded: failure=%q note=%q", failure, note)
	}

	failure, _ = updateOutcome(baseline, VerifyResult{ResolvedPath: "/usr/local/bin/codex", Output: "codex-cli 0.150.0"})
	if !strings.Contains(failure, "still reports 0.150.0") {
		t.Fatalf("unchanged behind latest: failure=%q", failure)
	}

	failure, note = updateOutcome(&installedBaseline{path: "/usr/local/bin/codex", version: "0.160.1"}, VerifyResult{ResolvedPath: "/usr/local/bin/codex", Output: "codex-cli 0.160.1"})
	if failure != "" || !strings.Contains(note, "Version unchanged at 0.160.1") {
		t.Fatalf("already current: failure=%q note=%q", failure, note)
	}

	_, note = updateOutcome(baseline, VerifyResult{ResolvedPath: "/opt/homebrew/bin/codex", Output: "codex-cli 0.160.1"})
	if !strings.Contains(note, "AO now runs /opt/homebrew/bin/codex (was /usr/local/bin/codex)") {
		t.Fatalf("path change note = %q", note)
	}

	if failure, note := updateOutcome(nil, VerifyResult{Output: "codex-cli 0.150.0"}); failure != "" || note != "" {
		t.Fatalf("no baseline: failure=%q note=%q", failure, note)
	}
}

func TestUpdateOutcomeRejectsUnverifiedOrPartialUpdates(t *testing.T) {
	for _, output := range []string{"codex 0.155.0", "codex development", "codex 0.149.0"} {
		failure, _ := updateOutcome(&installedBaseline{version: "0.150.0", latest: "0.160.1"}, VerifyResult{Output: output})
		if failure == "" {
			t.Errorf("output %q incorrectly accepted below known target or without a version", output)
		}
	}
	if failure, _ := updateOutcome(&installedBaseline{version: "unparseable", latest: "0.160.1"}, VerifyResult{Output: "codex 0.155.0"}); failure == "" {
		t.Fatal("unparseable baseline bypassed known latest")
	}
}

func TestUpdateOutcomeReadsBuildSuffixedVersions(t *testing.T) {
	baseline := &installedBaseline{path: "/Users/test/.amp/bin/amp", version: "0.0.1791033893-g28ae98", latest: "0.0.1791388870-g4d32fb", scheme: versionSchemeFor(TargetAmp)}
	failure, note := updateOutcome(baseline, VerifyResult{ResolvedPath: baseline.path, Output: "0.0.1791388870-g4d32fb (released 2026-10-07T10:00:00.000Z, 0d ago)"})
	if failure != "" || !strings.Contains(note, "Updated 0.0.1791033893-g28ae98 to 0.0.1791388870-g4d32fb") {
		t.Fatalf("failure=%q note=%q", failure, note)
	}
	failure, _ = updateOutcome(baseline, VerifyResult{ResolvedPath: baseline.path, Output: "0.0.1791033893-g28ae98"})
	if failure == "" {
		t.Fatal("an Amp update that left the old build running did not fail")
	}
}

func TestConfirmInstalledOwnerRefusesBinaryItCannotLocate(t *testing.T) {
	for name, verify := range map[string]func(context.Context, Target) (VerifyResult, error){
		"resolve failed": func(context.Context, Target) (VerifyResult, error) {
			return VerifyResult{}, errors.New("codex not found")
		},
		"empty path": func(context.Context, Target) (VerifyResult, error) {
			return VerifyResult{Output: "codex-cli 0.150.0"}, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := newTestService("darwin", "npm")
			// Even AO's own recorded install cannot say which copy an update
			// would change without the path sessions run.
			s.jobs[TargetCodex] = &Job{Target: TargetCodex, Status: StatusSucceeded, Method: "official-installer"}
			s.verifier = harnessVerifierFunc(verify)
			if _, err := s.confirmInstalledOwner(context.Background(), TargetCodex, Plan{Method: "official-installer"}); !errors.Is(err, ErrInstallOwner) {
				t.Fatalf("err = %v, want ErrInstallOwner", err)
			}
		})
	}
}

func TestConfirmInstalledOwnerRefusesVendorUpdateOfSystemPackage(t *testing.T) {
	s := newTestService("linux", "bash")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/nix/store/abc123-opencode-1.18.34/bin/opencode", Output: "1.18.34"}, nil
	})
	if _, err := s.confirmInstalledOwner(context.Background(), TargetOpencode, Plan{Method: "official-installer", Target: TargetOpencode}); !errors.Is(err, ErrInstallOwner) {
		t.Fatalf("err = %v, want ErrInstallOwner for a Nix-managed binary", err)
	}
}

func TestUpdateWithoutMethodUsesTheMethodThatOwnsTheBinary(t *testing.T) {
	for name, advisory := range map[string]*UpdateAdvisory{
		"from the update advisory": {AgentID: string(TargetCodex), Status: UpdateStatusBehindLatest, CurrentVersion: "1.2.0", LatestVersion: "1.3.0", MaintenanceMethod: "npm", CheckedAt: time.Now()},
		"from the binary":          nil,
	} {
		t.Run(name, func(t *testing.T) {
			s := newTestService("darwin", "npm", "sh")
			if advisory != nil {
				s.updateAdvisories = map[Target]UpdateAdvisory{TargetCodex: *advisory}
			}
			version := "1.2.0"
			s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
				return VerifyResult{ResolvedPath: "/Users/test/.npm/lib/node_modules/@openai/codex/bin/codex.js", Output: "codex-cli " + version}, nil
			})
			s.ownsInstallation = func(_ context.Context, _ string, method, _ string, _ bool) (bool, error) {
				return method == "npm", nil
			}
			var ran []string
			s.commands = commandRunnerFunc(func(_ context.Context, argv []string, _, _ io.Writer) error {
				ran = argv
				version = "1.3.0"
				return nil
			})
			job, err := s.StartAgentOperation(context.Background(), TargetCodex, "", AgentOperationUpdate, "")
			if err != nil {
				t.Fatalf("method-less update refused: %v", err)
			}
			if job.Method != "npm" {
				t.Fatalf("method = %q, want npm", job.Method)
			}
			s.workers.Wait()
			waitForStatus(t, s, TargetCodex, StatusSucceeded)
			if len(ran) == 0 || ran[0] != "npm" {
				t.Fatalf("ran %v, want the npm update", ran)
			}
		})
	}
}

func TestUpdateWithoutMethodRefusesUnconfirmedOwner(t *testing.T) {
	s := newTestService("darwin", "npm", "sh")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/opt/homebrew/Caskroom/codex/0.150.0/codex", Output: "codex-cli 0.150.0"}, nil
	})
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return false, nil }
	if _, err := s.StartAgentOperation(context.Background(), TargetCodex, "", AgentOperationUpdate, ""); !errors.Is(err, ErrInstallOwner) {
		t.Fatalf("err = %v, want ErrInstallOwner", err)
	}
}

func TestRefusedReinstallKeepsTheLastJobRecord(t *testing.T) {
	store := newInstallJobStoreFake()
	s := newTestService("darwin", "bash")
	s.jobStore = store
	finished := time.Now().UTC()
	last := Job{Target: TargetCursor, Status: StatusSucceeded, Method: "official-installer", StartedAt: &finished, FinishedAt: &finished, UpdatedAt: &finished}
	s.jobs[TargetCursor] = &last
	job, err := s.StartAgentOperation(context.Background(), TargetCursor, "", AgentOperationReinstall, "")
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != StatusUnsupported || job.Error == "" {
		t.Fatalf("job = %+v, want an unsupported refusal", job)
	}
	current, err := s.Status(context.Background(), TargetCursor)
	if err != nil || current.Status != StatusSucceeded {
		t.Fatalf("status after refusal = %+v err=%v, want the last succeeded job", current, err)
	}
	if _, ok, _ := store.GetAgentInstallJob(context.Background(), string(TargetCursor)); ok {
		t.Fatal("the refusal was persisted")
	}
}
