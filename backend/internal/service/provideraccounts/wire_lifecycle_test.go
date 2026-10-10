package provideraccounts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/proxyhost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// This fixture is a protocol peer, not a provider emulator. The account service,
// SQLite journal, and private HTTP client are real; only the detached SDK host
// is replaced so failures cannot contact a provider or local login.
type accountWirePeer struct {
	mu          sync.Mutex
	key         string
	snapshot    ports.ProviderRouteSnapshot
	credentials map[string]bool
	deleted     []string
	requests    []string
	applyMode   string
	deleteMode  string
	busy        bool
}

func (p *accountWirePeer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+p.key {
		http.Error(w, "control identity required", http.StatusUnauthorized)
		return
	}
	p.requests = append(p.requests, r.Method+" "+r.URL.Path)
	w.Header().Set("Content-Type", "application/json")
	switch r.Method + " " + r.URL.Path {
	case "GET /ao/status":
		_, _ = io.WriteString(w, `{"protocol_version":2}`)
	case "PUT /ao/routes":
		var next ports.ProviderRouteSnapshot
		if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
			http.Error(w, "bad routing body", http.StatusBadRequest)
			return
		}
		next.RequestBoundary = false
		if p.busy {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"code":"SESSION_BUSY"}`)
			return
		}
		if p.applyMode == "reject" {
			http.Error(w, "private upstream detail", http.StatusServiceUnavailable)
			return
		}
		if next.Revision != p.snapshot.Revision && next.Revision != p.snapshot.Revision+1 {
			http.Error(w, "revision conflict", http.StatusConflict)
			return
		}
		if next.Revision == p.snapshot.Revision && !reflect.DeepEqual(next, p.snapshot) {
			http.Error(w, "conflicting replay", http.StatusConflict)
			return
		}
		p.snapshot = clone(next)
		switch p.applyMode {
		case "lost":
			http.Error(w, "acknowledgement lost", http.StatusServiceUnavailable)
		case "malformed":
			_, _ = io.WriteString(w, "{")
		case "wrong-revision":
			next.Revision++
			_ = json.NewEncoder(w).Encode(next)
		case "wrong-account":
			if len(next.Routes) != 0 {
				next.Routes[0].AuthID = "different-account"
			}
			_ = json.NewEncoder(w).Encode(next)
		default:
			_ = json.NewEncoder(w).Encode(next)
		}
	case "DELETE /v8/management/credentials":
		name := r.URL.Query().Get("name")
		if p.deleteMode == "reject" {
			http.Error(w, "credential cleanup refused", http.StatusServiceUnavailable)
			return
		}
		if !p.credentials[name] {
			http.Error(w, "already absent", http.StatusNotFound)
			return
		}
		delete(p.credentials, name)
		p.deleted = append(p.deleted, name)
		if p.deleteMode == "lost" {
			http.Error(w, "deletion acknowledgement lost", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	case "GET /v8/management/credentials":
		files := make([]map[string]string, 0, len(p.credentials))
		for name := range p.credentials {
			files = append(files, map[string]string{"name": name})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"files": files})
	default:
		http.Error(w, "unexpected protocol operation", http.StatusNotFound)
	}
}

type accountWireHarness struct {
	root   string
	data   string
	store  *sqlite.Store
	client *proxyhost.Client
	peer   *accountWirePeer
	guard  *fakeGuard
	svc    *Service
	ctx    context.Context
	nextID int
}

func newAccountWireHarness(t *testing.T) *accountWireHarness {
	t.Helper()
	h := &accountWireHarness{root: filepath.Join(t.TempDir(), "proxy"), data: t.TempDir(), ctx: context.Background()}
	var err error
	h.client, err = proxyhost.New(h.root, "")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := os.ReadFile(filepath.Join(h.root, "run", "host.json"))
	if err != nil {
		t.Fatal(err)
	}
	var private struct {
		ControlKey string `json:"control_key"`
	}
	if err := json.Unmarshal(identity, &private); err != nil {
		t.Fatal(err)
	}
	h.peer = &accountWirePeer{key: private.ControlKey, credentials: make(map[string]bool), snapshot: ports.ProviderRouteSnapshot{Routes: []ports.ProviderRoute{}}}
	listener, err := net.Listen("tcp", strings.TrimPrefix(h.client.Endpoint(), "http://"))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: h.peer, ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	h.store, err = sqlitetest.Open(h.data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.store.Close() })
	h.guard = &fakeGuard{busy: make(map[domain.SessionID]bool)}
	h.replaceService(t)
	return h
}

func (h *accountWireHarness) replaceService(t *testing.T) {
	t.Helper()
	key, err := h.client.TicketKey()
	if err != nil {
		t.Fatal(err)
	}
	h.svc = New(h.store, h.client, h.guard, key, h.client.Endpoint(), func() string {
		h.nextID++
		return fmt.Sprintf("wire-account-%d", h.nextID)
	})
}

func (h *accountWireHarness) restartDaemon(t *testing.T) {
	t.Helper()
	beforeEndpoint := h.client.Endpoint()
	beforeKey, err := h.client.TicketKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.Close(); err != nil {
		t.Fatal(err)
	}
	h.store, err = sqlite.OpenPreMigrated(h.data)
	if err != nil {
		t.Fatal(err)
	}
	h.client, err = proxyhost.New(h.root, "")
	if err != nil {
		t.Fatal(err)
	}
	afterKey, err := h.client.TicketKey()
	if err != nil || !reflect.DeepEqual(beforeKey, afterKey) || h.client.Endpoint() != beforeEndpoint {
		t.Fatal("daemon restart replaced the live helper identity")
	}
	h.replaceService(t)
}

func (h *accountWireHarness) login(t *testing.T, provider, email, existing string) string {
	t.Helper()
	ref := provider + "-" + email + ".json"
	h.peer.mu.Lock()
	h.peer.credentials[ref] = true
	h.peer.mu.Unlock()
	id, err := recordLogin(h.ctx, h.svc, provider, email, ref, provider+"-"+email+"-auth", existing)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (h *accountWireHarness) assign(t *testing.T, session domain.SessionID, harness domain.AgentHarness, id string) map[string]string {
	t.Helper()
	if err := h.svc.AssignAccount(h.ctx, session, harness, id); err != nil {
		t.Fatal(err)
	}
	env, err := h.svc.LaunchAccountEnv(h.ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func (h *accountWireHarness) assertAssignment(t *testing.T, session domain.SessionID, id string, originalEnv map[string]string) {
	t.Helper()
	route, managed, err := h.svc.SessionAccount(h.ctx, session)
	if err != nil || !managed || route.AccountID != id {
		t.Fatalf("session %s assignment=%s managed=%v error=%v", session, route.AccountID, managed, err)
	}
	env, err := h.svc.LaunchAccountEnv(h.ctx, session)
	if err != nil || !reflect.DeepEqual(env, originalEnv) {
		t.Fatalf("session %s requires a process environment replacement", session)
	}
	ticket := env["AO_PROXY_TICKET"]
	if route.Provider == "claude" {
		ticket = env["ANTHROPIC_AUTH_TOKEN"]
	}
	digest := sha256.Sum256([]byte(ticket))
	if route.TicketHash != hex.EncodeToString(digest[:]) || ticket == "" {
		t.Fatal("durable route does not match the native process ticket")
	}
	state, pending, err := h.store.LoadProviderAccountState(h.ctx)
	if err != nil || pending != nil {
		t.Fatalf("unexpected unfinished journal: %v", err)
	}
	wantedAuth := ""
	if id != "" {
		entry, found := account(state, id)
		if !found {
			t.Fatal("assignment refers to an absent account")
		}
		wantedAuth = entry.AuthID
	}
	h.peer.mu.Lock()
	defer h.peer.mu.Unlock()
	if h.peer.snapshot.Revision != state.Revision {
		t.Fatal("AO committed a revision that its HTTP peer has not accepted")
	}
	found := false
	for _, remote := range h.peer.snapshot.Routes {
		if remote.SessionID != session {
			continue
		}
		found = true
		if remote.Provider != route.Provider || remote.TicketHash != route.TicketHash || remote.AuthID != wantedAuth {
			t.Fatal("remote mapping differs from the durable session assignment")
		}
		if remote.TicketHash == ticket {
			t.Fatal("private control snapshot contains a raw process ticket")
		}
	}
	if !found {
		t.Fatal("live session is missing from the complete remote snapshot")
	}
}

func TestAccountWireLifecyclePreservesTwoProvidersAndNativeSessionsAcrossRestart(t *testing.T) {
	h := newAccountWireHarness(t)
	alice := h.login(t, "codex", "alice@example.test", "")
	bob := h.login(t, "codex", "bob@example.test", "")
	clara := h.login(t, "claude", "clara@example.test", "")
	dan := h.login(t, "claude", "dan@example.test", "")
	codexEnv := h.assign(t, "codex-old", domain.HarnessCodex, alice)
	claudeEnv := h.assign(t, "claude-old", domain.HarnessClaudeCode, clara)
	h.guard.busy["codex-old"] = true
	h.guard.busy["claude-old"] = true
	if err := h.svc.SetPrimary(h.ctx, bob); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.SetPrimary(h.ctx, dan); err != nil {
		t.Fatal(err)
	}
	h.assertAssignment(t, "codex-old", alice, codexEnv)
	h.assertAssignment(t, "claude-old", clara, claudeEnv)
	for _, choice := range []struct {
		harness domain.AgentHarness
		wanted  string
	}{{domain.HarnessCodex, bob}, {domain.HarnessClaudeCode, dan}} {
		id, managed, err := h.svc.ResolveAccount(h.ctx, choice.harness, "")
		if err != nil || !managed || id != choice.wanted {
			t.Fatalf("new primary resolution=%s managed=%v error=%v", id, managed, err)
		}
	}
	newCodexEnv := h.assign(t, "codex-new", domain.HarnessCodex, bob)
	newClaudeEnv := h.assign(t, "claude-new", domain.HarnessClaudeCode, dan)
	h.restartDaemon(t)
	if err := h.svc.Recover(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.assertAssignment(t, "codex-old", alice, codexEnv)
	h.assertAssignment(t, "claude-old", clara, claudeEnv)
	h.assertAssignment(t, "codex-new", bob, newCodexEnv)
	h.assertAssignment(t, "claude-new", dan, newClaudeEnv)
	if err := h.svc.Switch(h.ctx, "codex-old", bob); err != nil {
		t.Fatalf("active request-boundary switch=%v", err)
	}
	h.assertAssignment(t, "codex-old", bob, codexEnv)
	h.assertAssignment(t, "claude-old", clara, claudeEnv)
	if err := h.svc.Switch(h.ctx, "native-old", bob); !errors.Is(err, ports.ErrProviderAccountIncompatible) {
		t.Fatalf("native session adoption=%v", err)
	}
	native, err := h.svc.LaunchAccountEnv(h.ctx, "native-old")
	if err != nil || native != nil {
		t.Fatal("account management changed an older native session")
	}
	// Destructive credential removal still waits for the active request to finish;
	// request-boundary rebinding above does not need that native idle fence.
	h.guard.busy["codex-old"] = false
	if err := h.svc.Remove(h.ctx, bob, alice, true); err != nil {
		t.Fatal(err)
	}
	h.assertAssignment(t, "codex-old", alice, codexEnv)
	h.assertAssignment(t, "codex-new", alice, newCodexEnv)
	h.assertAssignment(t, "claude-new", dan, newClaudeEnv)
	if err := h.svc.Remove(h.ctx, alice, "", false); err != nil {
		t.Fatal(err)
	}
	h.assertAssignment(t, "codex-old", "", codexEnv)
	h.assertAssignment(t, "codex-new", "", newCodexEnv)
	if _, managed, err := h.svc.ResolveAccount(h.ctx, domain.HarnessCodex, ""); !managed || !errors.Is(err, ports.ErrProviderLoginRequired) {
		t.Fatalf("last-account removal allowed native fallback: %v", err)
	}
	h.restartDaemon(t)
	if err := h.svc.Recover(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.assertAssignment(t, "codex-old", "", codexEnv)
	restored := h.login(t, "codex", "bob@example.test", bob)
	if restored != bob {
		t.Fatal("signed-out re-login changed account catalogue identity")
	}
	h.assertAssignment(t, "codex-old", bob, codexEnv)
	h.assertAssignment(t, "codex-new", bob, newCodexEnv)
	h.assertAssignment(t, "claude-old", clara, claudeEnv)
	h.assertAssignment(t, "claude-new", dan, newClaudeEnv)
	if h.guard.released != len(h.guard.acquired) {
		t.Fatal("wire lifecycle retained an idle-session fence")
	}
}

func TestAccountWireLostAndInvalidAcknowledgementsRemainRecoverableAfterRealDatabaseReopen(t *testing.T) {
	for _, mode := range []string{"reject", "lost", "malformed", "wrong-revision", "wrong-account"} {
		t.Run(mode, func(t *testing.T) {
			h := newAccountWireHarness(t)
			alice := h.login(t, "claude", "alice@example.test", "")
			bob := h.login(t, "claude", "bob@example.test", "")
			env := h.assign(t, "persistent", domain.HarnessClaudeCode, alice)
			before, err := h.svc.State(h.ctx)
			if err != nil {
				t.Fatal(err)
			}
			h.peer.mu.Lock()
			h.peer.applyMode = mode
			h.peer.mu.Unlock()
			if err := h.svc.Switch(h.ctx, "persistent", bob); err == nil {
				t.Fatal("invalid acknowledgement was accepted")
			}
			effective, pending, err := h.store.LoadProviderAccountState(h.ctx)
			if err != nil || pending == nil || !reflect.DeepEqual(effective, before) {
				t.Fatal("invalid acknowledgement committed facts or lost the recovery intent")
			}
			if _, _, err := h.svc.ResolveAccount(h.ctx, domain.HarnessClaudeCode, bob); !errors.Is(err, ports.ErrProviderAccountRecovery) {
				t.Fatalf("new session ignored interrupted routing: %v", err)
			}
			h.restartDaemon(t)
			h.peer.mu.Lock()
			h.peer.applyMode = ""
			h.peer.mu.Unlock()
			if err := h.svc.Recover(h.ctx); err != nil {
				t.Fatal(err)
			}
			h.assertAssignment(t, "persistent", bob, env)
			final, err := h.svc.State(h.ctx)
			if err != nil || final.Revision != before.Revision+1 {
				t.Fatal("recovery duplicated or skipped the admitted revision")
			}
			if err := h.svc.Recover(h.ctx); err != nil {
				t.Fatal(err)
			}
			again, err := h.svc.State(h.ctx)
			if err != nil || !reflect.DeepEqual(final, again) {
				t.Fatal("completed recovery changed durable facts a second time")
			}
		})
	}
}

func TestAccountWireDeletionLossDoesNotDeleteBeforeReassignmentAndCanVerifyAbsentCredential(t *testing.T) {
	for _, mode := range []string{"reject", "lost"} {
		t.Run(mode, func(t *testing.T) {
			h := newAccountWireHarness(t)
			alice := h.login(t, "codex", "alice@example.test", "")
			bob := h.login(t, "codex", "bob@example.test", "")
			env := h.assign(t, "secondary", domain.HarnessCodex, bob)
			h.peer.mu.Lock()
			h.peer.deleteMode = mode
			h.peer.mu.Unlock()
			if err := h.svc.Remove(h.ctx, bob, "", false); err == nil {
				t.Fatal("lost credential cleanup acknowledgement was hidden")
			}
			state, pending, err := h.store.LoadProviderAccountState(h.ctx)
			if err != nil || pending == nil || pending.DeleteCredential != "codex-bob@example.test.json" {
				t.Fatal("credential cleanup obligation was not retained")
			}
			if _, found := account(state, bob); found {
				t.Fatal("accepted reassignment did not commit account removal")
			}
			route, managed, err := h.svc.SessionAccount(h.ctx, "secondary")
			if err != nil || !managed || route.AccountID != alice {
				t.Fatal("cleanup failure rolled the session back to a removed credential")
			}
			h.peer.mu.Lock()
			for _, remote := range h.peer.snapshot.Routes {
				if remote.SessionID == "secondary" && remote.AuthID != "codex-alice@example.test-auth" {
					t.Error("credential deletion ran before remote session reassignment")
				}
			}
			h.peer.deleteMode = ""
			h.peer.mu.Unlock()
			h.restartDaemon(t)
			if err := h.svc.Recover(h.ctx); err != nil {
				t.Fatal(err)
			}
			h.assertAssignment(t, "secondary", alice, env)
			h.peer.mu.Lock()
			defer h.peer.mu.Unlock()
			if h.peer.credentials["codex-bob@example.test.json"] || len(h.peer.deleted) != 1 {
				t.Fatal("cleanup did not delete the intended credential exactly once")
			}
			if !h.peer.credentials["codex-alice@example.test.json"] {
				t.Fatal("recovery deleted the replacement primary")
			}
			if mode == "lost" {
				seenListing := false
				for _, request := range h.peer.requests {
					seenListing = seenListing || request == "GET /v8/management/credentials"
				}
				if !seenListing {
					t.Fatal("lost delete acknowledgement was not verified against inventory")
				}
			}
		})
	}
}

func TestAccountWireBusyRefusalIsNotReplayedAfterDaemonRestart(t *testing.T) {
	h := newAccountWireHarness(t)
	alice := h.login(t, "codex", "alice@example.test", "")
	bob := h.login(t, "codex", "bob@example.test", "")
	env := h.assign(t, "s", domain.HarnessCodex, bob)
	h.peer.mu.Lock()
	h.peer.busy = true
	h.peer.mu.Unlock()
	if err := h.svc.Remove(h.ctx, bob, "", true); !errors.Is(err, ports.ErrProviderAccountBusy) {
		t.Fatalf("helper busy error=%v", err)
	}
	required, err := h.svc.RecoveryRequired(h.ctx)
	if err != nil || required {
		t.Fatal("HTTP busy refusal became a queued account mutation")
	}
	h.peer.mu.Lock()
	h.peer.busy = false
	h.peer.mu.Unlock()
	h.restartDaemon(t)
	if err := h.svc.Recover(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.assertAssignment(t, "s", bob, env)
	state, err := h.svc.State(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	entry, found := account(state, bob)
	if !found || entry.CredentialRef == "" {
		t.Fatal("busy sign-out happened without a fresh user retry")
	}
	if err := h.svc.Remove(h.ctx, bob, "", true); err != nil {
		t.Fatal(err)
	}
	h.assertAssignment(t, "s", alice, env)
}
