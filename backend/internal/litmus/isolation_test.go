package litmus

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func boolPtr(value bool) *bool { return &value }

func TestLitmusSourcesContainNoTargetSpecificSettings(t *testing.T) {
	files, err := filepath.Glob("*.go")
	is(t, "")(nil, err)
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		data, err := os.ReadFile(file)
		is(t, "")(nil, err)
		for _, literal := range []string{"AO_", DefaultRepo, "DefaultRepo", "ao-brd-"} {
			if strings.Contains(string(data), literal) {
				t.Errorf("%s contains target-specific setting %q", file, literal)
			}
		}
		tree, err := parser.ParseFile(token.NewFileSet(), file, data, 0)
		is(t, "")(nil, err)
		var pidReader *ast.FuncDecl
		for _, decl := range tree.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && file == "target.go" && fn.Name.Name == "startLeg" {
				pidReader = fn // ready.pidFile in part 2 must remove this last exception.
			}
		}
		kindLiterals := map[token.Pos]bool{}
		ast.Inspect(tree, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.BinaryExpr:
				if field, ok := n.X.(*ast.SelectorExpr); ok && field.Sel.Name == "Kind" {
					if lit, ok := n.Y.(*ast.BasicLit); ok {
						kindLiterals[lit.Pos()] = true
					}
				}
			case *ast.CompositeLit:
				var values []string
				for _, elem := range n.Elts {
					if lit, ok := elem.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						value, err := strconv.Unquote(lit.Value)
						is(t, "")(nil, err)
						values = append(values, value)
					}
				}
				if slices.Equal(values, []string{"electron", "web", "cli", "api"}) {
					for _, elem := range n.Elts {
						kindLiterals[elem.Pos()] = true
					}
				}
			}
			return true
		})
		ast.Inspect(tree, func(node ast.Node) bool {
			if lit, ok := node.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				value, err := strconv.Unquote(lit.Value)
				is(t, "")(nil, err)
				if value == "running.json" && (pidReader == nil || lit.Pos() < pidReader.Pos() || lit.End() > pidReader.End()) {
					t.Errorf("%s contains running.json outside startLeg", file)
				}
				if value == "data" || (value == "electron" && !kindLiterals[lit.Pos()]) {
					t.Errorf("%s contains target-specific path part %q", file, value)
				}
			}
			return true
		})
	}
}

func TestIsolationEnvExpandsOnlyLitmusVariables(t *testing.T) {
	r, err := ParseRecipe([]byte(`kind: electron
run: {cmd: ./other-app}
env: {APP_PORT: wrong}
isolation:
  env:
    APP_PORT: "$LITMUS_APP_PORT"
    PATHS: "${LITMUS_ROOT}|$LITMUS_LEG|$LITMUS_LEG_DIR|$LITMUS_RUN_ID|$LITMUS_RUN_NUM|$LITMUS_DEBUG_PORT"
`))
	is(t, "")(nil, err)
	env, err := legEnv(r, Run{ID: "run_test-17"}, "base", "/run/base", "/checkout", "", 1234, 5678, "/private/tmp/lt-fixture")
	is(t, "")(nil, err)
	if envValue(env, "APP_PORT") != "1234" || envValue(env, "PATHS") != "/checkout|base|/run/base|run_test-17|17|5678" {
		t.Fatalf("isolation did not use the launch coordinates: %v", env)
	}
	for _, name := range []string{"HOME", "PATH", "AO_PORT", "LITMUS_DAEMON_PORT", "LITMUS_TYPO"} {
		r.Isolation.Env["APP_PORT"] = "${" + name + "}"
		data, err := yaml.Marshal(r)
		is(t, "")(nil, err)
		if _, err := ParseRecipe(data); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("unknown expansion %s did not fail validation: %v", name, err)
		}
		if _, err := legEnv(r, Run{}, "head", "/run/head", "/checkout", "", 1, 2, "/private/tmp/lt-fixture"); err == nil {
			t.Errorf("unvalidated expansion %s reached launch", name)
		}
	}
}

