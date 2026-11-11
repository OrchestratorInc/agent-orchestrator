package systeminstall

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestManagedOMPUsesStandaloneInsteadOfTheSystemBun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("managed Unix launchers use symlinks")
	}
	for _, goos := range []string{"darwin", "linux"} {
		for _, tc := range []struct {
			name         string
			operation    AgentOperation
			maintain     bool
			downloadErr  bool
			startupErr   bool
			unavailable  bool
			missingShell bool
			wantError    string
		}{
			{name: "migrate then maintain", maintain: true},
			{name: "reinstall", operation: AgentOperationReinstall},
			{name: "download fails", downloadErr: true, wantError: "download failed"},
			{name: "startup fails", startupErr: true, wantError: "invalid standalone binary"},
			{name: "installer unavailable", unavailable: true, wantError: "standalone installer is unavailable"},
			{name: "shell unavailable", missingShell: true, wantError: "sh was not found on PATH"},
		} {
			t.Run(goos+"/"+tc.name, func(t *testing.T) {
				operation, wantVersion := tc.operation, "18.14.0"
				if operation == "" {
					operation = AgentOperationUpdate
				}
				if operation == AgentOperationReinstall {
					wantVersion = "18.13.2"
				}
				found := []string{"bun", "sh"}
				if tc.missingShell {
					found = []string{"bun"}
				}
				s := newTestService(goos, found...)
				t.Cleanup(s.stop)
				s.managedRoot = t.TempDir()
				targetDir := s.managedTargetDir(TargetOMP)
				release := "v2"
				oldBinary := filepath.Join(targetDir, "versions", "v1", "bin", "omp")
				if err := os.MkdirAll(filepath.Dir(oldBinary), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(oldBinary, []byte("18.13.2\n"), 0o755); err != nil {
					t.Fatal(err)
				}
				old := managedRecord{Method: "bun", Package: "@oh-my-pi/pi-coding-agent", CLI: "omp", Version: "18.13.2", Dir: "v1", BinRel: filepath.Join("bin", "omp")}
				if err := s.switchManagedCurrent(context.Background(), targetDir, old.Dir); err != nil {
					t.Fatal(err)
				}
				if err := s.ensureManagedLauncher(TargetOMP, old); err != nil {
					t.Fatal(err)
				}
				if err := writeFileAtomic(filepath.Join(targetDir, "managed.json"), mustJSON(old)); err != nil {
					t.Fatal(err)
				}
				s.installCommands = installCommandRunnerFunc(func(context.Context, ports.InstallCommand, io.Writer, io.Writer) error {
					t.Error("OMP update must not install or upgrade the system Bun")
					return errors.New("unexpected Bun command")
				})
				if !tc.unavailable {
					s.installScripts = installScriptRunnerFunc(func(_ context.Context, command ports.InstallScriptCommand, _, _ io.Writer) (ports.InstallScriptResult, error) {
						if command.URL != "https://omp.sh/install" || !command.Isolated || !slices.Equal(command.Interpreter, []string{"/usr/bin/sh"}) || !slices.Equal(command.Args, []string{"--binary", "--ref", "v" + wantVersion}) {
							t.Errorf("standalone installer = %+v", command)
						}
						if tc.downloadErr {
							return ports.InstallScriptResult{}, errors.New("download failed")
						}
						bin := envValue(command.Env, "PI_INSTALL_DIR")
						if bin != filepath.Join(targetDir, "versions", release, "bin") || envValue(command.Env, "PATH") != managedInstallerPath {
							t.Fatalf("installer escaped the isolated candidate: %+v", command)
						}
						if err := os.MkdirAll(bin, 0o755); err != nil {
							return ports.InstallScriptResult{}, err
						}
						return ports.InstallScriptResult{}, os.WriteFile(filepath.Join(bin, "omp"), []byte(wantVersion+"\n"), 0o755)
					})
				}
				s.commands = commandRunnerFunc(func(_ context.Context, argv []string, stdout, stderr io.Writer) error {
					if len(argv) != 2 || argv[1] != "--version" {
						return errors.New("unexpected startup command")
					}
					if tc.startupErr {
						_, _ = io.WriteString(stderr, "invalid standalone binary\n")
						return errors.New("exit status 1")
					}
					_, err := io.WriteString(stdout, readFile(argv[0]))
					return err
				})
				launcher := filepath.Join(s.ManagedBinDir(), "omp")
				s.verifier = harnessVerifierFunc(func(_ context.Context, target Target) (VerifyResult, error) {
					if target != TargetOMP {
						return VerifyResult{}, errors.New("not installed")
					}
					return VerifyResult{ResolvedPath: launcher, Output: readFile(launcher)}, nil
				})
				if operation == AgentOperationUpdate {
					s.sessions = sessionListerStub{sessions: []domain.SessionRecord{{ID: "omp-1", Harness: domain.HarnessOMP}}}
				}
				s.updateAdvisories = map[Target]UpdateAdvisory{TargetOMP: {
					AgentID: "omp", Status: UpdateStatusBehindLatest, LatestVersion: "18.14.0",
					MaintenanceMethod: "bun", CheckedAt: time.Now(),
				}}
				plans, err := s.AgentPlans(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				var bun AgentInstallMethod
				for _, agent := range plans {
					if agent.AgentID == "omp" {
						for _, method := range agent.Methods {
							if method.ID == "bun" {
								bun = method
							}
						}
					}
				}
				if bun.ID != "bun" {
					t.Fatal("Bun-owned installation is missing from AgentPlans")
				}
				unsupported := tc.unavailable || tc.missingShell
				if unsupported {
					if bun.UpdateAvailable || bun.ReinstallAvailable || !strings.Contains(bun.UpdateReason, tc.wantError) || !strings.Contains(bun.ReinstallReason, tc.wantError) {
						t.Fatalf("unavailable installer was not disclosed: %+v", bun)
					}
				} else if !bun.UpdateAvailable || !bun.ReinstallAvailable ||
					bun.UpdateCommand != "/usr/bin/sh <downloaded from https://omp.sh/install> --binary" ||
					bun.ReinstallCommand != "/usr/bin/sh <downloaded from https://omp.sh/install> --binary --ref v18.13.2" {
					t.Fatalf("AgentPlans does not disclose the actual installer: %+v", bun)
				}
				job, err := s.StartAgentOperation(context.Background(), TargetOMP, "bun", operation, "")
				if unsupported {
					if !errors.Is(err, ErrInstallMethod) || !strings.Contains(err.Error(), tc.wantError) {
						t.Fatalf("unavailable installer was not rejected before execution: %v", err)
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					if job.Method != "official-installer" || !strings.Contains(job.Command, "/usr/bin/sh <downloaded from https://omp.sh/install> --binary") {
						t.Fatalf("job does not match the disclosed installer: %+v", job)
					}
					s.workers.Wait()
					job, err = s.Status(context.Background(), TargetOMP)
					if err != nil {
						t.Fatal(err)
					}
				}
				if tc.wantError != "" {
					if !unsupported && (job.Status != StatusFailed || !strings.Contains(job.Error+job.Output, tc.wantError)) {
						t.Fatalf("failure lacks %q: %+v", tc.wantError, job)
					}
					if record, ok := s.readManagedRecord(TargetOMP); !ok || record != old {
						t.Fatalf("working record changed after failure: %+v", record)
					}
					if got := readFile(launcher); got != "18.13.2\n" {
						t.Fatalf("working launcher changed after failure: %q", got)
					}
					if _, err := os.Stat(filepath.Join(targetDir, "versions", "v2")); !os.IsNotExist(err) {
						t.Fatalf("failed candidate was retained: %v", err)
					}
				} else {
					if job.Status != StatusSucceeded {
						t.Fatalf("operation failed: %+v", job)
					}
					if record, ok := s.readManagedRecord(TargetOMP); !ok || record.Method != job.Method || record.Package != "" || record.Version != wantVersion {
						t.Fatalf("standalone ownership was not recorded: %+v", record)
					}
					if got := readFile(launcher); got != wantVersion+"\n" {
						t.Fatalf("launcher did not select the verified candidate: %q", got)
					}
				}
				if got := readFile(oldBinary); got != "18.13.2\n" {
					t.Fatalf("previous release was modified: %q", got)
				}
				if tc.maintain {
					s.sessions = nil
					for _, followup := range []struct {
						operation        AgentOperation
						version, release string
					}{
						{AgentOperationReinstall, "18.14.0", "v3"},
						{AgentOperationUpdate, "18.14.1", "v4"},
					} {
						previous, ok := s.readManagedRecord(TargetOMP)
						if !ok || previous.Method != "official-installer" {
							t.Fatalf("migration record missing before %s: %+v", followup.operation, previous)
						}
						release, wantVersion = followup.release, followup.version
						if followup.operation == AgentOperationUpdate {
							s.mu.Lock()
							s.updateAdvisories = map[Target]UpdateAdvisory{TargetOMP: {
								AgentID: "omp", Status: UpdateStatusBehindLatest, LatestVersion: "18.14.1",
								MaintenanceMethod: "bun", CheckedAt: time.Now(),
							}}
							s.mu.Unlock()
						}
						job, err = s.StartAgentOperation(context.Background(), TargetOMP, "bun", followup.operation, "")
						if err != nil {
							t.Fatalf("%s after migration: %v", followup.operation, err)
						}
						if job.Method != "official-installer" || !strings.Contains(job.Command, "<downloaded from https://omp.sh/install> --binary") {
							t.Fatalf("%s escaped the managed standalone installer: %+v", followup.operation, job)
						}
						if followup.operation == AgentOperationReinstall && !strings.HasSuffix(job.Command, "--binary --ref v18.14.0") {
							t.Fatalf("reinstall did not pin the migrated record's version: %+v", job)
						}
						s.workers.Wait()
						job, err = s.Status(context.Background(), TargetOMP)
						if err != nil || job.Status != StatusSucceeded {
							t.Fatalf("%s after migration = %+v (%v)", followup.operation, job, err)
						}
						if record, ok := s.readManagedRecord(TargetOMP); !ok || record.Method != "official-installer" || record.Dir != release || record.Version != wantVersion {
							t.Fatalf("%s did not maintain the migrated copy: %+v", followup.operation, record)
						}
						assertResolvesInto(t, launcher, filepath.Join(targetDir, "versions", release))
						if got := readFile(filepath.Join(targetDir, "versions", previous.Dir, previous.BinRel)); got != previous.Version+"\n" {
							t.Fatalf("%s modified the previous managed release: %q", followup.operation, got)
						}
					}
				}
			})
		}
	}
}

func TestOMPStandaloneUpdateRejectsUnconfirmedBunOwner(t *testing.T) {
	s := newTestService("darwin", "bun", "sh")
	t.Cleanup(s.stop)
	s.managedRoot = t.TempDir()
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/Users/me/.bun/bin/omp", Output: "18.13.2"}, nil
	})
	ownershipChecked := false
	s.ownsInstallation = func(_ context.Context, _ string, method, pkg string, _ bool) (bool, error) {
		ownershipChecked = true
		if method != "bun" || pkg != "@oh-my-pi/pi-coding-agent" {
			t.Errorf("old installation checked against (%q, %q), want Bun's OMP package", method, pkg)
		}
		return false, nil
	}
	s.installScripts = installScriptRunnerFunc(func(context.Context, ports.InstallScriptCommand, io.Writer, io.Writer) (ports.InstallScriptResult, error) {
		t.Error("standalone installer ran before ownership was confirmed")
		return ports.InstallScriptResult{}, errors.New("unexpected installer")
	})
	_, err := s.StartAgentOperation(context.Background(), TargetOMP, "bun", AgentOperationUpdate, "")
	if !errors.Is(err, ErrInstallOwner) || !ownershipChecked {
		t.Fatalf("unconfirmed Bun owner was not rejected: %v (checked=%t)", err, ownershipChecked)
	}
}
