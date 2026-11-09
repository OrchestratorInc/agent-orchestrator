package conductorimport

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func repo(t *testing.T, files map[string]string, tracked ...string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, append([]string{"add", "--"}, tracked...)} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return root
}

func TestReadMergesSettingsLayers(t *testing.T) {
	root := repo(t, map[string]string{
		".gitignore":          ".env\n*.local.json\n",
		".env":                "A=1",
		".env.draft":          "B=2",
		".env.example":        "A=",
		"config/a.local.json": "{}",
		".conductor/settings.toml": `"$schema" = "https://conductor.build/schemas/settings.repo.schema.json"
enterprise_data_privacy = true
file_include_globs = ".env*\nconfig/*.local.json\n"

[scripts]
setup = "pnpm install"
archive = "docker compose down"
run_mode = "concurrent"

[scripts.run.dev]
command = "pnpm dev --port $CONDUCTOR_PORT"
default = true

[scripts.run.remote]
command = "make cloud"
available_in = "cloud"

[scripts.run.test]
command = "pnpm test"
args = ["--watch", "it's"]
options = { cwd = "web" }

[environment_variables]
API_URL = "http://localhost"
AO_TOKEN = "x"
EXISTING = "new"

[environment_variables.cloud]
ONLY_CLOUD = "1"

[prompts]
general = "Keep changes small."
code_review = "Be strict."

[git]
branch_prefix = "me/"
`,
		".conductor/settings.local.toml": `[scripts]
setup = "bun install\ncp \"$CONDUCTOR_ROOT_PATH/.env\" . && echo $CONDUCTOR_DEFAULT_BRANCH"

[environment_variables.local]
API_URL = "http://127.0.0.1"
`,
	}, ".gitignore", ".env.example", ".conductor/settings.toml")

	current := domain.ProjectConfig{Env: map[string]string{"EXISTING": "old"}}
	plan, err := Read(context.Background(), root, current)
	if err != nil {
		t.Fatal(err)
	}
	setup := "bun install\ncp \"$CONDUCTOR_ROOT_PATH/.env\" . && echo $CONDUCTOR_DEFAULT_BRANCH"
	if plan.Source != ".conductor/settings.toml" {
		t.Fatalf("source = %q", plan.Source)
	}
	want := domain.ProjectConfig{
		Env:        map[string]string{"EXISTING": "old", "API_URL": "http://127.0.0.1"},
		PostCreate: []string{copyStep(".env", "config/a.local.json"), setup},
		PreRemove:  []string{"docker compose down"},
		AgentRules: "Keep changes small.",
	}
	if !reflect.DeepEqual(plan.Config, want) {
		t.Fatalf("config = %+v\nwant %+v", plan.Config, want)
	}
	wantCues := []CueDraft{{Key: "scripts.run.dev", Name: "dev", Command: "pnpm dev --port $CONDUCTOR_PORT"}, {Key: "scripts.run.test", Name: "test", Command: `cd -- 'web' && pnpm test '--watch' 'it'\''s'`}}
	if !reflect.DeepEqual(plan.Cues, wantCues) {
		t.Fatalf("cues = %+v", plan.Cues)
	}
	wantWarnings := []Item{{Key: "scripts.setup", Detail: "CONDUCTOR_DEFAULT_BRANCH"}}
	if !reflect.DeepEqual(plan.Warnings, wantWarnings) {
		t.Fatalf("warnings = %+v", plan.Warnings)
	}
	wantSkipped := []Item{
		{Key: "enterprise_data_privacy"},
		{Key: "environment_variables.AO_TOKEN", Detail: "invalid"},
		{Key: "environment_variables.EXISTING", Detail: "already set"},
		{Key: "environment_variables.cloud"},
		{Key: "git.branch_prefix"},
		{Key: "prompts.code_review"},
		{Key: "scripts.run.remote", Detail: "cloud"},
		{Key: "scripts.run_mode"},
	}
	if !reflect.DeepEqual(plan.Skipped, wantSkipped) {
		t.Fatalf("skipped = %+v", plan.Skipped)
	}
	if len(plan.Added) != 5 {
		t.Fatalf("added = %+v", plan.Added)
	}

	again, err := Read(context.Background(), root, plan.Config)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Added) != 0 || !reflect.DeepEqual(again.Config, plan.Config) {
		t.Fatalf("a second import changed the config: %+v", again.Added)
	}
}

