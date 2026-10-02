//go:build linux || darwin

package persistenthost

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestProviderOwnerGroupReceiptRequiresRetirementProof(t *testing.T) {
	boot, err := providerBootID()
	if err != nil {
		t.Fatal(err)
	}
	for _, previousBoot := range []bool{false, true} {
		name := "same-boot"
		if previousBoot {
			name = "previous-boot"
		}
		t.Run(name, func(t *testing.T) {
			for _, entry := range []string{"exact", "unbound", "host-finish"} {
				t.Run(entry, func(t *testing.T) {
					dataDir, sessionID := t.TempDir(), "group-receipt"
					identity := descriptorIdentity(Descriptor{Token: "receipt-fixture"})
					owner, err := beginProviderOwner(dataDir, sessionID, identity)
					if err != nil {
						t.Fatal(err)
					}
					owner.State, owner.Boot = "stopped", boot
					owner.Group, owner.SessionID = max(2, os.Getpid()), max(2, os.Getpid())
					owner.Members = []providerProcessIdentity{{PID: owner.Group, Start: 1}}
					if previousBoot {
						owner.Boot = uuid.NewString()
					}
					if err := writeProviderOwner(dataDir, owner); err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(t.Context(), time.Second)
					defer cancel()
					switch entry {
					case "exact":
						err = ShutdownExact(ctx, dataDir, sessionID, identity)
					case "unbound":
						err = ShutdownExact(ctx, dataDir, sessionID, "")
					case "host-finish":
						err = finishProviderOwner(ctx, dataDir, owner)
					}
					if previousBoot {
						if err != nil {
							t.Fatal("previous-boot retirement proof rejected", err)
						}
					} else if !errors.Is(err, ErrOwnershipInconclusive) {
						t.Fatal("group-only stopped receipt authorized retirement on the same boot", err)
					}
					stored, readErr := readProviderOwner(dataDir, sessionID, identity)
					if readErr != nil || stored.State != owner.State || stored.Boot != owner.Boot {
						t.Fatal("receipt check rewrote ownership evidence", readErr)
					}
				})
			}
		})
	}
}

func TestProviderOwnerNativeExitKeepsUnresolvedReceipt(t *testing.T) {
	for _, protocol := range []Protocol{ProtocolRaw, ProtocolACP} {
		name := string(protocol)
		if protocol == ProtocolRaw {
			name = "raw"
		}
		t.Run(name, func(t *testing.T) {
			cfg := Config{SessionID: "native-retirement", DataDir: t.TempDir(), Workdir: t.TempDir(),
				Env:  append(os.Environ(), "AO_CHAT_HOST_PROVIDER_HELPER=1"),
				Argv: []string{os.Args[0], "-test.run=TestProviderHelper"}, Protocol: protocol}
			if protocol == ProtocolACP {
				cfg.Env = append(cfg.Env, "AO_CHAT_HOST_ACP_HELPER=1")
			}
			start := func() (Descriptor, <-chan error) {
				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				done := make(chan error, 1)
				go func() { done <- Run(ctx, cfg) }()
				return awaitDescriptor(t, cfg.DataDir, cfg.SessionID), done
			}
			stop := func(done <-chan error) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := Shutdown(ctx, cfg.DataDir, cfg.SessionID); err != nil {
					t.Fatal("native shutdown failed", err)
				}
				select {
				case err := <-done:
					if err != nil {
						t.Fatal("native host exit changed", err)
					}
				case <-ctx.Done():
					t.Fatal("native host did not join", ctx.Err())
				}
			}
			descriptor, done := start()
			stop(done)
			identity := descriptorIdentity(descriptor)
			owner, err := readProviderOwner(cfg.DataDir, cfg.SessionID, identity)
			if err != nil || owner.State != "active" {
				t.Fatal("native shutdown manufactured exact-retirement evidence", owner.State, err)
			}
			replacement, done := start()
			t.Cleanup(func() { stop(done) })
			for range 3 {
				if err := ShutdownExact(t.Context(), cfg.DataDir, cfg.SessionID, identity); !errors.Is(err, ErrOwnershipInconclusive) {
					t.Fatal("retry converted native shutdown to exact retirement", err)
				}
			}
			current, err := readDescriptor(cfg.DataDir, cfg.SessionID)
			if err != nil || descriptorIdentity(current) != descriptorIdentity(replacement) {
				t.Fatal("exact retry changed the replacement", err)
			}
			transport := awaitAttach(t, replacement)
			defer func() { _ = transport.Stdin.Close() }()
		})
	}
}
