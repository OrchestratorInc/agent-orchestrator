package persistenthost

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestManagedRawHostRequiresExactFingerprint(t *testing.T) {
	for _, tc := range []struct {
		name, expected, actual string
		compatible             bool
	}{
		{"same managed owner", "managed-a", "managed-a", true},
		{"foreign managed owner", "managed-a", "managed-b", false},
		{"native cannot adopt managed", "", "managed-a", false},
		{"managed cannot adopt native", "managed-a", "", false},
		{"native compatibility", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDescriptor(Config{Protocol: ProtocolRaw, OwnershipFingerprint: tc.expected}, Descriptor{Protocol: ProtocolRaw, OwnershipFingerprint: tc.actual})
			if tc.compatible && err != nil || !tc.compatible && !errors.Is(err, ErrIncompatible) {
				t.Fatalf("raw owner validation = %v, compatible=%v", err, tc.compatible)
			}
		})
	}
}

func TestManagedRawHostDetachedIdentityAndRestart(t *testing.T) {
	cfg := Config{SessionID: "managed-detached", DataDir: t.TempDir(), Workdir: t.TempDir(),
		Protocol: ProtocolManagedRaw, OwnershipFingerprint: "managed-controller-a",
		Env: append(os.Environ(), "AO_CHAT_HOST_PROVIDER_HELPER=1"), Argv: []string{os.Args[0], "-test.run=TestProviderHelper"}}
	first, err := ConnectOrStart(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Stdin.Close(); _ = Shutdown(context.Background(), cfg.DataDir, cfg.SessionID) })
	d, err := readDescriptor(cfg.DataDir, cfg.SessionID)
	if err != nil || d.OwnershipFingerprint != cfg.OwnershipFingerprint || d.Protocol != cfg.Protocol || first.HostIdentity() == "" {
		t.Fatal("detached launch lost its managed identity", err)
	}
	pid := requestProviderPID(t, first, 10, "pid")
	if err := first.Stdin.Close(); err != nil {
		t.Fatal(err)
	}
	for _, foreign := range []Config{
		{SessionID: cfg.SessionID, DataDir: cfg.DataDir, Protocol: cfg.Protocol, OwnershipFingerprint: "managed-controller-b"},
		{SessionID: cfg.SessionID, DataDir: cfg.DataDir},
	} {
		if _, err := ConnectOrStart(t.Context(), foreign); !errors.Is(err, ErrIncompatible) {
			t.Fatal("foreign generation or native controller could adopt managed host", err)
		}
	}
	cfg.Prepare = func(context.Context) (PreparedProvider, error) {
		t.Error("live reattachment rotated launch authorization")
		return PreparedProvider{}, errors.New("must not prepare")
	}
	second, err := ConnectOrStart(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Stdin.Close() }()
	if !second.Reconnected || second.HostIdentity() != first.HostIdentity() || second.NextRequestID < 10 {
		t.Fatal("restart lost managed host or request identity")
	}
	if got := requestProviderPID(t, second, 11, "pid"); got != pid {
		t.Fatal("reattachment launched another provider")
	}
}
