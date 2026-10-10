package provideraccounts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type expectedJourneyAccount struct {
	id       string
	provider string
	email    string
	signedIn bool
}
type expectedJourneySession struct {
	provider string
	account  string
	env      map[string]string
}
type accountJourney struct {
	h         accountHarness
	accounts  map[string]expectedJourneyAccount
	primaries map[string]string
	sessions  map[domain.SessionID]expectedJourneySession
	sequence  int
}

func newAccountJourney(t *testing.T) *accountJourney {
	t.Helper()
	j := &accountJourney{h: setupAccounts(t), accounts: map[string]expectedJourneyAccount{}, primaries: map[string]string{}, sessions: map[domain.SessionID]expectedJourneySession{}}
	for _, provider := range []string{"codex", "claude"} {
		for _, name := range []string{"first", "second"} {
			j.add(t, provider, name+"-"+provider+"@example.test", "")
		}
	}
	return j
}

func (j *accountJourney) add(t *testing.T, provider, email, existing string) {
	t.Helper()
	id, err := recordLogin(j.h.ctx, j.h.svc, provider, email, email+".json", email+"-auth", existing)
	if err != nil {
		t.Fatal(err)
	}
	j.accounts[id] = expectedJourneyAccount{id: id, provider: provider, email: email, signedIn: true}
	if j.primaries[provider] == "" {
		j.primaries[provider] = id
		for session, expectation := range j.sessions {
			if expectation.provider == provider && expectation.account == "" {
				expectation.account = id
				j.sessions[session] = expectation
			}
		}
	}
}

