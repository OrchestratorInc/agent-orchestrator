package conductorimport

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func repo(t *testing.T, files map[string]string, tracked ...string) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
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
		"conductor.json":      `{"scripts":{"setup":"npm ci"},"runScriptMode":"concurrent"}`,
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
setup = "bun install\ncp \"$CONDUCTOR_ROOT_PATH/.env\" . && echo $CONDUCTOR_BASE_DIR"

[environment_variables.local]
API_URL = "http://127.0.0.1"
`,
	}, ".gitignore", ".env.example", ".conductor/settings.toml")

	current := domain.ProjectConfig{Env: map[string]string{"EXISTING": "old"}}
	plan, err := Read(context.Background(), root, current)
	if err != nil {
		t.Fatal(err)
	}
	setup := "bun install\ncp \"$CONDUCTOR_ROOT_PATH/.env\" . && echo $CONDUCTOR_BASE_DIR"
	if plan.Source != ".conductor/settings.toml" {
		t.Fatalf("source = %q", plan.Source)
	}
	want := domain.ProjectConfig{
		Env:        map[string]string{"EXISTING": "old", "API_URL": "http://127.0.0.1"},
		PostCreate: []string{copyStep(".env*", "config/*.local.json"), setup},
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
	wantWarnings := []Item{{Key: "scripts.setup", Detail: "CONDUCTOR_BASE_DIR"}}
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
		".gitignore":                     ".env.local\n",
		".env.local":                     "A=1",
		"conductor.json":                 `{"scripts":{"setup":"npm ci","run":"npm run dev","archive":"rm -rf tmp"},"runScriptMode":"concurrent"}`,
		".conductor/settings.local.toml": "[scripts]\narchive = \"make clean\"\n",
	}, ".gitignore", "conductor.json")
	plan, err := Read(context.Background(), root, domain.ProjectConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Source != "conductor.json" || !reflect.DeepEqual(plan.Config.PostCreate, []string{copyStep(".env*"), "npm ci"}) || !reflect.DeepEqual(plan.Config.PreRemove, []string{"make clean"}) {
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
	seen := map[int]bool{}
	for i := range 200 {
		workspace := fmt.Sprintf("/work/repo-%d", i)
		base := port(workspace)
		if base < 10000 || base+9 >= 32768 || base%10 != 0 || base != port(workspace) {
			t.Fatalf("port for %s = %d", workspace, base)
		}
		seen[base] = true
	}
	if len(seen) < 180 {
		t.Fatalf("200 workspaces share %d port blocks", len(seen))
	}
}

func TestEnvSetsTheRepositoryDefaultBranch(t *testing.T) {
	ctx := context.Background()
	root := repo(t, map[string]string{"a": "1"}, "a")
	for _, args := range [][]string{
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-qm", "init"},
		{"remote", "add", "origin", "https://example.com/r.git"},
		{"update-ref", "refs/remotes/origin/trunk", "HEAD"},
		{"symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	workspace := filepath.Join(t.TempDir(), "lima")
	env := Env(ctx, root, workspace)
	if env["CONDUCTOR_DEFAULT_BRANCH"] != "trunk" || env["CONDUCTOR_ROOT_PATH"] != root || env["CONDUCTOR_WORKSPACE_NAME"] != "lima" || env["CONDUCTOR_IS_LOCAL"] != "1" {
		t.Fatalf("env = %v", env)
	}
	if _, ok := Env(ctx, t.TempDir(), workspace)["CONDUCTOR_DEFAULT_BRANCH"]; ok {
		t.Fatal("a folder without a default branch got CONDUCTOR_DEFAULT_BRANCH")
	}
}

func TestReadPutsUserSettingsBelowTheRepository(t *testing.T) {
	root := repo(t, map[string]string{
		".conductor/settings.toml": "[scripts.run.dev]\ncommand = \"pnpm dev\"\n",
	}, ".conductor/settings.toml")
	user := filepath.Join(os.Getenv("HOME"), ".conductor", "settings.toml")
	if err := os.MkdirAll(filepath.Dir(user), 0o750); err != nil {
		t.Fatal(err)
	}
	body := "\"$schema\" = \"https://conductor.build/schemas/settings.schema.json\"\ntool_approvals_enabled = true\n\n[models]\ndefault = \"gpt-5.5\"\n\n[scripts.run.dev]\ncommand = \"pnpm dev:user\"\n\n[scripts.run.lint]\ncommand = \"pnpm lint\"\n\n[environment_variables]\nUSER_ONLY = \"1\"\n"
	if err := os.WriteFile(user, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := Read(context.Background(), root, domain.ProjectConfig{})
	if err != nil {
		t.Fatal(err)
	}
	wantCues := []CueDraft{{Key: "scripts.run.dev", Name: "dev", Command: "pnpm dev"}, {Key: "scripts.run.lint", Name: "lint", Command: "pnpm lint"}}
	if plan.Source != ".conductor/settings.toml" || !reflect.DeepEqual(plan.Cues, wantCues) || plan.Config.Env["USER_ONLY"] != "1" || len(plan.Skipped) != 0 {
		t.Fatalf("plan = %+v", plan)
	}
	bare := repo(t, map[string]string{"a": "1"}, "a")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(os.Getenv("HOME"), ".conductor", "settings.toml")), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".conductor", "settings.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if plan, err := Read(context.Background(), bare, domain.ProjectConfig{}); err != nil || plan.Source != "" || len(plan.Cues) != 0 {
		t.Fatalf("a repository without Conductor settings imported user settings: %+v, %v", plan, err)
	}
}

func TestCopyStepCopiesIgnoredFilesIntoTheWorkspace(t *testing.T) {
	root := repo(t, map[string]string{
		".gitignore":                   ".env\n*.local.json\n.local-secrets/\n",
		".env":                         "A=1",
		".worktreeinclude":             ".env\n*.local.json\n.local-secrets/**\n!.local-secrets/**/*.example\n",
		"config/it's a.local.json":     "{}",
		"config/linked.local.json":     "{}",
		".local-secrets/a/key":         "k",
		".local-secrets/a/key.example": "k=",
		".conductor/settings.toml":     "[scripts]\nsetup = \"true\"\n",
	}, ".gitignore", ".conductor/settings.toml")
	ctx := context.Background()
	plan, err := Read(ctx, root, domain.ProjectConfig{Symlinks: []string{"config/linked.local.json"}})
	if err != nil || len(plan.Config.PostCreate) != 2 || strings.Contains(plan.Added[0].Detail, ".example") {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "new.local.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	again, err := Read(ctx, root, plan.Config)
	if err != nil || len(again.Added) != 0 || !reflect.DeepEqual(again.Config, plan.Config) {
		t.Fatalf("a new matching file changed the import: %+v, %v", again.Added, err)
	}
	workspace := t.TempDir()
	linked := filepath.Join(workspace, "config", "linked.local.json")
	if err := os.MkdirAll(filepath.Dir(linked), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "config", "linked.local.json"), linked); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		run := exec.Command("sh", "-c", plan.Config.PostCreate[0])
		run.Dir, run.Env = workspace, append(os.Environ(), "AO_SOURCE_TREE_PATH="+root)
		if out, err := run.CombinedOutput(); err != nil {
			t.Fatalf("copy step: %v: %s", err, out)
		}
	}
	for _, name := range []string{".env", "config/it's a.local.json", "config/new.local.json", ".local-secrets/a/key"} {
		info, err := os.Lstat(filepath.Join(workspace, name))
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("%s in workspace: %v, %v", name, info, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(workspace, ".local-secrets/a/key.example")); !os.IsNotExist(err) {
		t.Fatalf("negated file copied: %v", err)
	}
	if info, err := os.Lstat(linked); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("linked file replaced: %v, %v", info, err)
	}
}

func TestCopyStepReviewListsTheFirstSortedFiles(t *testing.T) {
	files := map[string]string{".gitignore": ".env*\n", ".conductor/settings.toml": "[scripts]\nsetup = \"true\"\n"}
	for i := range maxCopiedFiles + 2 {
		files[fmt.Sprintf(".env.%03d", i)] = "x"
	}
	root := repo(t, files, ".gitignore", ".conductor/settings.toml")
	plan, err := Read(context.Background(), root, domain.ProjectConfig{})
	if err != nil {
		t.Fatal(err)
	}
	listed := strings.Split(plan.Added[0].Detail, ", ")
	if len(listed) != maxCopiedFiles+1 || listed[0] != ".env.000" || listed[maxCopiedFiles-1] != ".env.199" || listed[maxCopiedFiles] != "+2" {
		t.Fatalf("listed %d files: %q … %q", len(listed), listed[0], listed[len(listed)-1])
	}
}
