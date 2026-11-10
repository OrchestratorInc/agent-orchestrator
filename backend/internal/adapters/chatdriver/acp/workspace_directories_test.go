package acp

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// TestACPWorkspaceChildrenWithoutAdditionalCapability 覆盖新建、恢复和分叉的真实 ACP 请求，子仓库不要求额外根目录能力。
func TestACPWorkspaceChildrenWithoutAdditionalCapability(t *testing.T) {
	root := t.TempDir()
	children := []string{filepath.Join(root, "snowy"), filepath.Join(root, "snowy-admin-web")}
	for _, child := range children {
		if err := os.Mkdir(child, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, operation := range []string{"start", "resume", "fork"} {
		t.Run(operation, func(t *testing.T) {
			agent := &fakeAgent{forkID: "child", capabilities: &acpsdk.AgentCapabilities{
				SessionCapabilities: acpsdk.SessionCapabilities{
					Resume: &acpsdk.SessionResumeCapabilities{}, Fork: &acpsdk.SessionForkCapabilities{},
				},
			}}
			driver := forkTestDriver(agent)
			cfg := ports.ChatStartConfig{WorkspacePath: root, AdditionalDirectories: append([]string{root}, children...)}
			var conversation ports.ChatConversation
			var err error
			switch operation {
			case "start":
				conversation, err = driver.Start(t.Context(), cfg)
			case "resume":
				conversation, err = driver.Resume(t.Context(), ports.ChatResumeConfig{
					ProviderConversationID: "owned:parent", WorkspacePath: root, AdditionalDirectories: cfg.AdditionalDirectories,
				})
			case "fork":
				conversation, err = driver.ForkIntoHost(t.Context(), "owned:parent", "", cfg)
			}
			if err != nil {
				t.Fatalf("%s workspace with child repositories: %v", operation, err)
			}
			defer func() { _ = conversation.Close() }()
			agent.mu.Lock()
			defer agent.mu.Unlock()
			cwd, additional := agent.newParams.Cwd, agent.newParams.AdditionalDirectories
			if operation == "resume" {
				cwd, additional = agent.resumeParams.Cwd, agent.resumeParams.AdditionalDirectories
			} else if operation == "fork" {
				cwd, additional = agent.forkParams.Cwd, agent.forkParams.AdditionalDirectories
			}
			if cwd != root || len(additional) != 0 {
				t.Fatalf("%s cwd=%q additional=%v, want root only", operation, cwd, additional)
			}
		})
	}
}

// TestACPAdditionalDirectoryBoundaries 保留外部路径、符号链接和绝对路径校验，避免省略必要的能力要求。
func TestACPAdditionalDirectoryBoundaries(t *testing.T) {
	base := t.TempDir()
	root, child, outside := filepath.Join(base, "workspace"), filepath.Join(base, "workspace", "child"), filepath.Join(base, "workspace-other")
	for _, dir := range []string{child, outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got, err := normalizeAdditionalDirectories(t.Context(), root, []string{child, outside, outside}, true)
	if err != nil || !reflect.DeepEqual(got, []string{outside}) {
		t.Errorf("external directory normalization = %v, %v", got, err)
	}
	link := filepath.Join(root, "external-link")
	if runtime.GOOS == "windows" {
		if output, err := aoprocess.CommandContext(t.Context(), "cmd", "/c", "mklink", "/J", link, outside).CombinedOutput(); err != nil {
			t.Fatalf("create junction: %s: %v", output, err)
		}
	} else if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{outside, link, filepath.Join(root, "missing")} {
		if _, err := normalizeAdditionalDirectories(t.Context(), root, []string{dir}, false); err == nil || !strings.Contains(err.Error(), "does not support additional") {
			t.Errorf("uncovered directory %q: %v", dir, err)
		}
	}
	if _, err := normalizeAdditionalDirectories(t.Context(), root, []string{"relative"}, true); err == nil || !strings.Contains(err.Error(), "must be absolute") {
		t.Errorf("relative directory validation = %v", err)
	}
}
