package primeagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestPrimeDaemonSocketHelpDeclaration(t *testing.T) {
	for _, tc := range []struct {
		name string
		help string
		want bool
	}{
		{"option", "Options:\n  --daemon-socket <PATH>  Select socket\n", true},
		{"short alias", "  -d, --daemon-socket <PATH>  Select socket\n", true},
		{"prose", "This build does not support --daemon-socket <PATH>.", false},
		{"usage", "Usage: prime-agent --daemon-socket <PATH>", false},
		{"another option description", "  --help  Explain --daemon-socket <PATH>", false},
		{"similar option", "  --daemon-socket-timeout <SECONDS>", false},
		{"no value declaration", "  --daemon-socket is unsupported", false},
		{"empty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := primeHelpDeclaresDaemonSocket(tc.help); got != tc.want {
				t.Fatalf("support = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPrimeDaemonSocketProbeIsBoundedAndCached(t *testing.T) {
	plugin := &Plugin{}
	calls := 0
	plugin.runCommand = func(ctx context.Context, binary, cwd string, env map[string]string, args ...string) ([]byte, error) {
		calls++
		deadline, bounded := ctx.Deadline()
		if !bounded || time.Until(deadline) > primeDaemonProbeTimeout || binary != "synthetic-prime" || cwd != "" || env != nil || !slices.Equal(args, []string{"--help"}) {
			t.Fatalf("unbounded or unexpected capability probe: deadline=%v, args=%v", deadline, args)
		}
		return []byte("  --daemon-socket <PATH>  Select socket\n"), nil
	}
	for range 2 {
		if err := plugin.requireDaemonSocket(context.Background(), "synthetic-prime"); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("help probes = %d, want one cached probe", calls)
	}
	plugin.InvalidateBinaryResolution()
	if err := plugin.requireDaemonSocket(context.Background(), "synthetic-prime"); err != nil || calls != 2 {
		t.Fatalf("binary invalidation retained stale support: %v, calls=%d", err, calls)
	}
}

func TestPrimeDaemonSocketProbeFailsClosed(t *testing.T) {
	probeErr := errors.New("synthetic help failure")
	plugin := &Plugin{runCommand: func(context.Context, string, string, map[string]string, ...string) ([]byte, error) {
		return []byte("  --daemon-socket <PATH>\n"), probeErr
	}}
	if err := plugin.requireDaemonSocket(context.Background(), "synthetic-prime"); !errors.Is(err, probeErr) || !strings.Contains(err.Error(), "cannot verify prime --daemon-socket support") {
		t.Fatalf("failed help accepted or diagnostic lost: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	plugin.runCommand = func(ctx context.Context, _ string, _ string, _ map[string]string, _ ...string) ([]byte, error) {
		<-ctx.Done()
		return []byte("  --daemon-socket <PATH>\n"), nil
	}
	if err := plugin.requireDaemonSocket(ctx, "synthetic-prime"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timed-out help accepted: %v", err)
	}
	if len(plugin.daemonSocketFlag) != 0 {
		t.Fatal("failed capability probe was cached")
	}
}

func TestPrimeOrdinaryLaunchDaemonSocketCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name string
		help string
		err  error
		flag bool
	}{
		{"supported", "  --daemon-socket <PATH>\n", nil, true},
		{"older CLI", "  --session-dir <PATH>\n", nil, false},
		{"prose only", "See docs for --daemon-socket <PATH>.", nil, false},
		{"failed help", "  --daemon-socket <PATH>\n", errors.New("synthetic probe failure"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := primeTestPlugin()
			p.daemonSocketFlag = nil
			calls := 0
			p.runCommand = func(_ context.Context, _, _ string, _ map[string]string, args ...string) ([]byte, error) {
				calls++
				if !slices.Equal(args, []string{"--help"}) {
					t.Fatalf("unexpected native call: %v", args)
				}
				return []byte(tc.help), tc.err
			}
			dataDir := t.TempDir()
			env := map[string]string{"OPENAI_BASE_URL": "", "OPENAI_API_KEY": "synthetic-project-key"}
			p.AugmentRuntimeEnv(env, dataDir)
			wantSocket := filepath.Join(dataDir, "agent-runtime", "prime-agent", "daemon.sock")
			if env["PRIME_AGENT_DAEMON_SOCKET"] != wantSocket || env["OPENAI_BASE_URL"] != "" || env["OPENAI_API_KEY"] != "synthetic-project-key" {
				t.Fatal("daemon fallback or provider environment changed")
			}
			fresh, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{DataDir: dataDir, Env: env})
			if err != nil {
				t.Fatal(err)
			}
			resume, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{DataDir: dataDir, Env: env, Session: ports.SessionRef{Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "ordinary-native-id"}}})
			if err != nil || !ok {
				t.Fatalf("ordinary resume: %v, %v", ok, err)
			}
			for _, argv := range [][]string{fresh, resume} {
				if slices.Contains(argv, "--daemon-socket") != tc.flag || slices.Contains(argv, wantSocket) != tc.flag {
					t.Fatalf("ordinary daemon flag = %v; supported=%v", argv, tc.flag)
				}
				if len(argv) < 4 || argv[0] != "sh" || !strings.Contains(argv[2], "unset OPENAI_BASE_URL;") || !strings.Contains(argv[2], "export OPENAI_API_KEY=") || slices.Contains(argv, "synthetic-project-key") {
					t.Fatalf("provider override handling changed: %v", argv)
				}
			}
			if !slices.Contains(resume, "--resume") || !slices.Contains(resume, "ordinary-native-id") {
				t.Fatalf("ordinary resume identity lost: %v", resume)
			}
			wantCalls := 1
			if tc.err != nil {
				wantCalls = 2 // Failed probes must not become a cached capability.
			}
			if calls != wantCalls {
				t.Fatalf("help probes = %d, want %d", calls, wantCalls)
			}
			if err := p.requireDaemonSocket(context.Background(), p.resolvedBinary); (err == nil) != tc.flag {
				t.Fatalf("import compatibility was weakened: %v", err)
			}
		})
	}
}

