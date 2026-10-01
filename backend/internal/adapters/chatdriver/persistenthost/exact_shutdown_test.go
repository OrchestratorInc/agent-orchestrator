package persistenthost

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
)

func TestRemovalHostAttachmentCarriesNonSecretIdentity(t *testing.T) {
	cfg := Config{SessionID: "removal-identity", DataDir: t.TempDir(), Workdir: t.TempDir(),
		Env: append(os.Environ(), "AO_CHAT_HOST_PROVIDER_HELPER=1"), Argv: []string{os.Args[0], "-test.run=TestProviderHelper"}}
	done := make(chan error, 1)
	go func() { done <- Run(t.Context(), cfg) }()
	d := awaitDescriptor(t, cfg.DataDir, cfg.SessionID)
	t.Cleanup(func() { _ = Shutdown(context.Background(), cfg.DataDir, cfg.SessionID); <-done })
	transport := awaitAttach(t, d)
	defer transport.Stdin.Close()
	owner, ok := any(transport).(interface{ HostIdentity() string })
	if !ok || owner.HostIdentity() == "" || owner.HostIdentity() == d.Token {
		t.Fatal("attachment lacks a non-secret identity for exact cold teardown")
	}
}

func TestRemovalHostExactShutdownSurvivesReplacementAndReattach(t *testing.T) {
	if !retirementAcceptance {
		t.Skip("deferred retirement acceptance; run with -tags=retirement_acceptance")
	}
	cfg := Config{SessionID: "removal-restart", DataDir: t.TempDir(), Workdir: t.TempDir(),
		Env: append(os.Environ(), "AO_CHAT_HOST_PROVIDER_HELPER=1"), Argv: []string{os.Args[0], "-test.run=TestProviderHelper"}}
	start := func() (<-chan error, Descriptor) {
		done := make(chan error, 1)
		go func() { done <- Run(t.Context(), cfg) }()
		return done, awaitDescriptor(t, cfg.DataDir, cfg.SessionID)
	}
	done, d := start()
	first := awaitAttach(t, d)
	identity := first.HostIdentity()
	if err := first.Stdin.Close(); err != nil {
		t.Fatal(err)
	}
	reconnected := awaitAttach(t, d)
	if reconnected.HostIdentity() != identity {
		t.Fatal("attachment changed durable host identity")
	}
	if err := ShutdownExact(t.Context(), cfg.DataDir, cfg.SessionID, ""); !errors.Is(err, ErrOwnershipInconclusive) {
		t.Fatal("missing identity authorized teardown", err)
	}
	if err := ShutdownExact(t.Context(), cfg.DataDir, cfg.SessionID, identity); err != nil {
		t.Fatal(err)
	}
	_ = reconnected.Stdin.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := ShutdownExact(t.Context(), cfg.DataDir, cfg.SessionID, identity); err != nil {
		t.Fatal("confirmed absence was not idempotent", err)
	}
	done, replacement := start()
	t.Cleanup(func() { _ = Shutdown(context.Background(), cfg.DataDir, cfg.SessionID); <-done })
	for range 3 {
		if err := ShutdownExact(t.Context(), cfg.DataDir, cfg.SessionID, identity); err != nil {
			t.Fatal(err)
		}
	}
	attached := awaitAttach(t, replacement)
	defer attached.Stdin.Close()
	if attached.HostIdentity() == identity {
		t.Fatal("replacement reused retired identity")
	}
}

func TestRemovalHostMissingDescriptorDoesNotProveAbsence(t *testing.T) {
	cfg := Config{SessionID: "removal-unpublished", DataDir: t.TempDir()}
	path, err := lockPath(cfg.DataDir, cfg.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := hostDir(cfg.DataDir, cfg.SessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ShutdownExact(t.Context(), cfg.DataDir, cfg.SessionID, ""); !errors.Is(err, ErrOwnershipInconclusive) {
		t.Fatal("live unpublished host was acknowledged absent", err)
	}
	if err := os.WriteFile(path, []byte("2147483647"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ShutdownExact(t.Context(), cfg.DataDir, cfg.SessionID, ""); !errors.Is(err, ErrOwnershipInconclusive) {
		t.Fatal("host death was mistaken for proof its provider child stopped", err)
	}
}
