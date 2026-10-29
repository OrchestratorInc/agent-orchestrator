package telemetrymeta

import "testing"

func TestNormalizeCommandPath(t *testing.T) {
	if got := NormalizeCommandPath("  AO   Hooks  claude-code  post-tool-use "); got != "ao hooks claude-code post-tool-use" {
		t.Fatalf("NormalizeCommandPath = %q, want normalized lowercase fields", got)
	}
}

func TestIsRoutineInternalCLICommandNormalizesLegacyShapes(t *testing.T) {
	for _, commandPath := range []string{
		"ao hooks",
		"ao  hooks",
		"AO HOOKS",
		"ao hooks claude-code post-tool-use",
		"ao session get sess-123",
		"ao project ls",
		"ao pty-host session-1",
		"ao unreal-provider /tmp/config.json",
	} {
		if !IsRoutineInternalCLICommand(commandPath) {
			t.Errorf("IsRoutineInternalCLICommand(%q) = false, want true", commandPath)
		}
	}
}

func TestCLIActorTypeKeepsKnownLegacyUserCommands(t *testing.T) {
	for _, commandPath := range []string{
		"ao agent ls",
		"ao session claim-pr",
		"ao dev import-projects",
		"ao project orchestration get",
		"ao project orchestration set",
		"ao handoff",
		"ao smoke list",
		"ao smoke set",
	} {
		if got := CLIActorType("", commandPath); got != "user" {
			t.Errorf("CLIActorType(%q) = %q, want user", commandPath, got)
		}
	}
}

func TestCLIActorTypeTreatsInternalAgentHandoffAsSystemByDefault(t *testing.T) {
	for _, commandPath := range []string{
		"ao session handoff",
		"ao session handoff submit",
	} {
		if got := CLIActorType("", commandPath); got != "system" {
			t.Errorf("CLIActorType(%q) = %q, want system", commandPath, got)
		}
	}
}

func TestCLIActorTypeSystemCommandsOverrideExplicitActor(t *testing.T) {
	for _, tc := range []struct {
		actorType   string
		commandPath string
	}{
		{actorType: "user", commandPath: "ao daemon"},
		{actorType: "agent", commandPath: "ao start"},
		{actorType: "user", commandPath: "AO  AGENT-PROCESS  SUPERVISE"},
	} {
		if got := CLIActorType(tc.actorType, tc.commandPath); got != "system" {
			t.Errorf("CLIActorType(%q, %q) = %q, want system", tc.actorType, tc.commandPath, got)
		}
	}
}

func TestCLIActorTypeKeepsConservativeFallback(t *testing.T) {
	for _, tc := range []struct {
		actorType   string
		commandPath string
		want        string
	}{
		{actorType: "agent", commandPath: "ao surprise", want: "agent"},
		{actorType: "user", commandPath: "ao surprise", want: "user"},
		{actorType: "system", commandPath: "ao spawn", want: "system"},
		{commandPath: "ao daemon", want: "system"},
		{commandPath: "ao spawn", want: "user"},
		{commandPath: "ao surprise", want: "system"},
	} {
		if got := CLIActorType(tc.actorType, tc.commandPath); got != tc.want {
			t.Errorf("CLIActorType(%q, %q) = %q, want %q", tc.actorType, tc.commandPath, got, tc.want)
		}
	}
}

func TestCLIActorTypeClassifiesTestingCommands(t *testing.T) {
	for actor, verbs := range map[string][]string{
		"user":  {"", "start", "post", "prune", "settings", "runs", "show", "discard", "recipe", "recipe validate"},
		"agent": {"intake", "prepare", "launch", "browser", "screenshot", "record", "record start", "record stop", "submit", "status", "cleanup"},
	} {
		for _, verb := range verbs {
			path := "ao testing " + verb
			if got := CLIActorType("", path); got != actor {
				t.Errorf("%q classified %q, want %q", path, got, actor)
			}
		}
	}
	for _, actor := range []string{"user", "agent", "system"} {
		if got := CLIActorType(actor, "ao testing start"); got != actor {
			t.Errorf("actor %q became %q", actor, got)
		}
	}
	for _, path := range []string{"ao testing-other", "ao testing surprise", "ao testing record pause", "ao testing recipe unknown"} {
		if got := CLIActorType("", path); got != "system" {
			t.Errorf("unknown path %q classified %q", path, got)
		}
	}
}