func TestPrivateHomeDefaultsAndExplicitSharedHome(t *testing.T) {
	owner := t.TempDir()
	t.Setenv("HOME", owner)
	for _, tc := range []struct {
		kind    string
		home    *bool
		private bool
	}{
		{"electron", nil, true}, {"electron", boolPtr(false), false},
		{"web", nil, false}, {"web", boolPtr(true), true},
	} {
		r := Recipe{Kind: tc.kind, Isolation: Isolation{Home: tc.home}}
		env, err := legEnv(r, Run{}, "head", "/leg", "/checkout", "", 1, 2, "/private/tmp/lt-fixture")
		is(t, "")(nil, err)
		want := owner
		if tc.private {
			want = "/leg/home"
		}
		if envValue(env, "HOME") != want {
			t.Errorf("%+v got HOME %q, want %q", tc, envValue(env, "HOME"), want)
		}
		wantLitmusHome := ""
		if tc.private {
			wantLitmusHome = want
		}
		if envValue(env, "LITMUS_HOME") != wantLitmusHome || envValue(env, "TMPDIR") != "/private/tmp/lt-fixture" {
			t.Fatalf("wrong private HOME coordinate or TMPDIR: %v", env)
		}
		if tc.private {
			for key, path := range privateHomeEnv("/leg") {
				if envValue(env, key) != path {
					t.Errorf("%s = %q, want %q", key, envValue(env, key), path)
				}
			}
		}
	}
}

func TestAORecipeSharesHomeWithoutProviderOverrides(t *testing.T) {
	r, err := ParseRecipe(defaultRecipe)
	is(t, "")(nil, err)
	owner := t.TempDir()
	t.Setenv("HOME", owner)
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "CURSOR_DATA_DIR", "OPENCODE_DATA_DIR", "GEMINI_HOME", "KIMI_SHARE_DIR", "KIMI_CODE_HOME", "GROK_HOME", "CLINE_DIR", "CLINE_DATA_DIR", "CLINE_PROVIDER_SETTINGS_PATH", "ANTHROPIC_API_KEY", "OPENAI_API_KEY"} {
		t.Setenv(key, "/owner/private")
	}
	head, err := legEnv(r, Run{ID: "run_x"}, "head", "/run/head", "/checkout", "", 1, 2, "/private/tmp/lt-fixture")
	is(t, "")(nil, err)
	base, err := legEnv(r, Run{ID: "run_x"}, "base", "/run/base", "/checkout", "", 3, 4, "/private/tmp/lt-fixture")
	is(t, "")(nil, err)
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "OPENCODE_DATA_DIR", "GEMINI_HOME", "KIMI_SHARE_DIR", "KIMI_CODE_HOME", "GROK_HOME", "CLINE_DIR", "CLINE_DATA_DIR", "CLINE_PROVIDER_SETTINGS_PATH"} {
		if _, ok := r.Isolation.Env[key]; ok || envValue(head, key) != "" || envValue(base, key) != "" {
			t.Errorf("recipe still overrides %s", key)
		}
	}
	for _, env := range [][]string{head, base} {
		if envValue(env, "HOME") != owner || envValue(env, "LITMUS_HOME") != "" {
			t.Fatal("AO leg does not use the owner's HOME")
		}
		if slices.ContainsFunc(env, func(v string) bool { return strings.Contains(v, "/owner/private") }) {
			t.Fatal("owner credentials or state paths reached a leg")
		}
	}
	if envValue(head, "CURSOR_DATA_DIR") != "" || envValue(base, "CURSOR_DATA_DIR") != "" {
		t.Fatal("ineffective Cursor override reached a leg")
	}
	for _, note := range []string{"Agents in a leg use the owner's real logins; keep agent turns short."} {
		// YAML block values drop the common indentation.
		if !strings.Contains(r.Notes, strings.ReplaceAll(note, "\n  ", "\n")) {
			t.Errorf("recipe omits %q", note)
		}
	}
}

