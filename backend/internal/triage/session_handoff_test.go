package triage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func handoffRequest(t *testing.T, c *hostClient, origin Origin) StartRequest {
	t.Helper()
	c.send(Command{ID: "prepare", Type: "prepare", Origin: &origin})
	response := c.response("prepare")
	if response.Run == nil {
		t.Fatalf("prepare failed: %#v", response)
	}
	request := *startCommand("", true).Start
	request.PreparationID, request.Origin = response.Run.ID, origin
	return request
}

func TestHandoffCollectsThenFreezesOrdinarySessionPayload(t *testing.T) {
	dir := t.TempDir()
	collecting, ready := make(chan struct{}), make(chan struct{})
	var providers atomic.Int32
	deps := configuredDeps()
	deps.Collect = func(ctx context.Context, _ Origin) ([]Evidence, error) {
		close(collecting)
		select {
		case <-ready:
			return []Evidence{{ID: "automatic", Text: "token=ghp_abcdefghijklmnopqrstuvwx"}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	deps.StartAgent = func(context.Context, Run) (AgentTransport, error) {
		providers.Add(1)
		return AgentTransport{}, errors.New("must use normal session")
	}
	c := serveForTest(t, dir, deps)
	request := handoffRequest(t, c, Origin{Surface: "help"})
	awaitSignal(t, collecting)
	c.send(Command{ID: "handoff", Type: "handoff", Start: &request})
	c.send(Command{ID: "responsive", Type: "catalog"})
	if response := c.response("responsive"); response.Type != "catalog" {
		t.Fatal("handoff blocked command input")
	}
	close(ready)
	response := c.response("handoff")
	if response.Run == nil || response.Launch == nil || response.Run.Execution != "session" || response.Run.Preparation || providers.Load() != 0 {
		t.Fatalf("invalid handoff: %#v", response)
	}
	launch := response.Launch
	if len(launch.Prompt) > 250 || len(launch.Attachments) != 2 || launch.ClientRequestID != "triage-"+request.PreparationID {
		t.Fatalf("bad normal launch: %#v", launch)
	}
	raw, err := base64.StdEncoding.DecodeString(launch.Attachments[0].Data)
	if err != nil || !json.Valid(raw) || !strings.Contains(string(raw), request.Observation) || !strings.Contains(string(raw), "automatic") || strings.Contains(string(raw), "ghp_abcdefghijklmnopqrstuvwx") {
		t.Fatalf("missing or unsanitized context: %s %v", raw, err)
	}
	playbook, _ := base64.StdEncoding.DecodeString(launch.Attachments[1].Data)
	for _, rule := range []string{"pull requests", "without another approval", "bug-report.md", "uncertain"} {
		if !strings.Contains(string(playbook), rule) {
			t.Fatalf("missing instruction %s", rule)
		}
	}
	c.send(Command{ID: "retry", Type: "handoff", Start: &request})
	if retry := c.response("retry"); !reflect.DeepEqual(retry.Launch, launch) {
		t.Fatal("retry rebuilt frozen payload")
	}
	changed := request
	changed.Observation = "a different problem"
	c.send(Command{ID: "changed", Type: "handoff", Start: &changed})
	if c.response("changed").Type != "error" {
		t.Fatal("frozen observation could change")
	}
	c.send(Command{ID: "intent", Type: "session-create-intent", RunID: request.PreparationID})
	if response := c.response("intent"); response.Run == nil || !response.Run.SessionLaunchStarted {
		t.Fatalf("POST intent missing: %#v", response)
	}
	c.send(Command{ID: "get-intent", Type: "get", RunID: request.PreparationID})
	if !c.response("get-intent").Run.SessionLaunchStarted {
		t.Fatal("get returned stale intent")
	}
	c.send(Command{ID: "link", Type: "session-linked", RunID: request.PreparationID, SessionID: "ordinary-session"})
	if response := c.response("link"); response.Run == nil || response.Run.SessionID != "ordinary-session" {
		t.Fatalf("session link failed: %#v", response)
	}
	c.send(Command{ID: "get-link", Type: "get", RunID: request.PreparationID})
	if c.response("get-link").Run.SessionID != "ordinary-session" {
		t.Fatal("get returned stale session")
	}
	c.send(Command{ID: "fallback", Type: "start", Start: &request})
	if response := c.response("fallback"); response.Type != "error" || providers.Load() != 0 {
		t.Fatal("native fallback after POST was allowed")
	}
	var recoveries atomic.Int32
	deps.RecoverConversation = func(context.Context, Run) error { recoveries.Add(1); return nil }
	c.stop()
	another := serveForTest(t, dir, deps)
	another.send(Command{ID: "restart", Type: "handoff", Start: &request})
	if retry := another.response("restart"); !reflect.DeepEqual(retry.Launch, launch) || recoveries.Load() != 0 {
		t.Fatal("restart changed ordinary session ownership")
	}
	another.send(Command{ID: "other-link", Type: "session-linked", RunID: request.PreparationID, SessionID: "another-session"})
	if another.response("other-link").Type != "error" {
		t.Fatal("ordinary session could be replaced")
	}
}

func TestHandoffCancellationRemainsResponsive(t *testing.T) {
	collecting := make(chan struct{})
	deps := configuredDeps()
	deps.Collect = func(ctx context.Context, _ Origin) ([]Evidence, error) {
		close(collecting)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	dir := t.TempDir()
	c := serveForTest(t, dir, deps)
	request := handoffRequest(t, c, Origin{})
	awaitSignal(t, collecting)
	c.send(Command{ID: "handoff", Type: "handoff", Start: &request})
	c.send(Command{ID: "cancel", Type: "cancel", RunID: request.PreparationID})
	var cancelled, failed bool
	c.wait(func(response Response) bool {
		if response.ID == "cancel" && response.Type != "error" {
			cancelled = true
		}
		if response.ID == "handoff" {
			failed = response.Type == "error"
			if !failed {
				t.Fatal("cancelled collection returned a session launch")
			}
		}
		return cancelled && failed
	})
	store, err := openRunStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.get(request.PreparationID)
	if err != nil || saved.Launch != nil {
		t.Fatal("cancelled collection staged a launch")
	}
}

func TestHandoffOnlyKnownUnsentLaunchMayFallback(t *testing.T) {
	for _, intent := range []bool{false, true} {
		t.Run(map[bool]string{false: "unsent", true: "uncertain"}[intent], func(t *testing.T) {
			dir := t.TempDir()
			deps := configuredDeps()
			c := serveForTest(t, dir, deps)
			request := handoffRequest(t, c, Origin{})
			c.send(Command{ID: "handoff", Type: "handoff", Start: &request})
			if c.response("handoff").Launch == nil {
				t.Fatal("handoff missing")
			}
			if intent {
				c.send(Command{ID: "intent", Type: "session-create-intent", RunID: request.PreparationID})
				c.response("intent")
			}
			var providers atomic.Int32
			conv := fakeConversation()
			deps.StartAgent = func(context.Context, Run) (AgentTransport, error) {
				providers.Add(1)
				return AgentTransport{Conversation: conv}, nil
			}
			c.stop()
			restarted := serveForTest(t, dir, deps)
			restarted.send(Command{ID: "fallback", Type: "start", Start: &request})
			response := restarted.response("fallback")
			if intent {
				if response.Type != "error" || providers.Load() != 0 {
					t.Fatal("uncertain POST allowed second agent")
				}
				return
			}
			if response.Run == nil || response.Run.Execution != "" {
				t.Fatalf("known unsent fallback failed: %#v", response)
			}
			restarted.state("running")
			if providers.Load() != 1 {
				t.Fatal("fallback did not start exactly one native agent")
			}
		})
	}
}

func TestKnownUnsentFallbackCannotReplaceAnotherNativeInvestigation(t *testing.T) {
	deps := configuredDeps()
	conv := fakeConversation()
	var providers atomic.Int32
	deps.StartAgent = func(context.Context, Run) (AgentTransport, error) {
		providers.Add(1)
		return AgentTransport{Conversation: conv}, nil
	}
	c := serveForTest(t, t.TempDir(), deps)
	request := handoffRequest(t, c, Origin{})
	c.send(Command{ID: "handoff", Type: "handoff", Start: &request})
	if c.response("handoff").Launch == nil {
		t.Fatal("handoff missing")
	}
	c.send(startCommand("other-native", true))
	_, running := c.responseAndState("other-native", "running")
	c.send(Command{ID: "old-fallback", Type: "start", Start: &request})
	if response := c.response("old-fallback"); response.Type != "error" || providers.Load() != 1 {
		t.Fatalf("old fallback replaced the live agent: %#v", response)
	}
	c.send(Command{ID: "stop-other", Type: "cancel", RunID: running.ID})
	if response := c.response("stop-other"); response.Type == "error" {
		t.Fatal("the other agent lost its owner")
	}
}

func TestAtomicHandoffRestartPreservesFrozenRequest(t *testing.T) {
	for _, savedLaunch := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-launch-write", true: "after-launch-write"}[savedLaunch], func(t *testing.T) {
			dir := t.TempDir()
			store, err := openRunStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			request := *startCommand("", true).Start
			request.PreparationID = "partial-run"
			run := Run{ID: request.PreparationID, Version: ProtocolVersion, CreatedAt: time.Now(), Request: request, Execution: "session", Preparation: true, State: "collecting", Messages: []Message{}}
			if err := store.save(run); err != nil {
				t.Fatal(err)
			}
			launch, err := buildSessionLaunch(context.Background(), run, dir)
			if err != nil {
				t.Fatal(err)
			}
			if savedLaunch {
				run.Launch, run.Preparation, run.State = &launch, false, "session_launch"
				if err := store.save(run); err != nil {
					t.Fatal(err)
				}
			}
			var collected atomic.Int32
			deps := configuredDeps()
			deps.Collect = func(context.Context, Origin) ([]Evidence, error) { collected.Add(1); return nil, nil }
			c := serveForTest(t, dir, deps)
			c.send(Command{ID: "resume", Type: "handoff", Start: &request})
			response := c.response("resume")
			if !savedLaunch {
				if response.Type != "error" || collected.Load() != 0 {
					t.Fatal("interrupted preparation without frozen payload must be restarted")
				}
				return
			}
			if response.Run == nil || response.Run.Launch != nil || response.Launch == nil || !reflect.DeepEqual(response.Run.Request, request) {
				t.Fatalf("partial handoff lost frozen request: %#v", response)
			}
			if savedLaunch && (!reflect.DeepEqual(*response.Launch, launch) || collected.Load() != 0) {
				t.Fatal("saved launch was rebuilt")
			}
		})
	}
}

func TestHandoffPermissionOverrideDoesNotSaveDefaults(t *testing.T) {
	deps := configuredDeps()
	catalog, _ := deps.Catalog(context.Background())
	catalog[0].Permissions = domain.PermissionModeDefault
	deps.Catalog = func(context.Context) ([]Choice, error) { return catalog, nil }
	c := serveForTest(t, t.TempDir(), deps)
	request := handoffRequest(t, c, Origin{})
	request.Permissions = domain.PermissionModeBypassPermissions
	c.send(Command{ID: "handoff", Type: "handoff", Start: &request})
	response := c.response("handoff")
	if response.Run == nil || response.Run.Request.Permissions != domain.PermissionModeBypassPermissions || catalog[0].Permissions != domain.PermissionModeDefault {
		t.Fatalf("explicit permission override changed defaults: %#v", response)
	}
}

func TestStartAndHandoffReserveOnlyOneExecution(t *testing.T) {
	deps := configuredDeps()
	catalog := deps.Catalog
	admissions := make(chan struct{}, 2)
	release := make(chan struct{})
	deps.Catalog = func(ctx context.Context) ([]Choice, error) {
		admissions <- struct{}{}
		select {
		case <-release:
			return catalog(ctx)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	started := make(chan struct{}, 1)
	conv := fakeConversation()
	deps.StartAgent = func(context.Context, Run) (AgentTransport, error) {
		started <- struct{}{}
		return AgentTransport{Conversation: conv}, nil
	}
	c := serveForTest(t, t.TempDir(), deps)
	request := handoffRequest(t, c, Origin{})
	c.send(Command{ID: "handoff", Type: "handoff", Start: &request})
	awaitSignal(t, admissions)
	c.send(Command{ID: "start", Type: "start", Start: &request})
	if response := c.response("start"); response.Type != "error" {
		t.Fatal("native start was admitted during an ordinary handoff")
	}
	close(release)
	if response := c.response("handoff"); response.Type != "session-launch" {
		t.Fatalf("ordinary handoff failed: %#v", response)
	}
	select {
	case <-started:
		t.Fatal("handoff winner also launched native provider")
	default:
	}
}

func TestExplicitlyCancelledHandoffCannotResume(t *testing.T) {
	for _, saved := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-staging", true: "after-staging"}[saved], func(t *testing.T) {
			dir := t.TempDir()
			store, err := openRunStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			request := *startCommand("", true).Start
			request.PreparationID = "cancelled-run"
			run := Run{ID: request.PreparationID, Request: request, Execution: "session", Preparation: true, State: "cancelled", Messages: []Message{}}
			if err := store.save(run); err != nil {
				t.Fatal(err)
			}
			if saved {
				launch, err := buildSessionLaunch(context.Background(), run, dir)
				if err != nil {
					t.Fatal(err)
				}
				run.Launch = &launch
				if err := store.save(run); err != nil {
					t.Fatal(err)
				}
			}
			c := serveForTest(t, dir, configuredDeps())
			for _, kind := range []string{"start", "handoff"} {
				c.send(Command{ID: kind, Type: kind, Start: &request})
				if response := c.response(kind); response.Type != "error" {
					t.Fatalf("cancelled report resumed: %#v", response)
				}
			}
		})
	}
}
