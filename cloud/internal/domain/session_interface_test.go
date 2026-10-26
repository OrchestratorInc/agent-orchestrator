package domain

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

func TestSessionInterfaceOppositeIsTwoWay(t *testing.T) {
	if got := SessionInterfaceTUI.Opposite(); got != SessionInterfaceChat {
		t.Fatalf("TUI opposite = %q, want chat", got)
	}
	if got := SessionInterfaceChat.Opposite(); got != SessionInterfaceTUI {
		t.Fatalf("Chat opposite = %q, want tui", got)
	}
}

// The precedence is the local daemon's, case for case.
func TestStartInterfaceMatchesTheSharedCases(t *testing.T) {
	raw, err := os.ReadFile("../../../contracts/session-start-interface.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Kind      string `json:"kind"`
			Requested string `json:"requested"`
			Default   string `json:"default"`
			Want      string `json:"want"`
		} `json:"cases"`
		WithoutDefault struct {
			Cloud []struct {
				Kind      string `json:"kind"`
				Requested string `json:"requested"`
				Want      string `json:"want"`
			} `json:"cloud"`
		} `json:"withoutDefault"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, tc := range fixture.Cases {
		// A harness that starts in Chat isolates the precedence.
		got := StartInterface(tc.Kind, SessionInterface(tc.Requested), SessionInterface(tc.Default), "claude-code")
		if string(got) != tc.Want {
			t.Errorf("StartInterface(%s, requested %q, default %q) = %q, want %q", tc.Kind, tc.Requested, tc.Default, got, tc.Want)
		}
	}
	// A create without a default (an older desktop) starts in the TUI unless
	// it asked for Chat, an orchestrator too.
	if len(fixture.WithoutDefault.Cloud) == 0 {
		t.Fatal("no cloud cases without a default")
	}
	for _, tc := range fixture.WithoutDefault.Cloud {
		got := StartInterface(tc.Kind, SessionInterface(tc.Requested), "", "claude-code")
		if string(got) != tc.Want {
			t.Errorf("StartInterface(%s, requested %q, no default) = %q, want %q", tc.Kind, tc.Requested, got, tc.Want)
		}
	}
}

func TestStartInterfaceGatesChatByHarness(t *testing.T) {
	for _, kind := range []string{"worker", "orchestrator"} {
		for harness, want := range map[string]SessionInterface{
			"claude-code": SessionInterfaceChat,
			"codex":       SessionInterfaceChat,
			// The one list of Chat harnesses (chatHarnesses, /me): Cursor
			// starts in Chat as it switches to it.
			"cursor":   SessionInterfaceChat,
			"opencode": SessionInterfaceTUI,
		} {
			if got := StartInterface(kind, "", SessionInterfaceChat, harness); got != want {
				t.Errorf("StartInterface(%s, %s) = %q, want %q", kind, harness, got, want)
			}
			if HarnessHasChat(harness) != (want == SessionInterfaceChat) {
				t.Errorf("HarnessHasChat(%s) disagrees with the start", harness)
			}
		}
	}
	if !slices.Equal(ChatHarnesses(), []string{"claude-code", "codex", "cursor"}) {
		t.Fatalf("ChatHarnesses() = %v", ChatHarnesses())
	}
}
