package codexappserver

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The menu is built from this list, so what it drops matters as much as what it
// keeps: a disabled skill would render as a command the provider then refuses to
// run, and a skill reachable from two roots would appear twice.
func TestListSkillsDropsDisabledAndDeduplicates(t *testing.T) {
	d, srv := newTestDriver(t)
	// Scripted before Start: the server goroutine reads this map, so writing it
	// afterwards would race the connection it is already serving. Kept on one line
	// because the transport is line-delimited JSON — a wrapped literal would never
	// parse as a frame and the request would simply never return.
	srv.reply("skills/list", `{"data":[{"cwd":"/tmp/ws","errors":[],"skills":[{"name":"review","description":"Long trigger text for the model","enabled":true,"scope":"repo","path":"/tmp/ws/.codex/skills/review/SKILL.md"},{"name":"switched-off","description":"never offered","enabled":false,"scope":"user","path":"/u/SKILL.md"},{"name":"","description":"nameless","enabled":true,"scope":"user","path":"/u/SKILL.md"}]},{"cwd":"/tmp/other","errors":[],"skills":[{"name":"review","description":"same skill from another root","enabled":true,"scope":"user","path":"/u/review/SKILL.md"},{"name":"ship","description":"legacy long text","shortDescription":"Ship it","enabled":true,"scope":"user","path":"/u/ship/SKILL.md"}]}]}`)

	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	lister, ok := conv.(ports.ChatSkillLister)
	if !ok {
		t.Fatal("conversation does not implement ChatSkillLister")
	}

	skills, err := lister.ListSkills(context.Background())
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("got %d skills, want review and ship only: %+v", len(skills), skills)
	}
	if skills[0].Name != "review" || skills[1].Name != "ship" {
		t.Errorf("provider order not preserved: %+v", skills)
	}
	if skills[0].Source != "repo" {
		t.Errorf("scope not carried through: %q, want repo", skills[0].Source)
	}
	// shortDescription is what fits a menu row; the full description is written for
	// the model and runs long.
	if skills[1].Description != "Ship it" {
		t.Errorf("shortDescription did not win: %q", skills[1].Description)
	}
}

// SKILL.json's interface block supersedes the top-level fields. Reading only the
// legacy fields would label a skill with text its author replaced.
func TestListSkillsPrefersTheInterfaceBlock(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.reply("skills/list", `{"data":[{"cwd":"/tmp/ws","errors":[],"skills":[{"name":"deploy","description":"legacy long text","shortDescription":"legacy short","enabled":true,"scope":"repo","path":"/tmp/ws/SKILL.md","interface":{"displayName":"Deploy to prod","shortDescription":"Ships the current branch"}}]}]}`)

	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	skills, err := conv.(ports.ChatSkillLister).ListSkills(context.Background())
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("got %d skills, want 1: %+v", len(skills), skills)
	}
	// The name stays the invocable one; only the label changes.
	if skills[0].Name != "deploy" {
		t.Errorf("name = %q, want the invocable name deploy", skills[0].Name)
	}
	if skills[0].DisplayName != "Deploy to prod" {
		t.Errorf("displayName = %q", skills[0].DisplayName)
	}
	if skills[0].Description != "Ships the current branch" {
		t.Errorf("description = %q", skills[0].Description)
	}
}

// A provider that reports nothing is a real answer, not a failure: the composer
// has to be able to tell "no skills" from "the call broke" so `/` can stay an
// ordinary character.
func TestListSkillsReportsAnEmptyListRatherThanNil(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.reply("skills/list", `{"data":[]}`)

	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	skills, err := conv.(ports.ChatSkillLister).ListSkills(context.Background())
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if skills == nil {
		t.Fatal("ListSkills returned nil; an empty catalog must encode as []")
	}
	if len(skills) != 0 {
		t.Errorf("got %d skills, want none: %+v", len(skills), skills)
	}
}
