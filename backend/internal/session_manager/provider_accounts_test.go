package sessionmanager

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	codexagent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type accountRoutingFake struct {
	route     domain.ProviderSessionRoute
	env       map[string]string
	managed   bool
	id        string
	err       error
	assigned  []domain.SessionID
	forgotten []domain.SessionID
	resolved  []string
	launches  []domain.SessionID
}

func (f *accountRoutingFake) ResolveAccount(_ context.Context, h domain.AgentHarness, id string) (string, bool, error) {
	f.resolved = append(f.resolved, string(h)+":"+id)
	return f.id, f.managed, f.err
}
func (f *accountRoutingFake) AssignAccount(_ context.Context, id domain.SessionID, _ domain.AgentHarness, _ string) error {
	f.assigned = append(f.assigned, id)
	return f.err
}
func (f *accountRoutingFake) SessionAccount(context.Context, domain.SessionID) (domain.ProviderSessionRoute, bool, error) {
	return f.route, f.managed, f.err
}
func (f *accountRoutingFake) LaunchAccountEnv(_ context.Context, id domain.SessionID) (map[string]string, error) {
	f.launches = append(f.launches, id)
	return f.env, f.err
}
func (f *accountRoutingFake) ForgetAccount(_ context.Context, id domain.SessionID) error {
	f.forgotten = append(f.forgotten, id)
	return f.err
}

type accountPauseLauncher struct {
	recordingLauncher
	paused, released []domain.SessionID
	pauseErr         error
}

