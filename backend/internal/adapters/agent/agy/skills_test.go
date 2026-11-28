package agy

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/skillbridge"
)

func TestTerminalCommandsAddTheBridgedSkillFolder(t *testing.T) {
	data, source := t.TempDir(), t.TempDir()
	skill := filepath.Join(source, "mine", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("---\nname: mine\ndescription: d\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := skillbridge.Sync(context.Background(), skillbridge.Config{
		Dir: skillbridge.SessionDir(data, "agy", "s-1"), Layout: skillbridge.AgentsLayout, Sources: []string{source},
	}); err != nil {
		t.Fatal(err)
	}
	dirs := SkillDirectories(data, "s-1")
	if len(dirs) != 1 {
		t.Fatalf("skill directories = %v", dirs)
	}
	plugin := &Plugin{resolvedBinary: "agy"}
	for session, want := range map[string][]string{
		"s-1": {"agy", "--add-dir", "/tmp/ws", "--add-dir", dirs[0]},
		"s-2": {"agy", "--add-dir", "/tmp/ws"},
	} {
		launch, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
			DataDir: data, SessionID: session, WorkspacePath: "/tmp/ws",
		})
		if err != nil || !slices.Equal(launch, want) {
			t.Errorf("launch %s = %v, %v, want %v", session, launch, err, want)
		}
		restore, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
			DataDir: data, Session: ports.SessionRef{
				ID: session, WorkspacePath: "/tmp/ws",
				Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "native"},
			},
		})
		if err != nil || !ok || !slices.Equal(restore, append(slices.Clone(want), "--conversation", "native")) {
			t.Errorf("restore %s = %v, %v", session, restore, err)
		}
	}
}

func TestNativeSkillRootsCoverWhereAgyLooks(t *testing.T) {
	gemini, workspace := t.TempDir(), t.TempDir()
	declared := filepath.Join(t.TempDir(), "team")
	for path, content := range map[string]string{
		filepath.Join(gemini, "config", "plugins", "p", "skills", "x", "SKILL.md"): "",
		filepath.Join(gemini, "config", "skills.json"):                             `{"entries":[{"path":"` + filepath.ToSlash(declared) + `"}]}`,
		filepath.Join(workspace, ".agents", "skills.json"):                         `{"entries":[{"path":"tools/skills"}]}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	roots := New().SkillSupport(context.Background(), ports.AgentSkillRequest{
		WorkspacePath: workspace, Env: map[string]string{"GEMINI_HOME": gemini},
	}).Native
	for i := range roots {
		roots[i] = filepath.Clean(roots[i])
	}
	for _, want := range []string{
		filepath.Join(gemini, "config", "skills"),
		filepath.Join(gemini, "skills"),
		filepath.Join(gemini, ACPStorageDir, "builtin", "skills"),
		filepath.Join(gemini, CLIStorageDir, "builtin", "skills"),
		filepath.Join(gemini, CLIStorageDir, "skills"),
		filepath.Join(gemini, "config", "plugins", "p", "skills"),
		filepath.Join(workspace, "tools", "skills"),
		filepath.Clean(declared),
	} {
		if !slices.Contains(roots, want) {
			t.Errorf("native roots %v lack %s", roots, want)
		}
	}
}

func TestNativeSkillRootsIncludeAgentsSkillsFromTheReleaseThatReadsThem(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the agy CLI")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GEMINI_HOME", "")
	t.Setenv(ACPServerPathEnvVar, "")
	agents := filepath.Join(home, ".agents", "skills")
	roots := func(version string, chat bool, env map[string]string) []string {
		binary := filepath.Join(t.TempDir(), "agy")
		if err := os.WriteFile(binary, []byte("#!/bin/sh\necho "+version+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return (&Plugin{resolvedBinary: binary}).SkillSupport(context.Background(), ports.AgentSkillRequest{Env: env, Chat: chat}).Native
	}
	for version, want := range map[string]bool{
		"1.5.15": false, "1.5.16": true, "1.5.17": true, "1.6.0": true, "1.4.99": false, "dev": false,
	} {
		if got := slices.Contains(roots(version, true, nil), agents); got != want {
			t.Errorf("agy %s reads ~/.agents/skills = %v, want %v", version, got, want)
		}
	}
	if slices.Contains(roots("1.5.16", true, map[string]string{"GEMINI_HOME": t.TempDir()}), agents) {
		t.Error("agy with another data folder was taken to read ~/.agents/skills")
	}
	t.Setenv(ACPServerPathEnvVar, "/opt/agy_acp_server")
	if slices.Contains(roots("1.5.16", true, nil), agents) {
		t.Error("the separate chat server was taken to read ~/.agents/skills")
	}
	if !slices.Contains(roots("1.5.16", false, nil), agents) {
		t.Error("a terminal session runs the agy CLI, which reads ~/.agents/skills")
	}
}
