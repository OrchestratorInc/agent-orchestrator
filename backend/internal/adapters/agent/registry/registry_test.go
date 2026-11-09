package registry

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestGeminiAdapterIsSelectable(t *testing.T) {
	reg, err := Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("gemini"); !ok {
		t.Fatal("Gemini CLI is not registered")
	}
	if !domain.AgentHarness("gemini").IsKnown() {
		t.Fatal("Gemini is not selectable")
	}
}

func TestDeepSeekHarnessAdapterIsSelectable(t *testing.T) {
	reg, err := Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("deepseek-harness"); !ok {
		t.Fatal("DeepSeek Harness is not registered")
	}
	if !domain.AgentHarness("deepseek-harness").IsKnown() {
		t.Fatal("DeepSeek Harness is not selectable")
	}
}

func TestOpenCodeMajorsAreIndependentlySelectable(t *testing.T) {
	reg, err := Build()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"opencode", "opencode-v2"} {
		adapter, ok := reg.Get(id)
		if !ok {
			t.Fatalf("%s is not registered", id)
		}
		if _, ok := adapter.(ports.Agent); !ok {
			t.Fatalf("%s does not implement Agent", id)
		}
		if _, ok := adapter.(ports.AgentAuthChecker); !ok {
			t.Fatalf("%s does not report authentication", id)
		}
		if _, ok := adapter.(ports.AgentBinaryResolver); !ok {
			t.Fatalf("%s does not expose its major-aware binary resolver", id)
		}
	}
}

// TestGetAgentHooksFootprintIsGitignored enforces a contract every shipped
// (and future) adapter must hold: any file GetAgentHooks writes into a session
// worktree must be covered by an AO-managed self-ignoring .gitignore
// (hookutil.EnsureWorkspaceGitignore) in its own folder or, as git allows, in
// a parent folder inside the workspace, listing the file's path from there.
// Pi needs the parent form: it skips any extension a .gitignore in the
// extensions folder lists. Hook files are untracked, and
// `git worktree remove` (without --force) refuses on any untracked file — an
// uncovered hook file makes every one of that adapter's session workspaces
// permanently undeletable (kill/cleanup can never free them).
func TestGetAgentHooksFootprintIsGitignored(t *testing.T) {
	for _, ha := range Harnessed() {
		t.Run(string(ha.Harness), func(t *testing.T) {
			ws := t.TempDir()
			if ha.Harness == "autohand" {
				t.Setenv("AUTOHAND_CONFIG", filepath.Join(t.TempDir(), "config.json"))
			}
			cfg := ports.WorkspaceHookConfig{
				SessionID:     "proj-1",
				WorkspacePath: ws,
				DataDir:       t.TempDir(),
			}
			if ha.Harness == "kimi" {
				cfg.Env = map[string]string{"KIMI_CODE_HOME": filepath.Join(cfg.DataDir, "kimi")}
			}
			if ha.Harness == "hermes" {
				cfg.Env = map[string]string{"HERMES_HOME": t.TempDir()}
			}
			ensureAgentBinary(t, string(ha.Harness))
			if err := ha.Agent.GetAgentHooks(context.Background(), cfg); err != nil {
				t.Fatalf("GetAgentHooks: %v", err)
			}
			files := workspaceFiles(t, ws)
			for _, rel := range files {
				if why := gitignoreCoverage(ws, rel); why != "" {
					t.Errorf("hook file %q is not covered by an AO-managed .gitignore (%s); it will keep the session worktree permanently dirty", rel, why)
				}
			}
		})
	}
}

// gitignoreCoverage reports why rel (slash-separated, relative to ws) is not
// covered by an AO-managed .gitignore in its folder or a parent folder up to
// ws, or "" when one lists it.
func gitignoreCoverage(ws, rel string) string {
	var why []string
	dir := filepath.Dir(filepath.FromSlash(rel))
	for {
		gitignorePath := filepath.Join(ws, dir, ".gitignore")
		data, err := os.ReadFile(gitignorePath) //nolint:gosec // test-owned temp dir
		entry, _ := filepath.Rel(dir, filepath.FromSlash(rel))
		entry = "/" + filepath.ToSlash(entry)
		switch {
		case err != nil:
		case !strings.Contains(string(data), hookutil.GitignoreSentinel):
			why = append(why, filepath.ToSlash(filepath.Join(dir, ".gitignore"))+" is not AO-managed")
		case hasLine(string(data), entry):
			return ""
		default:
			why = append(why, filepath.ToSlash(filepath.Join(dir, ".gitignore"))+" does not list "+entry)
		}
		if dir == "." {
			break
		}
		dir = filepath.Dir(dir)
	}
	if len(why) == 0 {
		return "no .gitignore in its folder or above"
	}
	return strings.Join(why, "; ")
}

func TestEveryHarnessReportsAuthStatus(t *testing.T) {
	for _, ha := range Harnessed() {
		if _, ok := ha.Agent.(ports.AgentAuthChecker); !ok {
			t.Errorf("%s does not implement ports.AgentAuthChecker", ha.Harness)
		}
	}
}

func TestRegistryIncludesFX(t *testing.T) {
	reg, err := Build()
	if err != nil {
		t.Fatal(err)
	}
	adapter, ok := reg.Get("fx")
	if !ok {
		t.Fatal("registry does not contain fx")
	}
	if manifest := adapter.Manifest(); manifest.Name != "fx" {
		t.Fatalf("fx manifest name = %q, want fx", manifest.Name)
	}

	for _, item := range Harnessed() {
		if item.Harness == domain.HarnessFX {
			return
		}
	}
	t.Fatal("Harnessed does not contain fx")
}