func (l *accountPauseLauncher) AcquireAccountRoutingPause(_ context.Context, id domain.SessionID) (func(), error) {
	l.paused = append(l.paused, id)
	if l.pauseErr != nil {
		return nil, l.pauseErr
	}
	var once sync.Once
	return func() { once.Do(func() { l.released = append(l.released, id) }) }, nil
}
func TestProviderAccountLaunchEnvOnlyChangesManagedSessions(t *testing.T) {
	cases := []struct {
		name    string
		h       domain.AgentHarness
		p       string
		managed bool
		want    error
	}{
		{"managed-codex", domain.HarnessCodex, "codex", true, nil},
		{"managed-claude", domain.HarnessClaudeCode, "claude", true, nil},
		{"native-codex", domain.HarnessCodex, "", false, nil},
		{"native-claude", domain.HarnessClaudeCode, "", false, nil},
		{"provider-mismatch", domain.HarnessClaudeCode, "codex", true, ports.ErrProviderAccountIncompatible},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _, _, _ := newManager()
			f := &accountRoutingFake{managed: tc.managed, route: domain.ProviderSessionRoute{Provider: tc.p}, env: map[string]string{"AO_PROXY_TICKET": "private", "ANTHROPIC_API_KEY": ""}}
			m.SetProviderAccounts(f)
			env := map[string]string{"PATH": "/agent-bin", "ANTHROPIC_API_KEY": "ambient"}
			err := m.applyAccountEnv(context.Background(), domain.SessionRecord{ID: "s", Harness: tc.h}, env)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want=%v", err, tc.want)
			}
			if env["PATH"] != "/agent-bin" {
				t.Fatal("account route overwrote PATH")
			}
			if tc.managed && tc.want == nil {
				if env["AO_PROXY_TICKET"] != "private" || env["ANTHROPIC_API_KEY"] != "" || !reflect.DeepEqual(f.launches, []domain.SessionID{"s"}) {
					t.Fatalf("env=%v launches=%v", env, f.launches)
				}
			} else {
				if env["ANTHROPIC_API_KEY"] != "ambient" || len(f.launches) != 0 {
					t.Fatalf("native/mismatched env changed=%v launches=%v", env, f.launches)
				}
			}
		})
	}
}
func TestProviderAccountLaunchFailuresLeaveEnvironmentUntouched(t *testing.T) {
	m, _, _, _ := newManager()
	failure := errors.New("routing database unavailable")
	m.SetProviderAccounts(&accountRoutingFake{managed: true, err: failure})
	env := map[string]string{"PATH": "/agent-bin", "AO_PROXY_TICKET": "previous"}
	before := map[string]string{"PATH": "/agent-bin", "AO_PROXY_TICKET": "previous"}
	err := m.applyAccountEnv(context.Background(), domain.SessionRecord{ID: "s", Harness: domain.HarnessCodex}, env)
	if !errors.Is(err, failure) || !reflect.DeepEqual(env, before) {
		t.Fatalf("env=%v err=%v", env, err)
	}
}
func TestProviderAccountMutationRefusesActiveAndUnknownSessions(t *testing.T) {
	for _, state := range []domain.ActivityState{domain.ActivityActive, "unknown", ""} {
		t.Run(string(state), func(t *testing.T) {
			m, st, _, _ := newManager()
			st.sessions["s"] = domain.SessionRecord{ID: "s", Mode: domain.SessionModeChat, Activity: domain.Activity{State: state}}
			release, err := m.AcquireAccountMutation(context.Background(), []domain.SessionID{"s"})
			if release != nil || !errors.Is(err, ports.ErrProviderAccountBusy) {
				t.Fatalf("release=%v err=%v", release != nil, err)
			}
			done, allowed := m.AcquireSessionInput("s")
			if !allowed {
				t.Fatal("busy refusal left input fenced")
			}
			done()
		})
	}
}
func TestProviderAccountMutationFencesInputUntilIdempotentRelease(t *testing.T) {
	for _, state := range []domain.ActivityState{domain.ActivityIdle, domain.ActivityWaitingInput} {
		t.Run(string(state), func(t *testing.T) {
			l := &accountPauseLauncher{}
			m, st, _ := newChatManager(l)
			st.sessions["s"] = domain.SessionRecord{ID: "s", Mode: domain.SessionModeChat, Activity: domain.Activity{State: state}}
			release, err := m.AcquireAccountMutation(context.Background(), []domain.SessionID{"s"})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(l.paused, []domain.SessionID{"s"}) {
				t.Fatalf("chat not fenced=%v", l.paused)
			}
			if _, allowed := m.AcquireSessionInput("s"); allowed {
				t.Fatal("input admitted during account mutation")
			}
			if _, err = m.AcquireAccountMutation(context.Background(), []domain.SessionID{"s"}); !errors.Is(err, ports.ErrProviderAccountBusy) {
				t.Fatalf("second mutation=%v", err)
			}
			release()
			release()
			if !reflect.DeepEqual(l.released, []domain.SessionID{"s"}) {
				t.Fatalf("pause release=%v", l.released)
			}
			done, allowed := m.AcquireSessionInput("s")
			if !allowed {
				t.Fatal("input remains fenced")
			}
			done()
		})
	}
}
func TestProviderAccountMutationRollsBackEveryEarlierSessionOnBatchRefusal(t *testing.T) {
	l := &accountPauseLauncher{}
	m, st, _ := newChatManager(l)
	st.sessions["idle"] = domain.SessionRecord{ID: "idle", Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}
	st.sessions["busy"] = domain.SessionRecord{ID: "busy", Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityActive}}
	release, err := m.AcquireAccountMutation(context.Background(), []domain.SessionID{"idle", "busy"})
	if release != nil || !errors.Is(err, ports.ErrProviderAccountBusy) {
		t.Fatalf("release=%v err=%v", release != nil, err)
	}
	if !reflect.DeepEqual(l.released, []domain.SessionID{"idle"}) {
		t.Fatalf("earlier chat remains paused=%v", l.released)
	}
	for _, id := range []domain.SessionID{"idle", "busy"} {
		done, allowed := m.AcquireSessionInput(id)
		if !allowed {
			t.Fatalf("%s fenced after failed batch", id)
		}
		done()
	}
}
func TestProviderAccountMutationPropagatesControllerRefusalAndReleasesInput(t *testing.T) {
	l := &accountPauseLauncher{pauseErr: ports.ErrProviderAccountBusy}
	m, st, _ := newChatManager(l)
	st.sessions["s"] = domain.SessionRecord{ID: "s", Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}
	release, err := m.AcquireAccountMutation(context.Background(), []domain.SessionID{"s"})
	if release != nil || !errors.Is(err, ports.ErrProviderAccountBusy) {
		t.Fatalf("release=%v err=%v", release != nil, err)
	}
	done, allowed := m.AcquireSessionInput("s")
	if !allowed {
		t.Fatal("controller refusal left input fenced")
	}
	done()
	if len(l.released) != 0 {
		t.Fatal("released a pause never acquired")
	}
}
func TestProviderAccountMutationRequiresChatAdmissionBoundary(t *testing.T) {
	m, st, _ := newChatManager(&recordingLauncher{})
	st.sessions["s"] = domain.SessionRecord{ID: "s", Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}
	if _, err := m.AcquireAccountMutation(context.Background(), []domain.SessionID{"s"}); !errors.Is(err, ports.ErrProviderAccountBusy) {
		t.Fatalf("unguarded chat mutation=%v", err)
	}
}
func TestProviderAccountMutationAllowsExitedTerminatedAndRemovedSessions(t *testing.T) {
	m, st, _, _ := newManager()
	st.sessions["terminated"] = domain.SessionRecord{ID: "terminated", IsTerminated: true}
	st.sessions["exited"] = domain.SessionRecord{ID: "exited", Activity: domain.Activity{State: domain.ActivityExited}}
	ids := []domain.SessionID{"terminated", "exited", "absent"}
	release, err := m.AcquireAccountMutation(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if _, allowed := m.AcquireSessionInput(id); allowed {
			t.Fatalf("%s not fenced", id)
		}
	}
	release()
	for _, id := range ids {
		done, allowed := m.AcquireSessionInput(id)
		if !allowed {
			t.Fatalf("%s remains fenced", id)
		}
		done()
	}
}
func TestProviderAccountMutationStorageFailureLeavesNoFence(t *testing.T) {
	m, st, _, _ := newManager()
	st.getSessionErr = errors.New("sqlite busy")
	release, err := m.AcquireAccountMutation(context.Background(), []domain.SessionID{"s"})
	if release != nil || !errors.Is(err, st.getSessionErr) {
		t.Fatalf("release=%v err=%v", release != nil, err)
	}
	done, allowed := m.AcquireSessionInput("s")
	if !allowed {
		t.Fatal("database failure left input fenced")
	}
	done()
}
func TestProviderRelatedWorkUsesOwnerOnlyWhenProviderMatches(t *testing.T) {
	cases := []struct {
		name      string
		h         domain.AgentHarness
		p         string
		managed   bool
		account   string
		wantCalls int
		want      error
	}{
		{"codex-owner", domain.HarnessCodex, "codex", true, "a", 1, nil},
		{"claude-owner", domain.HarnessClaudeCode, "claude", true, "c", 1, nil},
		{"different-provider", domain.HarnessClaudeCode, "codex", true, "a", 0, nil},
		{"unrelated-harness", domain.HarnessCodex, "claude", true, "c", 0, nil},
		{"native-owner", domain.HarnessCodex, "", false, "", 0, nil},
		{"waiting-owner", domain.HarnessCodex, "codex", true, "", 0, ports.ErrProviderLoginRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _, _, _ := newManager()
			f := &accountRoutingFake{managed: tc.managed, route: domain.ProviderSessionRoute{Provider: tc.p, AccountID: tc.account}, env: map[string]string{"ticket": "owner"}}
			m.SetProviderAccounts(f)
			env, err := m.RelatedAccountEnv(context.Background(), "worker", tc.h)
			if !errors.Is(err, tc.want) || len(f.launches) != tc.wantCalls {
				t.Fatalf("env=%v calls=%v err=%v", env, f.launches, err)
			}
			if tc.wantCalls == 1 && (env["ticket"] != "owner" || f.launches[0] != "worker") {
				t.Fatalf("reviewer did not inherit owner=%v %v", env, f.launches)
			}
		})
	}
}

