package main

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

func TestPullRequestReferenceUsesTheWorkingDirectoryRepository(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "--quiet"}, {"remote", "add", "origin", "https://github.com/acme/voice-calling-ai.git"}} {
		command := exec.Command("git", args...)
		command.Dir = dir
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, output)
		}
	}
	previous, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	ctx := context.Background()
	// A worker that does not list the session's repositories, or a checkout
	// that is not one of them (a fork), sends the number unchanged.
	t.Setenv(worker.SessionRepositoriesEnv, "")
	if got := pullRequestReference(ctx, "12"); got != "12" {
		t.Fatalf("without the session's repositories the number became %q", got)
	}
	t.Setenv(worker.SessionRepositoriesEnv, "acme/alice")
	if got := pullRequestReference(ctx, "12"); got != "12" {
		t.Fatalf("a checkout outside the session's repositories turned the number into %q", got)
	}
	t.Setenv(worker.SessionRepositoriesEnv, "acme/alice,acme/voice-calling-ai")
	if got := pullRequestReference(ctx, "12"); got != "https://github.com/acme/voice-calling-ai/pull/12" {
		t.Fatalf("number resolved to %q", got)
	}
	if got := pullRequestReference(ctx, "#12"); got != "https://github.com/acme/voice-calling-ai/pull/12" {
		t.Fatalf("#number resolved to %q", got)
	}
	url := "https://github.com/acme/alice/pull/3"
	if got := pullRequestReference(ctx, url); got != url {
		t.Fatalf("URL changed to %q", got)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if got := pullRequestReference(ctx, "12"); got != "12" {
		t.Fatalf("outside a checkout the number became %q", got)
	}
}

func TestGitHubFullName(t *testing.T) {
	for remote, want := range map[string]string{
		"https://github.com/acme/alice.git": "acme/alice",
		"git@github.com:acme/alice.git":     "acme/alice",
		"https://gitlab.com/acme/alice":     "",
		"https://github.com/acme":           "",
	} {
		got, ok := githubFullName(remote)
		if got != want || ok != (want != "") {
			t.Errorf("githubFullName(%q) = %q, %v", remote, got, ok)
		}
	}
}
