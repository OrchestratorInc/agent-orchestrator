//go:build linux

package persistenthost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestProviderOwnerEscapeHelper(t *testing.T) {
	role, dir := os.Getenv("AO_OWNER_ESCAPE_ROLE"), os.Getenv("AO_OWNER_ESCAPE_DIR")
	if role == "" {
		return
	}
	if role == "child" {
		for {
			time.Sleep(time.Hour)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "ready"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "release")); err == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestProviderOwnerEscapeHelper$")
	child.Env = append(os.Environ(), "AO_OWNER_ESCAPE_ROLE=child")
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "child"), []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}

func TestProviderOwnerEscapedDescendantBlocksRetirement(t *testing.T) {
	for _, mode := range []string{"exact-retirement", "account-deletion"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			dataDir, dir := t.TempDir(), t.TempDir()
			st := sqlitetest.MustOpenAt(t, dataDir)
			if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "escape-project", Path: dataDir, RegisteredAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			rec, err := st.CreateSession(ctx, domain.SessionRecord{ProjectID: "escape-project", Harness: domain.HarnessCodex,
				Mode: domain.SessionModeChat, Kind: domain.KindWorker, CreatedAt: time.Now(), Activity: domain.Activity{State: domain.ActivityActive},
				Metadata: domain.SessionMetadata{ControllerGeneration: "original-controller", WorkspacePath: dataDir}})
			if err != nil {
				t.Fatal(err)
			}
			provider := domain.AccountsManagerProviderCodex
			if _, _, err := st.GetOrCreateAccountsManagerSessionRoute(ctx, domain.AccountsManagerSessionRoute{SessionID: rec.ID, Provider: provider,
				Mode: domain.AccountsManagerManaged, AccountID: "account-a"}); err != nil {
				t.Fatal(err)
			}
			parent := exec.Command(os.Args[0], "-test.run=^TestProviderOwnerEscapeHelper$")
			parent.Env = append(os.Environ(), "AO_OWNER_ESCAPE_ROLE=parent", "AO_OWNER_ESCAPE_DIR="+dir)
			parent.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := parent.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = parent.Process.Kill(); _ = parent.Wait() })
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("escape leader did not become ready")
				}
				time.Sleep(time.Millisecond)
			}
			owner, err := beginProviderOwner(dataDir, string(rec.ID), strings.Repeat("d", 64))
			if err != nil {
				t.Fatal(err)
			}
			if err := captureProviderOwner(&owner, parent.Process.Pid); err != nil {
				t.Fatal(err)
			}
			if err := writeProviderOwner(dataDir, owner); err != nil {
				t.Fatal(err)
			}
			if err := st.RecordAccountsManagerChatHost(ctx, rec.ID, provider, rec.Metadata.ControllerGeneration, owner.Identity); err != nil {
				t.Fatal(err)
			}
			router := &removalCrashRouter{store: st}
			newManager := func() *sessionmanager.Manager {
				chat := chatsvc.New(chatsvc.Options{Store: st, Sessions: st, StopExactProviderHost: func(ctx context.Context, id domain.SessionID, identity string) error {
					return ShutdownExact(ctx, dataDir, string(id), identity)
				}})
				return sessionmanager.New(sessionmanager.Deps{Store: st, Chat: removalCrashChat{chat}, Lifecycle: lifecycle.New(st, nil),
					AccountsManager: router, DataDir: dataDir, BackgroundContext: ctx})
			}
			observed, resume := make(chan struct{}), make(chan struct{})
			var once sync.Once
			barrier := func(current providerOwner) {
				if current.Identity == owner.Identity {
					once.Do(func() { close(observed); <-resume })
				}
			}
			providerCensusBarrierForTest.Store(&barrier)
			t.Cleanup(func() { providerCensusBarrierForTest.Store(nil) })
			result := make(chan error, 1)
			m := newManager()
			if mode == "exact-retirement" {
				go func() { result <- ShutdownExact(ctx, dataDir, string(rec.ID), owner.Identity) }()
			} else {
				impact, err := st.AccountsManagerRemovalImpact(ctx, "account-a")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := m.StartAccountsManagerRemoval(ctx, "escape-delete", "account-a", impact.Revision, true); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-observed:
			case <-time.After(5 * time.Second):
				close(resume)
				t.Fatal("retirement never reached the census barrier")
			}
			if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0o600); err != nil {
				close(resume)
				t.Fatal(err)
			}
			if err := parent.Wait(); err != nil {
				close(resume)
				t.Fatal(err)
			}
			close(resume)
			raw, err := os.ReadFile(filepath.Join(dir, "child"))
			if err != nil {
				t.Fatal(err)
			}
			childPID, err := strconv.Atoi(string(raw))
			if err != nil {
				t.Fatal(err)
			}
			child, err := readProviderProc(childPID)
			if err != nil || child.group == owner.Group || child.session == owner.SessionID {
				t.Fatal("fixture did not escape the original group and session", err)
			}
			t.Cleanup(func() { _ = signalProviderIdentity(context.Background(), child) })
			if mode == "exact-retirement" {
				if err := <-result; err == nil {
					t.Error("retirement acknowledged while an unrecorded moved descendant survives")
				}
			} else {
				if err := m.WaitAgentSwitchWorkers(ctx); err != nil {
					t.Fatal(err)
				}
				op, found, err := st.GetAccountsManagerRemoval(ctx, "escape-delete")
				if err != nil || !found || op.Phase != domain.AccountsManagerRemovalRecovery || op.Impact.Sessions[0].Stopped || router.finalized.Load() != 0 {
					t.Errorf("account deletion advanced while escaped descendant survives: phase=%s finalized=%d err=%v", op.Phase, router.finalized.Load(), err)
				}
				if op.Phase == domain.AccountsManagerRemovalRecovery {
					if err := st.Close(); err != nil {
						t.Fatal(err)
					}
					st, err = sqlite.OpenPreMigrated(dataDir)
					if err != nil {
						t.Fatal(err)
					}
					defer st.Close()
					router.store = st
					m = newManager()
					if err := m.ReconcileStartupSafety(ctx); err != nil {
						t.Fatal(err)
					}
					if _, err := m.RetryAccountsManagerRemoval(ctx, "escape-delete"); err != nil {
						t.Fatal(err)
					}
					if err := m.WaitAgentSwitchWorkers(ctx); err != nil {
						t.Fatal(err)
					}
					op, _, err = st.GetAccountsManagerRemoval(ctx, "escape-delete")
					if err != nil || op.Phase != domain.AccountsManagerRemovalRecovery || router.finalized.Load() != 0 {
						t.Error("restart authorized deletion without descendant retirement", err)
					}
				}
			}
			stored, err := readProviderOwner(dataDir, string(rec.ID), owner.Identity)
			if err != nil || stored.State == "stopped" {
				t.Error("durable stopped receipt permits a live escaped descendant", err)
			}
			if !removalCrashProcessRunning(childPID) {
				t.Error("fixture descendant was not preserved for the negative assertion")
			}
		})
	}
}