func TestProviderAccountSpawnRoutesPrimaryAndExplicitSelections(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		for _, choice := range []string{"", "explicit-account"} {
			t.Run(provider+choice, func(t *testing.T) {
				m, st, rt, _ := newManager()
				harness := domain.HarnessCodex
				if provider == "claude" {
					harness = domain.HarnessClaudeCode
				}
				env := map[string]string{"AO_PROXY_ENDPOINT": "http://127.0.0.1:4567", "AO_PROXY_TICKET": "session-ticket"}
				if provider == "claude" {
					env = map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:4567", "ANTHROPIC_AUTH_TOKEN": "session-ticket", "ANTHROPIC_API_KEY": ""}
				}
				f := &accountRoutingFake{managed: true, id: "resolved-account", route: domain.ProviderSessionRoute{Provider: provider, AccountID: "resolved-account"}, env: env}
				m.SetProviderAccounts(f)
				rec, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: harness, RequestedMode: domain.SessionModeTUI, ProviderAccountID: choice})
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(f.resolved, []string{string(harness) + ":" + choice}) || !reflect.DeepEqual(f.assigned, []domain.SessionID{rec.ID}) || !reflect.DeepEqual(f.launches, []domain.SessionID{rec.ID}) {
					t.Fatalf("resolve=%v assign=%v launch=%v", f.resolved, f.assigned, f.launches)
				}
				if _, exists := st.sessions[rec.ID]; !exists {
					t.Fatal("routed session was not persisted")
				}
				if rt.created == 0 {
					t.Fatal("routed session never launched")
				}
			})
		}
	}
}
func TestProviderAccountSpawnLoginFailureCreatesNoDurableSession(t *testing.T) {
	m, st, rt, _ := newManager()
	m.SetProviderAccounts(&accountRoutingFake{managed: true, err: ports.ErrProviderLoginRequired})
	_, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex, ProviderAccountID: "signed-out"})
	if !errors.Is(err, ports.ErrProviderLoginRequired) {
		t.Fatalf("spawn error=%v", err)
	}
	if len(st.sessions) != 0 || rt.created != 0 {
		t.Fatalf("failed auth created session=%d runtimes=%d", len(st.sessions), rt.created)
	}
}