func TestReadLegacyConductorJSON(t *testing.T) {
	root := repo(t, map[string]string{
		".gitignore":     ".env.local\n",
		".env.local":     "A=1",
		"conductor.json": `{"scripts":{"setup":"npm ci","run":"npm run dev","archive":"rm -rf tmp"},"runScriptMode":"concurrent"}`,
	}, ".gitignore", "conductor.json")
	plan, err := Read(context.Background(), root, domain.ProjectConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Source != "conductor.json" || !reflect.DeepEqual(plan.Config.PostCreate, []string{copyStep(".env.local"), "npm ci"}) || !reflect.DeepEqual(plan.Config.PreRemove, []string{"rm -rf tmp"}) {
		t.Fatalf("plan = %+v", plan)
	}
	if !reflect.DeepEqual(plan.Cues, []CueDraft{{Key: "scripts.run", Name: "Run", Command: "npm run dev"}}) || plan.Config.Symlinks != nil {
		t.Fatalf("plan = %+v", plan)
	}
	if !reflect.DeepEqual(plan.Skipped, []Item{{Key: "runScriptMode"}}) {
		t.Fatalf("skipped = %+v", plan.Skipped)
	}
}

func TestReadWithoutConductorSettings(t *testing.T) {
	root := repo(t, map[string]string{".gitignore": ".env\n", ".env": "A=1"}, ".gitignore")
	current := domain.ProjectConfig{PostCreate: []string{"make"}}
	plan, err := Read(context.Background(), root, current)
	if err != nil || plan.Source != "" || !reflect.DeepEqual(plan.Config, current) || len(plan.Added) != 0 {
		t.Fatalf("plan = %+v, err = %v", plan, err)
	}
}

func TestEnvGivesEachWorkspaceAStablePortBlock(t *testing.T) {
	seen := map[string]bool{}
	for i := range 200 {
		workspace := fmt.Sprintf("/work/repo-%d", i)
		env := Env("/work/repo", workspace)
		base, err := strconv.Atoi(env["CONDUCTOR_PORT"])
		if err != nil || base < 10000 || base+9 >= 32768 || base%10 != 0 || env["CONDUCTOR_PORT"] != Env("/other", workspace)["CONDUCTOR_PORT"] {
			t.Fatalf("port for %s = %q", workspace, env["CONDUCTOR_PORT"])
		}
		seen[env["CONDUCTOR_PORT"]] = true
	}
	if len(seen) < 180 {
		t.Fatalf("200 workspaces share %d port blocks", len(seen))
	}
}

func copyStep(files ...string) string {
	quoted := make([]string, len(files))
	for i, file := range files {
		quoted[i] = shellQuote(file)
	}
	return `for f in ` + strings.Join(quoted, " ") + `; do if [ -e "$AO_SOURCE_TREE_PATH/$f" ]; then mkdir -p "$(dirname "$f")" && cp -pR "$AO_SOURCE_TREE_PATH/$f" "$f" || exit; fi; done`
}

func TestCopyStepCopiesIgnoredFilesIntoTheWorkspace(t *testing.T) {
	root := repo(t, map[string]string{
		".gitignore":               ".env\n*.local.json\n",
		".env":                     "A=1",
		".worktreeinclude":         ".env\n*.local.json\n",
		"config/it's a.local.json": "{}",
		".conductor/settings.toml": "[scripts]\nsetup = \"true\"\n",
	}, ".gitignore", ".conductor/settings.toml")
	plan, err := Read(context.Background(), root, domain.ProjectConfig{Symlinks: []string{"config/linked.local.json"}})
	if err != nil || len(plan.Config.PostCreate) != 2 {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	workspace := t.TempDir()
	for range 2 {
		run := exec.Command("sh", "-c", plan.Config.PostCreate[0])
		run.Dir, run.Env = workspace, append(os.Environ(), "AO_SOURCE_TREE_PATH="+root)
		if out, err := run.CombinedOutput(); err != nil {
			t.Fatalf("copy step: %v: %s", err, out)
		}
	}
	for _, name := range []string{".env", "config/it's a.local.json"} {
		info, err := os.Lstat(filepath.Join(workspace, name))
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("%s in workspace: %v, %v", name, info, err)
		}
	}
}
