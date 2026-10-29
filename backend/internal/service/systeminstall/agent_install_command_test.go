package systeminstall

import (
	"context"
	"strings"
	"testing"
)

// Pre-flight shows this command for the user to paste, so it must be a
// runnable line on every OS and never a downloaded-script placeholder.
func TestAgentInstallCommandPerOS(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			s := newTestService(goos, "npm", "bash", "powershell.exe")
			s.installCapabilities = installCapabilitiesStub{prefix: "/Users/test/.npm", writable: true}
			command, docsURL, err := s.AgentInstallCommand(context.Background(), TargetCodex)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(command, "npm install") || !strings.Contains(command, "@openai/codex") {
				t.Fatalf("command = %q, want the npm install line", command)
			}
			if strings.Contains(command, "<downloaded from") {
				t.Fatalf("command = %q is a script placeholder", command)
			}
			if docsURL == "" {
				t.Fatal("docs URL is empty")
			}
		})
	}
}

func TestAgentInstallCommandWithoutRunnableMethodKeepsGuide(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			s := newTestService(goos, "bash", "powershell.exe")
			command, docsURL, err := s.AgentInstallCommand(context.Background(), TargetGoose)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(command, "<downloaded from") {
				t.Fatalf("command = %q is a script placeholder", command)
			}
			if docsURL == "" {
				t.Fatal("docs URL is empty")
			}
		})
	}
	if _, _, err := newTestService("linux").AgentInstallCommand(context.Background(), TargetTmux); err == nil {
		t.Fatal("tmux is not an agent target")
	}
}