func TestIsolationRejectsManagedHomeKeys(t *testing.T) {
	for _, key := range []string{"HOME", "TMPDIR", "XDG_CONFIG_HOME", "XDG_OTHER", "npm_config_prefix", "npm_config_cache", "APPDATA", "USERPROFILE", "LOCALAPPDATA", "TMP", "TEMP"} {
		for _, private := range []bool{true, false} {
			r := Recipe{Kind: "electron", Run: Step{Cmd: "fixture"}, Isolation: Isolation{Home: boolPtr(private), Env: map[string]string{key: "custom"}}}
			data, err := yaml.Marshal(r)
			is(t, "")(nil, err)
			_, err = ParseRecipe(data)
			if (err != nil) != (private || slices.Contains([]string{"TMPDIR", "TMP", "TEMP"}, key)) || (err != nil && !strings.Contains(err.Error(), key)) {
				t.Errorf("private=%t key=%s: %v", private, key, err)
			}
		}
	}
}

func TestLitmusHomeRequiresPrivateHome(t *testing.T) {
	for _, field := range []string{
		"isolation:\n  home: false\n  env: {PROVIDER_HOME: '${LITMUS_HOME}/provider'}\n",
		"isolation:\n  home: false\n  dirs: ['$LITMUS_HOME/provider']\n",
		"isolation: {home: false}\nenv: {PROVIDER_HOME: '$LITMUS_HOME/provider'}\n",
		"isolation: {home: false}\nrun: {cmd: fixture, args: ['$LITMUS_HOME/provider']}\n",
		"isolation: {home: false}\nseed: [{cmd: 'echo ${LITMUS_HOME}'}]\n",
	} {
		source := "kind: electron\n"
		if !strings.Contains(field, "\nrun:") {
			source += "run: {cmd: fixture}\n"
		}
		if _, err := ParseRecipe([]byte(source + field)); err == nil || !strings.Contains(err.Error(), "LITMUS_HOME") {
			t.Errorf("shared home accepted %s: %v", field, err)
		}
	}
}

func TestIsolationDirsStayInsideLeg(t *testing.T) {
	for _, path := range []string{"$LITMUS_LEG_DIR/profile", "${LITMUS_HOME}/provider", "$LITMUS_LEG_DIR/state/nested"} {
		r, err := ParseRecipe([]byte("kind: electron\nrun: {cmd: fixture}\nisolation:\n  dirs: ['" + path + "']\n"))
		is(t, "")(nil, err)
		dirs, err := r.isolationDirs(func(key string) string {
			if key == "LITMUS_HOME" {
				return "/run/head/home"
			}
			return "/run/head"
		})
		is(t, "")(nil, err)
		if len(dirs) != 1 || !strings.HasPrefix(dirs[0], "/run/head/") {
			t.Fatalf("directory did not expand under the leg: %v", dirs)
		}
	}
	for _, path := range []string{"", "/outside", "relative", "$LITMUS_LEG_DIR", "$LITMUS_LEG_DIR/../escape", "$LITMUS_LEG_DIR/../../escape", "$LITMUS_ROOT/outside", "$LITMUS_TYPO/x"} {
		if _, err := ParseRecipe([]byte("kind: electron\nrun: {cmd: fixture}\nisolation:\n  dirs: ['" + path + "']\n")); err == nil || !strings.Contains(err.Error(), "isolation.dirs") {
			t.Errorf("invalid directory %q passed: %v", path, err)
		}
	}
}

func TestContextPackNamesTheSpawningBinary(t *testing.T) {
	svc, _ := newSvc(t)
	run, err := svc.Intake(ctx, "s1", prURL)
	is(t, "")(nil, err)
	exe, err := os.Executable()
	is(t, "")(nil, err)
	if pack := readRunFile(t, svc, run, "context-pack.md"); !strings.Contains(pack, "CLI binary: "+exe+"; its directory is first on the tester PATH") {
		t.Fatalf("pack omits binary identity:\n%s", pack)
	}
}