func TestPrimeUnsupportedDaemonSocketBlocksNativePreparationAndRestore(t *testing.T) {
	root, sourceCWD, workspace := t.TempDir(), t.TempDir(), t.TempDir()
	source := writePrimeNativeFixture(t, root, primeSourceID, sourceCWD, "", primeNativeFixtureRows()...)
	writePrimeNativeFixture(t, root, primeForkID, workspace, "", primeForkFixtureRows()...)
	plugin := primeTestPlugin()
	plugin.daemonSocketFlag = nil
	calls := 0
	plugin.runCommand = func(_ context.Context, _ string, _ string, _ map[string]string, args ...string) ([]byte, error) {
		calls++
		if !slices.Equal(args, []string{"--help"}) {
			t.Fatalf("unsupported binary reached native fork: %v", args)
		}
		return []byte("See docs for --daemon-socket <PATH> support."), nil
	}
	ref, err := plugin.PrepareImportedSession(context.Background(), ports.ImportedSessionRelocation{NativeSessionID: primeSourceID, ConfigDir: root, TranscriptPath: source, SourceCWD: sourceCWD, LaunchCWD: workspace, DataDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "does not advertise --daemon-socket") || ref.NativeSessionID != "" {
		t.Fatalf("unsupported preparation = %#v, %v", ref, err)
	}
	argv, restored, err := plugin.GetRestoreCommand(context.Background(), primeTestRestoreConfig(t, root, workspace, primeForkID))
	if err == nil || !strings.Contains(err.Error(), "does not advertise --daemon-socket") || restored || len(argv) != 0 || calls != 1 {
		t.Fatalf("unsupported restore = %v, %v, %v, probes=%d", argv, restored, err, calls)
	}
}

func TestPrimeNativeInventoryRefusesOverflowWithoutPartialResults(t *testing.T) {
	root, workspace := t.TempDir(), t.TempDir()
	selected := writePrimeNativeFixture(t, root, primeForkID, workspace, "", primeForkFixtureRows()...)
	for i := 1; i < maxPrimeSessionFiles; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("%05d.jsonl", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := primeSessionFiles(context.Background(), root)
	if err != nil || len(files) != maxPrimeSessionFiles || !files[selected] {
		t.Fatalf("at-limit inventory = %d files, %v", len(files), err)
	}
	if err := os.WriteFile(filepath.Join(root, "overflow.jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	files, err = primeSessionFiles(context.Background(), root)
	if err == nil || !strings.Contains(err.Error(), "inventory exceeds the limit") || len(files) != 0 {
		t.Fatalf("overflow inventory = %d files, %v", len(files), err)
	}
	path, found, err := (&Plugin{}).LocateTranscript(context.Background(), ports.NativeSessionRef{NativeSessionID: primeForkID, ConfigDir: root})
	if err == nil || !strings.Contains(err.Error(), "inventory exceeds the limit") || found || path != "" {
		t.Fatalf("overflow selected a native session: %q, %v, %v", path, found, err)
	}
}

func TestPrimeCommandSessionDirUsesFlagName(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"reordered with extra flags", []string{"prime", "--model", "synthetic", "--offline", "--session-dir", "old", "--daemon-socket", "socket"}, []string{"prime", "--model", "synthetic", "--offline", "--session-dir", "new", "--daemon-socket", "socket"}},
		{"equals", []string{"prime", "--offline", "--session-dir=old"}, []string{"prime", "--offline", "--session-dir=new"}},
		{"prompt containing flag", []string{"prime", "--append-system-prompt", "--session-dir", "--session-dir", "old"}, []string{"prime", "--append-system-prompt", "--session-dir", "--session-dir", "new"}},
		{"missing", []string{"prime", "--offline"}, nil},
		{"missing value", []string{"prime", "--session-dir"}, nil},
		{"empty equals", []string{"prime", "--session-dir="}, nil},
		{"next flag", []string{"prime", "--session-dir", "--offline"}, nil},
		{"duplicate", []string{"prime", "--session-dir", "old", "--session-dir=other"}, nil},
		{"positional flag text", []string{"prime", "--", "--session-dir", "old"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := slices.Clone(tc.args)
			got, err := primeCommandSessionDir(tc.args, "new")
			if (err != nil) != (tc.want == nil) || !slices.Equal(got, tc.want) || !slices.Equal(tc.args, original) {
				t.Fatalf("session-dir rewrite = %v, %v; want %v; original=%v", got, err, tc.want, tc.args)
			}
		})
	}
}