func TestRegistryIncludesPrimeAgent(t *testing.T) {
	reg, err := Build()
	if err != nil {
		t.Fatal(err)
	}
	adapter, ok := reg.Get("prime-agent")
	if !ok {
		t.Fatal("registry does not contain prime-agent")
	}
	manifest := adapter.Manifest()
	if manifest.Name != "Prime Agent" {
		t.Fatalf("prime-agent manifest name = %q, want Prime Agent", manifest.Name)
	}

	for _, item := range Harnessed() {
		if item.Harness == "prime-agent" {
			return
		}
	}
	t.Fatal("Harnessed does not contain prime-agent")
}

func TestRegistryIncludesOMP(t *testing.T) {
	reg, err := Build()
	if err != nil {
		t.Fatal(err)
	}
	adapter, ok := reg.Get("omp")
	if !ok {
		t.Fatal("registry does not contain omp")
	}
	manifest := adapter.Manifest()
	if manifest.Name != "OMP" {
		t.Fatalf("omp manifest name = %q, want OMP", manifest.Name)
	}

	for _, item := range Harnessed() {
		if item.Harness == domain.HarnessOMP {
			return
		}
	}
	t.Fatal("Harnessed does not contain omp")
}

func TestRegistryIncludesCommandCode(t *testing.T) {
	reg, err := Build()
	if err != nil {
		t.Fatal(err)
	}
	adapter, ok := reg.Get("command-code")
	if !ok {
		t.Fatal("registry does not contain command-code")
	}
	manifest := adapter.Manifest()
	if manifest.Name != "Command Code" {
		t.Fatalf("command-code manifest name = %q, want Command Code", manifest.Name)
	}

	for _, item := range Harnessed() {
		if item.Harness == domain.HarnessCommandCode {
			return
		}
	}
	t.Fatal("Harnessed does not contain command-code")
}

func TestRegistryIncludesCodewhale(t *testing.T) {
	reg, err := Build()
	if err != nil {
		t.Fatal(err)
	}
	adapter, ok := reg.Get("codewhale")
	if !ok || adapter.Manifest().Name != "Codewhale" {
		t.Fatalf("codewhale adapter = %+v, ok=%v", adapter, ok)
	}
}

func TestRegistryIncludesMiMoCode(t *testing.T) {
	reg, err := Build()
	if err != nil {
		t.Fatal(err)
	}
	adapter, ok := reg.Get("mimo-code")
	if !ok {
		t.Fatal("registry does not contain mimo-code")
	}
	if got := adapter.Manifest().Name; got != "MiMo Code" {
		t.Fatalf("mimo-code manifest name = %q", got)
	}
}

func TestRegistryIncludesOpenHands(t *testing.T) {
	reg, err := Build()
	if err != nil {
		t.Fatal(err)
	}
	adapter, ok := reg.Get("openhands")
	if !ok {
		t.Fatal("registry does not contain openhands")
	}
	if name := adapter.Manifest().Name; name != "OpenHands" {
		t.Fatalf("openhands manifest name = %q, want OpenHands", name)
	}
	for _, item := range Harnessed() {
		if item.Harness == domain.HarnessOpenHands {
			return
		}
	}
	t.Fatal("Harnessed does not contain openhands")
}

func TestHarnessedExcludesFakeHarness(t *testing.T) {
	for _, ha := range Harnessed() {
		if ha.Harness == domain.HarnessFake {
			t.Fatal("fake harness must not be returned as a shipped selectable agent")
		}
	}
}

func TestEveryProductionHarnessReportsModelOrModeConfig(t *testing.T) {
	for _, ha := range Harnessed() {
		t.Run(string(ha.Harness), func(t *testing.T) {
			spec, err := ha.Agent.GetConfigSpec(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, field := range spec.Fields {
				if field.Key == "model" || field.Key == "mode" {
					return
				}
			}
			t.Fatalf("%s exposes neither model nor mode configuration: %#v", ha.Harness, spec.Fields)
		})
	}
}

// workspaceFiles returns every regular file under root, relative to root.
func workspaceFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk workspace: %v", err)
	}
	return files
}

func ensureAgentBinary(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	binPath := filepath.Join(dir, name)
	version := "0.80.6"
	if name == "omp" {
		version = "17.1.0"
	}
	if name == "codewhale" {
		version = "0.10.0"
	}
	script := "#!/usr/bin/env sh\nif [ \"${1:-}\" = \"--version\" ]; then\n  echo \"" + name + " " + version + "\"\nfi\nexit 0\n"
	if err := os.WriteFile(binPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake agent binary %q: %v", binPath, err)
	}

	old := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+old)
}

func hasLine(content, line string) bool {
	for _, l := range strings.Split(content, "\n") {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}

func TestMemcodeAdapterIsSelectable(t *testing.T) {
	reg, err := Build()
	if err != nil {
		t.Fatal(err)
	}
	adapter, ok := reg.Get("memcode")
	if !ok || !domain.HarnessMemcode.IsKnown() {
		t.Fatal("Memcode is not selectable")
	}
	if adapter.Manifest().Name != "memcode" {
		t.Fatal(adapter.Manifest())
	}
	if _, ok := adapter.(ports.Agent); !ok {
		t.Fatal("missing TUI agent")
	}
}
