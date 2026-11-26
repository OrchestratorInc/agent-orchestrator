package storagemgr

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// markerRepo builds a committed git repo ignoring ignore, with a file inside
// folder and the given marker files written after the commit.
func markerRepo(t *testing.T, ignore, folder string, markers []string) string {
	t.Helper()
	requireGit(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".gitignore"), ignore)
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "-c", "user.name=AO test", "-c", "user.email=test@example.invalid", "commit", "-q", "-m", "init")
	writeFile(t, filepath.Join(dir, filepath.FromSlash(folder), "content.bin"), "generated")
	for _, marker := range markers {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(marker)), "marker")
	}
	return dir
}

func targetRels(targets []offloadTarget) []string {
	rels := make([]string, 0, len(targets))
	for _, target := range targets {
		rels = append(rels, target.Rel)
	}
	return rels
}

func TestOffloadMarkersRecogniseEachLanguage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ignore  string
		folder  string
		markers []string // proving files; without them the folder is left alone
	}{
		{name: "dotnet bin beside csproj", ignore: "bin/\n", folder: "api/bin", markers: []string{"api/Api.csproj"}},
		{name: "dotnet obj beside fsproj", ignore: "obj/\n", folder: "api/obj", markers: []string{"api/Api.fsproj"}},
		{name: "php vendor beside composer.json", ignore: "/vendor/\n", folder: "vendor", markers: []string{"composer.json"}},
		{name: "ruby vendor/bundle beside Gemfile", ignore: "/vendor/bundle/\n", folder: "vendor/bundle", markers: []string{"Gemfile"}},
		{name: "elixir _build beside mix.exs", ignore: "/_build/\n", folder: "_build", markers: []string{"mix.exs"}},
		{name: "elixir deps beside mix.exs", ignore: "/deps/\n", folder: "deps", markers: []string{"mix.exs"}},
		{name: "python .venv holding pyvenv.cfg", ignore: ".venv/\n", folder: ".venv", markers: []string{".venv/pyvenv.cfg"}},
		{name: "python venv holding pyvenv.cfg", ignore: "venv/\n", folder: "venv", markers: []string{"venv/pyvenv.cfg"}},
		{name: "python env holding pyvenv.cfg", ignore: "env/\n", folder: "env", markers: []string{"env/pyvenv.cfg"}},
		{name: "swift .build beside Package.swift", ignore: ".build/\n", folder: ".build", markers: []string{"Package.swift"}},
		{name: "haskell .stack-work beside stack.yaml", ignore: ".stack-work/\n", folder: ".stack-work", markers: []string{"stack.yaml"}},
		{name: "haskell dist-newstyle beside a cabal file", ignore: "dist-newstyle/\n", folder: "dist-newstyle", markers: []string{"app.cabal"}},
		{name: "cmake build folder beside CMakeLists.txt", ignore: "cmake-build-*/\n", folder: "cmake-build-debug", markers: []string{"CMakeLists.txt"}},
		{name: "zig zig-out beside build.zig", ignore: "zig-out/\n", folder: "zig-out", markers: []string{"build.zig"}},
		{name: "zig .zig-cache beside build.zig", ignore: ".zig-cache/\n", folder: ".zig-cache", markers: []string{"build.zig"}},
		{name: "android .cxx beside build.gradle.kts", ignore: ".cxx/\n", folder: "app/.cxx", markers: []string{"app/build.gradle.kts"}},
		{name: "nx cache beside nx.json", ignore: "/.nx/cache/\n", folder: ".nx/cache", markers: []string{"nx.json"}},
		{name: "yarn cache beside .yarnrc.yml", ignore: "/.yarn/cache/\n", folder: ".yarn/cache", markers: []string{".yarnrc.yml"}},
		{name: "cocoapods Pods beside Podfile", ignore: "Pods/\n", folder: "Pods", markers: []string{"Podfile"}},
		{name: "dart .dart_tool beside pubspec.yaml", ignore: ".dart_tool/\n", folder: ".dart_tool", markers: []string{"pubspec.yaml"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := offloadPolicy{HasSetup: true}
			with := markerRepo(t, tc.ignore, tc.folder, tc.markers)
			targets, _, err := findOffloadTargets(context.Background(), with, policy)
			if err != nil {
				t.Fatal(err)
			}
			if got := targetRels(targets); !reflect.DeepEqual(got, []string{tc.folder}) {
				t.Fatalf("with its marker: targets = %v, want [%s]", got, tc.folder)
			}
			without := markerRepo(t, tc.ignore, tc.folder, nil)
			targets, _, err = findOffloadTargets(context.Background(), without, policy)
			if err != nil {
				t.Fatal(err)
			}
			if len(targets) != 0 {
				t.Fatalf("without its marker: targets = %v, want none", targetRels(targets))
			}
		})
	}
}

