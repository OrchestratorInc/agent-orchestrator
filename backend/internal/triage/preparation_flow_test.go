package triage

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPreparationCollectsWhileChoosingAndStartsOnlyOnce(t *testing.T) {
	origin := Origin{SessionID: "affected", Surface: "session", AppVersion: "original"}
	collecting, finish := make(chan struct{}), make(chan struct{})
	started := make(chan Run, 1)
	var count atomic.Int32
	deps := configuredDeps()
	deps.Collect = func(ctx context.Context, selected Origin) ([]Evidence, error) {
		if !reflect.DeepEqual(selected, origin) {
			t.Error("collector lost origin")
		}
		count.Add(1)
		close(collecting)
		select {
		case <-finish:
			return []Evidence{{ID: "snapshot", Text: "collected before start"}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	conv := fakeConversation()
	deps.StartAgent = func(_ context.Context, run Run) (AgentTransport, error) {
		started <- run
		return AgentTransport{Conversation: conv}, nil
	}
	c := serveForTest(t, t.TempDir(), deps)
	c.send(Command{ID: "prepare", Type: "prepare", Origin: &origin})
	prepared := c.response("prepare")
	if prepared.Run == nil || !prepared.Run.Preparation {
		t.Fatalf("not prepared: %#v", prepared)
	}
	awaitSignal(t, collecting)
	c.send(Command{ID: "reopen", Type: "prepare", Origin: &origin})
	if again := c.response("reopen"); again.Run == nil || again.Run.ID != prepared.Run.ID {
		t.Fatal("chooser reopened a second collection")
	}
	cmd := startCommand("start", true)
	cmd.Start.Origin, cmd.Start.PreparationID = origin, prepared.Run.ID
	c.send(cmd)
	if response := c.response("start"); response.Run == nil || response.Run.Preparation {
		t.Fatalf("start failed: %#v", response)
	}
	cmd.ID = "again"
	c.send(cmd)
	if response := c.response("again"); response.Run == nil || response.Run.ID != prepared.Run.ID {
		t.Fatal("duplicate start replaced run")
	}
	select {
	case <-started:
		t.Fatal("agent started before collection completed")
	default:
	}
	close(finish)
	select {
	case run := <-started:
		if count.Load() != 1 || len(run.Evidence) != 1 || run.Evidence[0].ID != "snapshot" || !run.Request.PublishAuthorized {
			t.Fatalf("agent lost automatic context: %#v", run)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("agent did not start")
	}
}

func TestPreparationOriginAndCancellationBoundaries(t *testing.T) {
	collecting := make(chan struct{})
	var providers atomic.Int32
	deps := configuredDeps()
	deps.Collect = func(ctx context.Context, _ Origin) ([]Evidence, error) {
		close(collecting)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	deps.StartAgent = func(context.Context, Run) (AgentTransport, error) {
		providers.Add(1)
		return AgentTransport{}, errors.New("unexpected agent")
	}
	c := serveForTest(t, t.TempDir(), deps)
	origin := Origin{HostID: "local", SessionID: "affected", Surface: "session"}
	c.send(Command{ID: "prepare", Type: "prepare", Origin: &origin})
	run := *c.response("prepare").Run
	awaitSignal(t, collecting)
	for _, changed := range []Origin{{HostID: "remote", SessionID: "affected", Surface: "session"}, {HostID: "local", SessionID: "other", Surface: "session"}, {HostID: "local", SessionID: "affected", Surface: "help"}} {
		cmd := startCommand("wrong", false)
		cmd.Start.Origin, cmd.Start.PreparationID = changed, run.ID
		c.send(cmd)
		if response := c.response("wrong"); response.Type != "error" {
			t.Fatal("changed origin was accepted")
		}
	}
	c.send(Command{ID: "cancel", Type: "cancel", RunID: run.ID})
	c.responseAndState("cancel", "cancelled")
	if providers.Load() != 0 {
		t.Fatal("cancelled preparation launched agent")
	}
}

func TestPreparationPartialCollectionIsScrubbedBeforeAgent(t *testing.T) {
	deps := configuredDeps()
	deps.Collect = func(context.Context, Origin) ([]Evidence, error) {
		return []Evidence{{ID: "known", Text: "token=ghp_abcdefghijklmnopqrstuvwx"}}, errors.New("unavailable token=ghp_abcdefghijklmnopqrstuvwx")
	}
	observed := make(chan Run, 1)
	deps.StartAgent = func(_ context.Context, run Run) (AgentTransport, error) {
		observed <- run
		return AgentTransport{}, errors.New("fake startup")
	}
	c := serveForTest(t, t.TempDir(), deps)
	c.send(startCommand("start", false))
	c.response("start")
	select {
	case run := <-observed:
		if len(run.Evidence) != 2 {
			t.Fatal("partial evidence was dropped")
		}
		for _, item := range run.Evidence {
			if strings.Contains(item.Text, "ghp_abcdefghijklmnopqrstuvwx") {
				t.Fatal("secret reached agent")
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("partial report prevented agent start")
	}
}