func TestProviderAccountMutationWithCodexWarningFooter(t *testing.T) {
	for _, tc := range []struct {
		name, prompt, work string
		busy               bool
	}{
		{"idle", "\x1b[1m›\x1b[m \x1b[2mAsk Codex to do anything\x1b[m", "• Completed", false},
		// Unsent text is not a request in flight. The idle proof shared with
		// interface switches no longer stops for it (#6366), and an account change
		// leaves the terminal and its composer untouched, so it is admitted.
		{"unsent draft", "\x1b[1m›\x1b[m Keep this draft", "• Completed", false},
		{"active screen despite idle row", "\x1b[1m›\x1b[m", "• Working (4s • esc to interrupt)", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, st, runtime, _, events := newTransitionManager(t, domain.SessionModeTUI)
			useFastInterfaceTransitionTimings(m)
			m.agents = singleAgent{agent: &codexagent.Plugin{}}
			rec := st.sessions["session-1"]
			rec.Harness = domain.HarnessCodex
			st.sessions[rec.ID] = rec
			runtime.aliveByHandle = map[string]bool{"runtime-1": true}
			output := tc.work + "\n\n" + tc.prompt + "\n\n  GPT-5.5 low · ~/project · Task title\n  ? for shortcuts     ⚠ 3 warnings · f2 to view"
			runtime.outputForCall = func(int) string { return output }
			gate := &transitionInputGate{acquired: make(chan string, 1), released: make(chan string, 1)}
			m.SetTerminalInputGate(gate)
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			release, err := m.AcquireAccountMutation(ctx, []domain.SessionID{rec.ID})
			if tc.busy {
				if release != nil || !errors.Is(err, ports.ErrProviderAccountBusy) {
					t.Fatalf("unsafe account change admitted: %v", err)
				}
			} else {
				if err != nil || release == nil {
					t.Fatalf("idle warnings blocked account change: %v", err)
				}
				if _, allowed := m.AcquireSessionInput(rec.ID); allowed {
					t.Fatal("input bypassed account fence")
				}
				select {
				case <-gate.released:
					t.Fatal("terminal intake released before routing change")
				default:
				}
				release()
				release()
			}
			select {
			case id := <-gate.acquired:
				if id != "runtime-1" {
					t.Fatal(id)
				}
			default:
				t.Fatal("terminal input not fenced")
			}
			select {
			case <-gate.released:
			default:
				t.Fatal("terminal input fence leaked")
			}
			select {
			case <-gate.released:
				t.Fatal("terminal input released twice")
			default:
			}
			done, allowed := m.AcquireSessionInput(rec.ID)
			if !allowed {
				t.Fatal("session input remains fenced")
			}
			done()
			if runtime.styledOutputCalls < 3 || len(*events) != 0 || !reflect.DeepEqual(rec, st.sessions[rec.ID]) {
				t.Fatal("account admission lacked repeated proof or changed session lifecycle/history")
			}
		})
	}
}
