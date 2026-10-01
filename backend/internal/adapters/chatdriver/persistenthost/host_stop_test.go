package persistenthost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedHostFailedBirthDoesNotBlockRetry(t *testing.T) {
	root := t.TempDir()
	err := Run(t.Context(), Config{DataDir: root, SessionID: "managed", Workdir: t.TempDir(),
		Protocol: ProtocolManagedRaw, Argv: []string{filepath.Join(root, "missing-provider")},
	})
	if !os.IsNotExist(err) {
		t.Fatal("failed birth lost its missing-executable error", err)
	}
	if err := confirmManagedHostStops(root, "managed"); err != nil {
		t.Fatal("failed birth permanently blocked retry", err)
	}
}

func TestManagedHostColdLaunchRejectsUnjoinedOwner(t *testing.T) {
	root := t.TempDir()
	identity := strings.Repeat("b", 64)
	if _, err := beginProviderOwner(root, "managed", identity, ProtocolManagedRaw); err != nil {
		t.Fatal(err)
	}
	prepared := false
	_, err := ConnectOrStart(t.Context(), Config{DataDir: root, SessionID: "managed", Workdir: t.TempDir(), Protocol: ProtocolManagedRaw,
		OwnershipFingerprint: "synthetic-binding",
		Prepare:              func(context.Context) (PreparedProvider, error) { prepared = true; return PreparedProvider{}, nil },
	})
	if !errors.Is(err, ErrOwnershipInconclusive) || prepared {
		t.Fatal("crash before publication minted credentials or admitted a replacement", err)
	}
	if err := recordHostStopped(root, "managed", identity); err != nil {
		t.Fatal(err)
	}
	if err := confirmManagedHostStops(root, "managed"); err != nil {
		t.Fatal("completed owner still blocks recovery", err)
	}
}

func TestHostStopReceiptIsNotRetirementProof(t *testing.T) {
	root := t.TempDir()
	identity := strings.Repeat("a", 64)
	if _, err := beginProviderOwner(root, "managed", identity); err != nil {
		t.Fatal(err)
	}
	if stopped, err := hostStopped(root, "managed", identity); err != nil || stopped {
		t.Fatal("missing receipt accepted", err)
	}
	if err := recordHostStopped(root, "managed", identity); err != nil {
		t.Fatal(err)
	}
	if stopped, err := hostStopped(root, "managed", identity); err != nil || !stopped {
		t.Fatal("complete receipt rejected", err)
	}
	if err := ShutdownHost(t.Context(), root, "managed", identity); err != nil {
		t.Fatal("lost response retry", err)
	}
	if err := ShutdownExact(t.Context(), root, "managed", identity); !errors.Is(err, ErrOwnershipInconclusive) {
		t.Fatal("ordinary stop receipt waived retirement proof", err)
	}
	path, err := providerOwnerPath(root, "managed", identity)
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"", "host-stopped-v1:" + identity, "host-stopped-v1:" + identity + "\nextra", strings.Repeat("a", 256)} {
		if err := os.WriteFile(path+".host-stopped", []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if stopped, err := hostStopped(root, "managed", identity); err == nil || stopped {
			t.Fatal("interrupted or oversized receipt accepted")
		}
	}
}
