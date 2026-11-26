package systeminstall

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A daemon whose own Node is too old for a harness says nothing about the
// Node that owns the installed copy. An update validates the runtime that
// will run it; a fresh install still needs the daemon's.
func TestNPMUpdateValidatesOwningNodeNotDaemonNode(t *testing.T) {
	const pkg = "@anthropic-ai/claude-code"
	type layout struct {
		ownNode     bool
		nodeVersion string
	}
	setup := func(t *testing.T, l layout) (*Service, *[][]string, string) {
		t.Helper()
		var prefix, bin string
		if l.ownNode {
			prefix = filepath.Join(t.TempDir(), ".nvm", "versions", "node", "v24.0.0")
		} else {
			prefix = filepath.Join(t.TempDir(), ".local")
		}
		entry := writeNPMPackage(t, filepath.Join(prefix, "lib", "node_modules"), pkg)
		bin = filepath.Join(prefix, "bin", "claude")
		linkBinary(t, bin, entry)
		if l.ownNode {
			for _, file := range []string{filepath.Join(prefix, "bin", "node"), filepath.Join(prefix, "lib", "node_modules", "npm", "bin", "npm-cli.js")} {
				if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, nil, 0o755); err != nil {
					t.Fatal(err)
				}
			}
		}
		resolvedPrefix, _ := filepath.EvalSymlinks(prefix)
		s := newTestService("darwin", "npm")
		// The daemon's Node is 20; Claude Code's npm recipe needs 22.
		s.installCapabilities = installCapabilitiesStub{prefix: "/Users/test/.npm", writable: true, nodeVersion: "v20.0.0"}
		s.pathWritable = pathWritableProbeFunc(func(_ context.Context, path string) (bool, error) {
			return path == resolvedPrefix || path == "/Users/test/.npm", nil
		})
		s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
			return VerifyResult{ResolvedPath: bin, Output: "2.0.0 (Claude Code)"}, nil
		})
		s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return true, nil }
		ran := &[][]string{}
		s.commands = commandRunnerFunc(func(_ context.Context, argv []string, stdout, _ io.Writer) error {
			*ran = append(*ran, argv)
			switch {
			case len(argv) == 2 && argv[1] == "--version":
				_, err := io.WriteString(stdout, l.nodeVersion+"\n")
				return err
			case len(argv) == 3 && argv[2] == "--version":
				_, err := io.WriteString(stdout, "10.0.0\n")
				return err
			}
			return nil
		})
		return s, ran, resolvedPrefix
	}

	t.Run("owning Node 24 updates despite daemon Node 20", func(t *testing.T) {
		s, ran, prefix := setup(t, layout{ownNode: true, nodeVersion: "v24.0.0"})
		job, err := s.StartAgentOperation(context.Background(), TargetClaudeCode, "npm", AgentOperationUpdate, "")
		if err != nil {
			t.Fatalf("update rejected for the daemon's Node: %v", err)
		}
		if job.Status == StatusUnsupported {
			t.Fatalf("job = %+v", job)
		}
		s.workers.Wait()
		install := (*ran)[len(*ran)-1]
		node, _ := filepath.EvalSymlinks(filepath.Join(prefix, "bin", "node"))
		if install[0] != node || !slices.Contains(install, "install") || !slices.Contains(install, prefix) {
			t.Fatalf("install argv = %v, want the prefix's own Node installing into %s", install, prefix)
		}
	})
	t.Run("prefix without its own Node runs on the daemon's", func(t *testing.T) {
		s, _, _ := setup(t, layout{ownNode: false})
		job, err := s.StartAgentOperation(context.Background(), TargetClaudeCode, "npm", AgentOperationUpdate, "")
		if err != nil {
			t.Fatal(err)
		}
		if job.Status != StatusUnsupported || !strings.Contains(job.Error, "Node.js 22+ is required") {
			t.Fatalf("job = %+v, want the daemon Node rejection", job)
		}
	})
	t.Run("owning Node too old", func(t *testing.T) {
		s, _, _ := setup(t, layout{ownNode: true, nodeVersion: "v20.1.0"})
		job, err := s.StartAgentOperation(context.Background(), TargetClaudeCode, "npm", AgentOperationUpdate, "")
		if err != nil {
			t.Fatal(err)
		}
		if job.Status != StatusUnsupported || !strings.Contains(job.Error, "with the Node in") {
			t.Fatalf("job = %+v, want the owning Node rejection", job)
		}
	})
	t.Run("fresh install still needs the daemon's Node", func(t *testing.T) {
		s, _, _ := setup(t, layout{ownNode: true, nodeVersion: "v24.0.0"})
		if _, err := s.StartAgentOperation(context.Background(), TargetClaudeCode, "npm", AgentOperationInstall, ""); !errors.Is(err, ErrInstallMethod) {
			t.Fatalf("install error = %v, want ErrInstallMethod", err)
		}
	})
}
