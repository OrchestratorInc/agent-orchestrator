package primeagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

func TestTerminateNativeSessionStopsTheActiveOwnerOfTheTranscript(t *testing.T) {
	listJSON := []byte(`{"sessions":[{"id":"active-7","sessionId":"native-7","activeSessionId":"active-7","cwd":"/work","lifecycle":"active","activity":"idle","isSessionActive":true,"isStreaming":false,"isCompacting":false,"attachedClients":0,"messageCount":2,"sessionActions":{"queuedCount":0,"steering":[],"followUps":[]}}]}`)
	var stopped string
	p := &Plugin{
		resolvedBinary: "prime-agent",
		runCommand: func(ctx context.Context, binary, cwd string, env map[string]string, args ...string) ([]byte, error) {
			if _, ok := ctx.Deadline(); !ok {
				return nil, errors.New("command context has no deadline")
			}
			wantPrimeDir := "/ao-data/agent-runtime/prime-agent"
			if binary != "prime-agent" || cwd != wantPrimeDir {
				return nil, fmt.Errorf("binary=%q cwd=%q", binary, cwd)
			}
			if got := env["PRIME_AGENT_CODING_AGENT_DIR"]; got != wantPrimeDir {
				return nil, fmt.Errorf("env=%v", env)
			}
			switch strings.Join(args, " ") {
			case "list --json":
				return listJSON, nil
			case "stop active-7 --json":
				stopped = "active-7"
				return []byte(`{"stopped":true}`), nil
			default:
				return nil, fmt.Errorf("unexpected args: %q", args)
			}
		},
	}
	err := p.TerminateNativeSession(context.Background(), ports.SessionRef{
		WorkspacePath: "/work",
		DataDir:       "/ao-data",
		Metadata: map[string]string{
			ports.MetadataKeyAgentSessionID: "native-7",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if stopped != "active-7" {
		t.Fatalf("stopped = %q, want active-7", stopped)
	}
}

func TestAugmentRuntimeEnvUsesAOPrimeStateRoot(t *testing.T) {
	env := map[string]string{}
	New().AugmentRuntimeEnv(env, "/ao-data")
	if got, want := env["PRIME_AGENT_CODING_AGENT_DIR"], "/ao-data/agent-runtime/prime-agent"; got != want {
		t.Fatalf("PRIME_AGENT_CODING_AGENT_DIR = %q, want %q", got, want)
	}
}

func TestTerminateNativeSessionSafety(t *testing.T) {
	tests := []struct {
		name       string
		nativeID   string
		listOutput string
		listErr    error
		stopErr    error
		wantList   bool
		wantStop   bool
		wantErr    string
	}{
		{name: "inactive transcript", nativeID: "native-7", listOutput: `{"sessions":[]}`, wantList: true},
		{name: "row without active owner", nativeID: "native-7", listOutput: `{"sessions":[{"sessionId":"native-7"}]}`, wantList: true},
		{name: "ambiguous active owners", nativeID: "native-7", listOutput: `{"sessions":[{"sessionId":"native-7","activeSessionId":"a"},{"sessionId":"native-7","activeSessionId":"b"}]}`, wantList: true, wantErr: "multiple live Prime sessions"},
		{name: "malformed list", nativeID: "native-7", listOutput: `{`, wantList: true, wantErr: "decode session list"},
		{name: "list failure", nativeID: "native-7", listErr: errors.New("daemon unavailable"), wantList: true, wantErr: "list live sessions"},
		{name: "stop failure", nativeID: "native-7", listOutput: `{"sessions":[{"sessionId":"native-7","activeSessionId":"active-7"}]}`, stopErr: errors.New("refused"), wantList: true, wantStop: true, wantErr: "stop active session"},
		{name: "blank AO id", nativeID: "   ", listOutput: `not consulted`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			listCalled := false
			stopCalled := false
			p := &Plugin{
				resolvedBinary: "prime-agent",
				runCommand: func(_ context.Context, _, _ string, _ map[string]string, args ...string) ([]byte, error) {
					switch strings.Join(args, " ") {
					case "list --json":
						listCalled = true
						return []byte(tc.listOutput), tc.listErr
					case "stop active-7 --json":
						stopCalled = true
						return []byte(`{"stopped":true}`), tc.stopErr
					default:
						return nil, fmt.Errorf("unexpected args: %q", args)
					}
				},
			}
			err := p.TerminateNativeSession(context.Background(), ports.SessionRef{
				WorkspacePath: "/work",
				DataDir:       "/ao-data",
				Metadata: map[string]string{
					ports.MetadataKeyAgentSessionID: tc.nativeID,
				},
			})
			if tc.wantErr == "" && err != nil {
				t.Fatalf("TerminateNativeSession error = %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("TerminateNativeSession error = %v, want substring %q", err, tc.wantErr)
			}
			if listCalled != tc.wantList || stopCalled != tc.wantStop {
				t.Fatalf("list=%v stop=%v, want list=%v stop=%v", listCalled, stopCalled, tc.wantList, tc.wantStop)
			}
		})
	}
}

func TestTerminateNativeSessionHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &Plugin{
		resolvedBinary: "prime-agent",
		runCommand: func(context.Context, string, string, map[string]string, ...string) ([]byte, error) {
			return nil, errors.New("must not run")
		},
	}
	err := p.TerminateNativeSession(ctx, ports.SessionRef{Metadata: map[string]string{
		ports.MetadataKeyAgentSessionID: "native-7",
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("TerminateNativeSession error = %v, want context.Canceled", err)
	}
}

// A Prime session started before AO pinned its private daemon socket still runs
// on Prime's default socket; terminate must find and stop it there.
func TestTerminateNativeSessionFallsBackToTheDefaultDaemonSocket(t *testing.T) {
	owner := `{"sessions":[{"sessionId":"native-7","activeSessionId":"active-7"}]}`
	other := `{"sessions":[{"sessionId":"native-8","activeSessionId":"active-8"}]}`
	down := errors.New("connect ENOENT daemon.sock")
	type daemon struct {
		list    string
		listErr error
	}
	tests := []struct {
		name          string
		privateSocket bool
		private       daemon
		fallback      daemon
		wantStops     []string
		wantFallback  bool
		wantErr       string
	}{
		{name: "no private socket, default daemon owns", fallback: daemon{list: owner}, wantStops: []string{"default"}, wantFallback: true},
		{name: "private daemon owns", privateSocket: true, private: daemon{list: owner}, fallback: daemon{list: owner}, wantStops: []string{"private"}},
		{name: "private daemon has a newer session, default owns", privateSocket: true, private: daemon{list: other}, fallback: daemon{list: owner}, wantStops: []string{"default"}, wantFallback: true},
		{name: "private daemon has no match, default daemon down", privateSocket: true, private: daemon{list: other}, fallback: daemon{listErr: down}, wantFallback: true},
		{name: "stale private socket, default owns", privateSocket: true, private: daemon{listErr: down}, fallback: daemon{list: owner}, wantStops: []string{"default"}, wantFallback: true},
		{name: "stale private socket, default has no match", privateSocket: true, private: daemon{listErr: down}, fallback: daemon{list: other}, wantFallback: true, wantErr: "list live sessions"},
		{name: "neither daemon running", fallback: daemon{listErr: down}, wantFallback: true, wantErr: "list live sessions"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			primeDir := filepath.Join(dataDir, "agent-runtime", "prime-agent")
			socket := filepath.Join(primeDir, "daemon.sock")
			if tc.privateSocket {
				if err := os.MkdirAll(primeDir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(socket, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var stops []string
			fallbackListed := false
			p := &Plugin{
				resolvedBinary: "prime-agent",
				runCommand: func(_ context.Context, _, _ string, env map[string]string, args ...string) ([]byte, error) {
					if env["PRIME_AGENT_CODING_AGENT_DIR"] != primeDir {
						return nil, fmt.Errorf("env=%v", env)
					}
					target, d := "default", tc.fallback
					if got, ok := env["PRIME_AGENT_DAEMON_SOCKET"]; ok {
						if got != socket {
							return nil, fmt.Errorf("socket=%q", got)
						}
						target, d = "private", tc.private
					}
					switch strings.Join(args, " ") {
					case "list --json":
						if target == "default" {
							fallbackListed = true
						}
						return []byte(d.list), d.listErr
					case "stop active-7 --json":
						stops = append(stops, target)
						return []byte(`{"stopped":true}`), nil
					default:
						return nil, fmt.Errorf("unexpected args: %q", args)
					}
				},
			}
			err := p.TerminateNativeSession(context.Background(), ports.SessionRef{
				DataDir:  dataDir,
				Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "native-7"},
			})
			if tc.wantErr == "" && err != nil {
				t.Fatalf("TerminateNativeSession error = %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("TerminateNativeSession error = %v, want substring %q", err, tc.wantErr)
			}
			if strings.Join(stops, ",") != strings.Join(tc.wantStops, ",") || fallbackListed != tc.wantFallback {
				t.Fatalf("stops=%v fallbackListed=%v, want stops=%v fallbackListed=%v", stops, fallbackListed, tc.wantStops, tc.wantFallback)
			}
		})
	}
}
