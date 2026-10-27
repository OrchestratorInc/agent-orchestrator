package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/aoagents/agent-orchestrator/cloud/internal/workerexec"
)

func TestStartInterface(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	unavailable := func() error { return workerexec.ErrChatAdapterUnavailable }
	cases := []struct {
		name      string
		launch    worker.LaunchContext
		installed bool
		startable func() error
		moved     bool
		moveErr   error
		restart   bool
		want      string
		wantAsked bool
	}{
		{name: "no interface is the terminal", launch: worker.LaunchContext{Harness: "claude-code"}, want: "tui"},
		{name: "terminal stays", launch: worker.LaunchContext{Harness: "claude-code", Interface: "tui"}, installed: true, startable: unavailable, want: "tui"},
		{name: "codex chat needs no adapter", launch: worker.LaunchContext{Harness: "codex", Interface: "chat"}, installed: true, startable: unavailable, want: "chat"},
		{name: "claude chat with the adapter", launch: worker.LaunchContext{Harness: "claude-code", Interface: "chat"}, installed: true, startable: func() error { return nil }, want: "chat"},
		{name: "claude not installed yet", launch: worker.LaunchContext{Harness: "claude-code", Interface: "chat"}, startable: unavailable, want: "chat"},
		{name: "no adapter and no npm moves to the terminal", launch: worker.LaunchContext{Harness: "claude-code", Interface: "chat"}, installed: true, startable: unavailable, moved: true, want: "tui", wantAsked: true},
		{name: "a session switched to chat stays", launch: worker.LaunchContext{Harness: "claude-code", Interface: "chat"}, installed: true, startable: unavailable, want: "chat", wantAsked: true},
		{name: "an older control plane keeps chat", launch: worker.LaunchContext{Harness: "claude-code", Interface: "chat"}, installed: true, startable: unavailable, moveErr: errors.New("404"), want: "chat", wantAsked: true},
		{name: "a restarted worker never moves the session", launch: worker.LaunchContext{Harness: "claude-code", Interface: "chat"}, installed: true, moved: true, restart: true, want: "chat"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asked := false
			got := startInterface(context.Background(), logger, tc.launch, chatStartChecks{
				firstStart:       !tc.restart,
				harnessInstalled: func(string) bool { return tc.installed },
				claudeChatStartable: func() error {
					if tc.startable == nil {
						t.Fatal("adapter checked")
					}
					return tc.startable()
				},
				fallBack: func(_ context.Context, code string) (bool, error) {
					asked = true
					if code != worker.ChatStartAdapterUnavailable {
						t.Errorf("fallback code = %q", code)
					}
					return tc.moved, tc.moveErr
				},
			})
			if got != tc.want || asked != tc.wantAsked {
				t.Fatalf("startInterface = %q (asked control plane %v), want %q (%v)", got, asked, tc.want, tc.wantAsked)
			}
		})
	}
}

func TestSettleClaudeChatStart(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	failedInstall := func(context.Context) error {
		return fmt.Errorf("%w: npm install: registry unreachable", workerexec.ErrChatAdapterUnavailable)
	}
	claudeChat := worker.LaunchContext{Harness: "claude-code", Interface: "chat"}
	cases := []struct {
		name       string
		launch     worker.LaunchContext
		ensure     func(context.Context) error
		moved      bool
		moveErr    error
		switchErr  error
		restart    bool
		want       bool
		wantAsked  bool
		wantSwitch bool
	}{
		{name: "codex chat", launch: worker.LaunchContext{Harness: "codex", Interface: "chat"}, ensure: failedInstall},
		{name: "claude terminal", launch: worker.LaunchContext{Harness: "claude-code", Interface: "tui"}, ensure: failedInstall},
		{name: "adapter installed", launch: claudeChat, ensure: func(context.Context) error { return nil }},
		{name: "still installing past the wait moves to the terminal", launch: claudeChat, ensure: func(context.Context) error { return workerexec.ErrChatAdapterInstalling }, moved: true, want: true, wantAsked: true, wantSwitch: true},
		{name: "install failed moves to the terminal", launch: claudeChat, ensure: failedInstall, moved: true, want: true, wantAsked: true, wantSwitch: true},
		{name: "a session switched to chat stays", launch: claudeChat, ensure: failedInstall, wantAsked: true},
		{name: "an older control plane keeps chat", launch: claudeChat, ensure: failedInstall, moveErr: errors.New("404"), wantAsked: true},
		{name: "the terminal could not start", launch: claudeChat, ensure: failedInstall, moved: true, switchErr: errors.New("pty"), wantAsked: true, wantSwitch: true},
		{name: "a restarted worker never moves the session", launch: claudeChat, ensure: failedInstall, moved: true, restart: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asked, switched := false, false
			got := settleClaudeChatStart(context.Background(), logger, tc.launch, chatAdapterSettle{
				firstStart: !tc.restart,
				ensure:     tc.ensure,
				fallBack: func(_ context.Context, code string) (bool, error) {
					asked = true
					if code != worker.ChatStartAdapterUnavailable {
						t.Errorf("fallback code = %q", code)
					}
					return tc.moved, tc.moveErr
				},
				switchToTUI: func(context.Context) error {
					switched = true
					return tc.switchErr
				},
			})
			if got != tc.want || asked != tc.wantAsked || switched != tc.wantSwitch {
				t.Fatalf("settle = %v (asked %v, switched %v), want %v (%v, %v)", got, asked, switched, tc.want, tc.wantAsked, tc.wantSwitch)
			}
		})
	}
}

