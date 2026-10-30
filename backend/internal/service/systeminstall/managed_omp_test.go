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

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestManagedOMPUsesStandaloneInsteadOfTheSystemBun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("managed Unix launchers use symlinks")
	}
	for _, goos := range []string{"darwin", "linux"} {
		for _, tc := range []struct {
			name        string
			downloadErr bool
			startupErr  bool
			unavailable bool
			wantError   string
		}{
			{name: "success"},
			{name: "download fails", downloadErr: true, wantError: "download failed"},
			{name: "startup fails", startupErr: true, wantError: "invalid standalone binary"},
			{name: "installer unavailable", unavailable: true, wantError: "standalone installer is unavailable"},
		} {
			t.Run(goos+"/"+tc.name, func(t *testing.T) {
				s := newTestService(goos, "bun", "sh")
				s.managedRoot = t.TempDir()
				targetDir := s.managedTargetDir(TargetOMP)
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
						if command.URL != "https://omp.sh/install" || !command.Isolated || !slices.Equal(command.Args, []string{"--binary", "--ref", "v18.14.0"}) {
							t.Errorf("standalone installer = %+v", command)
						}
						if tc.downloadErr {
							return ports.InstallScriptResult{}, errors.New("download failed")
						}
						bin := envValue(command.Env, "PI_INSTALL_DIR")
						if bin != filepath.Join(targetDir, "versions", "v2", "bin") || envValue(command.Env, "PATH") != managedInstallerPath {
							t.Fatalf("installer escaped the isolated candidate: %+v", command)
						}
						if err := os.MkdirAll(bin, 0o755); err != nil {
							return ports.InstallScriptResult{}, err
						}
						return ports.InstallScriptResult{}, os.WriteFile(filepath.Join(bin, "omp"), []byte("18.14.0\n"), 0o755)
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
				var output strings.Builder
				err := s.runManagedInstall(context.Background(), Plan{Target: TargetOMP, Method: "bun", Package: old.Package}, &installedBaseline{path: launcher, latest: "18.14.0"}, nil, &output)
				if tc.wantError != "" {
					if err == nil {
						t.Fatal("failed candidate reported success")
					}
					failureSummary(err, &output)
					if !strings.Contains(output.String()+err.Error(), tc.wantError) {
						t.Fatalf("failure lacks %q: %v\n%s", tc.wantError, err, output.String())
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
					if err != nil {
						t.Fatal(err)
					}
					if record, ok := s.readManagedRecord(TargetOMP); !ok || record.Method != "official-installer" || record.Package != "" || record.Version != "18.14.0" {
						t.Fatalf("standalone ownership was not recorded: %+v", record)
					}
					if got := readFile(launcher); got != "18.14.0\n" {
						t.Fatalf("launcher did not select the verified candidate: %q", got)
					}
				}
				if got := readFile(oldBinary); got != "18.13.2\n" {
					t.Fatalf("previous release was modified: %q", got)
				}
			})
		}
	}
}
