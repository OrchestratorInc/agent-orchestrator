package persistenthost

import (
	"context"
	"errors"
	"os"
	"testing"
)

// TestRawHostPinsIdentityAcrossDetach 删除原始协议指纹检查会让新凭据接管旧进程，删除环境传递会无法恢复原宿主。
func TestRawHostPinsIdentityAcrossDetach(t *testing.T) {
	cfg := Config{SessionID: "raw-profile", DataDir: t.TempDir(), Workdir: t.TempDir(), Env: append(os.Environ(), "AO_CHAT_HOST_PROVIDER_HELPER=1"), Argv: []string{os.Args[0], "-test.run=TestProviderHelper"}, OwnershipFingerprint: "private-profile-identity"}
	prepareCalls := 0
	cfg.Prepare = func(context.Context) (PreparedProvider, error) {
		prepareCalls++
		return PreparedProvider{Env: cfg.Env, Argv: cfg.Argv}, nil
	}
	transport, err := ConnectOrStart(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Shutdown(context.Background(), cfg.DataDir, cfg.SessionID) })
	if err := transport.Stdin.Close(); err != nil {
		t.Fatal(err)
	}
	descriptor := awaitDescriptor(t, cfg.DataDir, cfg.SessionID)
	if descriptor.OwnershipFingerprint != cfg.OwnershipFingerprint {
		t.Fatal("raw host lost private identity")
	}
	changed := cfg
	changed.OwnershipFingerprint = "changed-credential"
	if _, err := ConnectOrStart(context.Background(), changed); !errors.Is(err, ErrIncompatible) {
		t.Fatalf("changed identity: %v", err)
	}
	changed.OwnershipFingerprint = ""
	if _, err := ConnectOrStart(context.Background(), changed); !errors.Is(err, ErrIncompatible) {
		t.Fatalf("native fallback: %v", err)
	}
	transport, err = ConnectOrStart(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Stdin.Close()
	if !transport.Reconnected || prepareCalls != 1 {
		t.Fatalf("recovery spawned a replacement: reconnect=%v prepares=%d", transport.Reconnected, prepareCalls)
	}
}