// The settle gives the adapter's install claudeChatAdapterWait once the
// workspace is ready: long enough for an install that only starts then (about
// 20 seconds on a template that installs Node itself), short enough that a
// broken install still moves the session to the terminal soon.
func TestSettleClaudeChatStartWaitsForAnInstall(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if claudeChatAdapterWait < time.Minute || claudeChatAdapterWait > 2*time.Minute {
		t.Fatalf("claudeChatAdapterWait = %v, want between one and two minutes", claudeChatAdapterWait)
	}
	var waited time.Duration
	settleClaudeChatStart(context.Background(), logger, worker.LaunchContext{Harness: "claude-code", Interface: "chat"}, chatAdapterSettle{
		firstStart: true,
		ensure: func(ctx context.Context) error {
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("the adapter wait has no deadline")
			}
			waited = time.Until(deadline)
			return nil
		},
		fallBack:    func(context.Context, string) (bool, error) { t.Fatal("fell back"); return false, nil },
		switchToTUI: func(context.Context) error { t.Fatal("switched"); return nil },
	})
	if waited <= 0 || waited > claudeChatAdapterWait {
		t.Fatalf("waited up to %v, want at most %v", waited, claudeChatAdapterWait)
	}
}

type claimedChatRunner bool

func (claimedChatRunner) Run(context.Context) error { return nil }
func (r claimedChatRunner) ClaimedTurn() bool       { return bool(r) }

// A terminal agent started after this worker's Chat controller claimed a
// turn (a switch from Chat) does not get the session's task again; before
// that (a Chat start falling back to the terminal) it does, exactly once.
func TestWithoutPromptChatRan(t *testing.T) {
	launch := worker.LaunchContext{Harness: "claude-code", Prompt: "do the work"}
	if got := withoutPromptChatRan(launch, claimedChatRunner(false)).Prompt; got != "do the work" {
		t.Fatalf("before any Chat turn: prompt = %q", got)
	}
	if got := withoutPromptChatRan(launch, nil).Prompt; got != "do the work" {
		t.Fatalf("without a Chat controller: prompt = %q", got)
	}
	if got := withoutPromptChatRan(launch, claimedChatRunner(true)).Prompt; got != "" {
		t.Fatalf("after a Chat turn: prompt = %q, want none", got)
	}
}

// A session whose checkout or Setup failed opened its Chat controller with
// the startup error set (claiming nothing). After a successful retry the
// adapter wait and fallback run before the error clears, so the task waits for
// them as on a start that succeeded first time; that start opens the
// controller only after them.
func TestStartChatOnceWorkspaceReadySettlesBeforeTheControllerTakesTurns(t *testing.T) {
	for _, tc := range []struct {
		name       string
		chatOpened bool
		want       []string
	}{
		{"first start succeeded", false, []string{"settle", "open"}},
		{"retry after a failed start", true, []string{"settle", "clear"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			startChatOnceWorkspaceReady(tc.chatOpened,
				func() { calls = append(calls, "settle") },
				func() { calls = append(calls, "clear") },
				func() { calls = append(calls, "open") })
			if !reflect.DeepEqual(calls, tc.want) {
				t.Fatalf("calls = %v, want %v", calls, tc.want)
			}
		})
	}
}
