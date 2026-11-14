package persistenthost

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// A host that died uncleanly (crash, reboot) leaves its descriptor behind, and
// it may have run another harness. Starting the session's next host must wait
// for that host's own descriptor instead of failing on the dead one. A failure
// here used to fail an agent switch's target start and leak the new host,
// which then blocked restoring the source.
func TestConnectOrStartReplacesDeadHostOfAnotherConfiguration(t *testing.T) {
	raw := Config{
		Env:  append(os.Environ(), "AO_CHAT_HOST_PROVIDER_HELPER=1"),
		Argv: []string{os.Args[0], "-test.run=TestProviderHelper"},
	}
	acp := Config{
		Env:  append(os.Environ(), "AO_CHAT_HOST_PROVIDER_HELPER=1", "AO_CHAT_HOST_ACP_HELPER=1"),
		Argv: []string{os.Args[0], "-test.run=TestProviderHelper"}, Protocol: ProtocolACP, OwnershipFingerprint: "owner",
	}
	for _, tc := range []struct {
		name  string
		stale Descriptor
		cfg   Config
	}{
		{"dead ACP host, raw request", Descriptor{Protocol: ProtocolACP, OwnershipFingerprint: "owner"}, raw},
		{"dead raw host, ACP request", Descriptor{Protocol: ProtocolRaw}, acp},
		{"dead ACP host of another launch", Descriptor{Protocol: ProtocolACP, OwnershipFingerprint: "previous"}, acp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			cfg := tc.cfg
			cfg.SessionID, cfg.DataDir, cfg.Workdir = "dead-host", dataDir, t.TempDir()
			stale := tc.stale
			stale.Version, stale.SessionID, stale.Token = ProtocolVersion, cfg.SessionID, "dead-host-token"
			stale.Address, stale.PID, stale.StartedAt = "127.0.0.1:1", deadPID(t), time.Now()
			dir, _ := hostDir(dataDir, cfg.SessionID)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := writeDescriptor(dataDir, stale); err != nil {
				t.Fatal(err)
			}

			transport, err := ConnectOrStart(context.Background(), cfg)
			if err != nil {
				t.Fatalf("ConnectOrStart over a dead host's descriptor: %v", err)
			}
			t.Cleanup(func() { _ = Shutdown(context.Background(), dataDir, cfg.SessionID) })
			_ = transport.Stdin.Close()
			d, err := readDescriptor(dataDir, cfg.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			if d.Token == stale.Token || d.Protocol != cfg.Protocol {
				t.Fatalf("descriptor = protocol %q token %q, want the new %q host", d.Protocol, d.Token, cfg.Protocol)
			}
		})
	}
}

// deadPID returns the PID of a process that has already exited.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}