func (j *accountJourney) ids(provider string, signedIn bool) []string {
	ids := []string{}
	for id, a := range j.accounts {
		if a.provider == provider && a.signedIn == signedIn {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func journeyHarness(provider string) domain.AgentHarness {
	if provider == "codex" {
		return domain.HarnessCodex
	}
	return domain.HarnessClaudeCode
}

func (j *accountJourney) spawn(t *testing.T, provider, explicit string) {
	t.Helper()
	id, managed, err := j.h.svc.ResolveAccount(j.h.ctx, journeyHarness(provider), explicit)
	wanted := explicit
	if wanted == "" {
		wanted = j.primaries[provider]
	}
	if wanted == "" {
		if id != "" || !managed || !errors.Is(err, ports.ErrProviderLoginRequired) {
			t.Fatalf("no account should require login, got %q/%v/%v", id, managed, err)
		}
		return
	}
	if err != nil || !managed || id != wanted {
		t.Fatalf("spawn choice %q got %q/%v/%v", wanted, id, managed, err)
	}
	j.sequence++
	session := domain.SessionID(fmt.Sprintf("journey-session-%d", j.sequence))
	if err := j.h.svc.AssignAccount(j.h.ctx, session, journeyHarness(provider), id); err != nil {
		t.Fatal(err)
	}
	env, err := j.h.svc.LaunchAccountEnv(j.h.ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	j.sessions[session] = expectedJourneySession{provider: provider, account: wanted, env: env}
}

func (j *accountJourney) remove(t *testing.T, id string, signOut bool) {
	t.Helper()
	a := j.accounts[id]
	replacement := j.primaries[a.provider]
	if replacement == id {
		replacement = ""
		for _, candidate := range j.ids(a.provider, true) {
			if candidate != id {
				replacement = candidate
				break
			}
		}
	}
	if err := j.h.svc.Remove(j.h.ctx, id, replacement, signOut); err != nil {
		t.Fatal(err)
	}
	if j.primaries[a.provider] == id {
		j.primaries[a.provider] = replacement
	}
	for session, expected := range j.sessions {
		if expected.account == id {
			expected.account = j.primaries[a.provider]
			j.sessions[session] = expected
		}
	}
	if signOut {
		a.signedIn = false
		j.accounts[id] = a
	} else {
		delete(j.accounts, id)
	}
}

func (j *accountJourney) check(t *testing.T) {
	t.Helper()
	state, pending, err := j.h.store.LoadProviderAccountState(j.h.ctx)
	if err != nil || pending != nil {
		t.Fatalf("completed journey left recovery required: %v", err)
	}
	if len(state.Accounts) != len(j.accounts) || len(state.Routes) != len(j.sessions) || len(state.Primaries) != 2 {
		t.Fatal("catalogue, assignments, or independent provider adoption diverged")
	}
	if j.h.proxy.snapshot.Revision != state.Revision {
		t.Fatal("completed journey has an unacknowledged revision")
	}
	for _, a := range state.Accounts {
		expected, found := j.accounts[a.ID]
		if !found || a.Provider != expected.provider || a.Email != expected.email {
			t.Fatal("catalogue identity changed during routing")
		}
		if (a.CredentialRef != "" && a.AuthID != "") != expected.signedIn {
			t.Fatal("catalogue signed-in state differs from the user action")
		}
		if !expected.signedIn && (a.CredentialRef != "" || a.AuthID != "") {
			t.Fatal("signed-out entry retains partial credentials")
		}
	}
	for _, provider := range []string{"codex", "claude"} {
		id, adopted := primary(state, provider)
		if !adopted || id != j.primaries[provider] {
			t.Fatalf("provider %s primary diverged", provider)
		}
		if id != "" && !j.accounts[id].signedIn {
			t.Fatal("signed-out entry became a usable primary")
		}
	}
	tickets := make(map[string]domain.SessionID)
	for _, route := range state.Routes {
		expected, found := j.sessions[route.SessionID]
		if !found || route.Provider != expected.provider || route.AccountID != expected.account {
			t.Fatalf("session %s changed unexpectedly", route.SessionID)
		}
		env, err := j.h.svc.LaunchAccountEnv(j.h.ctx, route.SessionID)
		if err != nil || !reflect.DeepEqual(env, expected.env) {
			t.Fatal("routing changed a native process's environment")
		}
		ticket := env["AO_PROXY_TICKET"]
		if expected.provider == "claude" {
			ticket = env["ANTHROPIC_AUTH_TOKEN"]
			for _, variable := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "AO_PROXY_TICKET", "AO_PROXY_ENDPOINT"} {
				value, cleared := env[variable]
				if !cleared || value != "" {
					t.Fatalf("managed Claude launch retained ambient setting %s", variable)
				}
			}
		}
		digest := sha256.Sum256([]byte(ticket))
		if len(ticket) != 64 || route.TicketHash != hex.EncodeToString(digest[:]) {
			t.Fatal("native ticket and durable route disagree")
		}
		if owner, exists := tickets[ticket]; exists && owner != route.SessionID {
			t.Fatal("two sessions share a routing capability")
		}
		tickets[ticket] = route.SessionID
		remoteFound := false
		for _, remote := range j.h.proxy.snapshot.Routes {
			if remote.SessionID != route.SessionID {
				continue
			}
			remoteFound = true
			wantedAuth := ""
			if expected.account != "" {
				wantedAuth = j.accounts[expected.account].email + "-auth"
			}
			if remote.AuthID != wantedAuth || remote.Provider != expected.provider || remote.TicketHash != route.TicketHash {
				t.Fatal("helper session mapping differs from the user-selected account")
			}
		}
		if !remoteFound {
			t.Fatal("complete helper snapshot omitted a durable session")
		}
	}
	if j.h.guard.released != len(j.h.guard.acquired) {
		t.Fatal("completed action left native input paused")
	}
	native, err := j.h.svc.LaunchAccountEnv(j.h.ctx, "preexisting-native-session")
	if err != nil || native != nil {
		t.Fatal("journey adopted an older unmanaged session")
	}
}

func TestAccountJourneysKeepIndependentDefaultsAndStableSessionPins(t *testing.T) {
	for _, seed := range []int64{1, 7, 19, 37, 91, 271} {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			j := newAccountJourney(t)
			random := rand.New(rand.NewSource(seed))
			for step := 0; step < 100; step++ {
				provider := []string{"codex", "claude"}[random.Intn(2)]
				usable := j.ids(provider, true)
				signedOut := j.ids(provider, false)
				switch random.Intn(9) {
				case 0:
					j.spawn(t, provider, "")
				case 1:
					if len(usable) > 0 {
						j.spawn(t, provider, usable[random.Intn(len(usable))])
					}
				case 2:
					if len(usable) > 0 {
						id := usable[random.Intn(len(usable))]
						if err := j.h.svc.SetPrimary(j.h.ctx, id); err != nil {
							t.Fatal(err)
						}
						j.primaries[provider] = id
					}
				case 3:
					if len(usable) > 0 {
						j.remove(t, usable[random.Intn(len(usable))], random.Intn(2) == 0)
					}
				case 4:
					if len(signedOut) > 0 {
						a := j.accounts[signedOut[random.Intn(len(signedOut))]]
						j.add(t, provider, a.email, a.id)
					} else {
						j.add(t, provider, fmt.Sprintf("%s-%d@example.test", provider, step), "")
					}
				case 5:
					if len(usable) > 0 {
						ids := make([]string, 0, len(j.sessions))
						for session, expectation := range j.sessions {
							if expectation.provider == provider {
								ids = append(ids, string(session))
							}
						}
						sort.Strings(ids)
						if len(ids) > 0 {
							session := domain.SessionID(ids[random.Intn(len(ids))])
							target := usable[random.Intn(len(usable))]
							if err := j.h.svc.Switch(j.h.ctx, session, target); err != nil {
								t.Fatal(err)
							}
							expectation := j.sessions[session]
							expectation.account = target
							j.sessions[session] = expectation
						}
					}
				case 6:
					previous := j.h.svc.newID
					j.h.svc = New(j.h.store, j.h.proxy, j.h.guard, []byte(strings.Repeat("k", 32)), "http://127.0.0.1:1234", previous)
					if err := j.h.svc.Recover(j.h.ctx); err != nil {
						t.Fatal(err)
					}
				case 7:
					before, err := j.h.svc.State(j.h.ctx)
					if err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithCancel(j.h.ctx)
					cancel()
					if err := j.h.svc.SetPrimary(ctx, "any-account"); !errors.Is(err, context.Canceled) {
						t.Fatalf("cancelled preference error=%v", err)
					}
					after, err := j.h.svc.State(j.h.ctx)
					if err != nil || !reflect.DeepEqual(before, after) {
						t.Fatal("cancelled action changed account facts")
					}
				case 8:
					for session := range j.sessions {
						if err := j.h.svc.ForgetAccount(j.h.ctx, session); err != nil {
							t.Fatal(err)
						}
						delete(j.sessions, session)
						break
					}
				}
				j.check(t)
			}
		})
	}
}