func TestOffloadMarkerMustSitBesideTheFolder(t *testing.T) {
	// A project file in api/ proves api/bin, not a scripts/bin elsewhere.
	dir := markerRepo(t, "bin/\n", "api/bin", []string{"api/Api.csproj"})
	writeFile(t, filepath.Join(dir, "scripts", "bin", "deploy.sh"), "#!/bin/sh")
	targets, _, err := findOffloadTargets(context.Background(), dir, offloadPolicy{HasSetup: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := targetRels(targets); !reflect.DeepEqual(got, []string{"api/bin"}) {
		t.Fatalf("targets = %v, want [api/bin]", got)
	}
}

func TestOffloadInstallTierNeedsAWayBack(t *testing.T) {
	dir := markerRepo(t, "node_modules/\n.venv/\ntarget/\n", "target", []string{"package-lock.json", ".venv/pyvenv.cfg"})
	writeFile(t, filepath.Join(dir, "node_modules", "dep", "index.js"), "dependency")
	writeFile(t, filepath.Join(dir, "packages", "x", "node_modules", "y", "index.js"), "nested dependency")
	for _, tc := range []struct {
		name    string
		policy  offloadPolicy
		targets []string
		held    []string
	}{
		{
			name:    "no setup and no clone",
			targets: []string{"target"},
			held:    []string{".venv", "node_modules", "packages/x/node_modules"},
		},
		{
			// The template is keyed by a lockfile: only the root has one.
			name:    "clone with a lockfile",
			policy:  offloadPolicy{Cloneable: true},
			targets: []string{"node_modules", "target"},
			held:    []string{".venv", "packages/x/node_modules"},
		},
		{
			name:    "setup script",
			policy:  offloadPolicy{HasSetup: true},
			targets: []string{".venv", "node_modules", "packages/x/node_modules", "target"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			targets, held, err := findOffloadTargets(context.Background(), dir, tc.policy)
			if err != nil {
				t.Fatal(err)
			}
			if got := targetRels(targets); !reflect.DeepEqual(got, tc.targets) {
				t.Fatalf("targets = %v, want %v", got, tc.targets)
			}
			if !reflect.DeepEqual(held, tc.held) {
				t.Fatalf("held = %v, want %v", held, tc.held)
			}
		})
	}
}

func TestOffloadPolicyForReadsTheSetupScript(t *testing.T) {
	svc, _ := newTestService(t, newFakeStore(), nil, nil, nil)
	if svc.offloadPolicyFor(domain.ProjectRecord{Config: domain.ProjectConfig{PostCreate: []string{"  "}}}).HasSetup {
		t.Fatal("a blank Setup command reinstalls nothing")
	}
	if !svc.offloadPolicyFor(domain.ProjectRecord{Config: domain.ProjectConfig{PostCreate: []string{"uv sync"}}}).HasSetup {
		t.Fatal("a Setup command must count")
	}
}

func TestOffloadReasonNamesHeldBackFolders(t *testing.T) {
	sess := sessionView{ID: "p-1", Quiet: true, LastActivity: policyNow.Add(-100 * time.Hour)}
	wt := quietWorktree("p-1")
	wt.HeldBack = []string{".venv", "Pods"}
	actions := plan(policyInput{Now: policyNow, Settings: domain.DefaultStorageSettings(), Snapshot: &scanSnapshot{Sessions: []sessionView{sess}, Worktrees: []WorktreeUsage{wt}}})
	action, ok := findAction(actions, domain.StorageCleanOffload, wt.Path)
	if !ok || !strings.Contains(action.Reason, "kept .venv, Pods: no Setup script would reinstall them") {
		t.Fatalf("action=%+v ok=%v, want its reason to name the held-back folders", action, ok)
	}
}

func TestKeptIgnoredUsesTheSameMarkers(t *testing.T) {
	// In an ended worktree a bin/ with no project file is the user's own.
	dir := markerRepo(t, "bin/\n", "tools/bin", nil)
	writeFile(t, filepath.Join(dir, "api", "bin", "Debug", "app.dll"), "built")
	writeFile(t, filepath.Join(dir, "api", "Api.csproj"), "<Project />")
	kept, err := keptIgnored(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"tools/bin/"}; !reflect.DeepEqual(kept, want) {
		t.Fatalf("kept = %q, want %q", kept, want)
	}
}
