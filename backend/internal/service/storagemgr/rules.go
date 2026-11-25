package storagemgr

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// offloadTier says how an offloaded folder comes back.
type offloadTier int

const (
	// tierRebuilds folders come back on their own: the next build, test run
	// or tool start recreates them.
	tierRebuilds offloadTier = iota
	// tierInstall folders need an install step (npm ci, uv sync, pod install)
	// to come back, so they are offloaded only when one is guaranteed.
	tierInstall
)

// offloadRule describes one kind of folder AO may offload.
type offloadRule struct {
	tier offloadTier
	// beside lists file-name patterns of which one must match a regular file
	// in the folder's parent (for a two-segment rule, beside its first
	// segment): the file that proves what the folder is, such as App.csproj
	// beside bin/. Empty means the name alone is unambiguous.
	beside []string
	// inside names a regular file that must exist inside the folder, such as
	// pyvenv.cfg in a virtualenv.
	inside string
	// packageManaged folders are filled by a package manager from its
	// registry, never with the user's own files, so they are not searched for
	// secrets (packages routinely ship test keys).
	packageManaged bool
}

// offloadRules are the folders an offload may take, keyed by folder name or,
// for a folder only recognisable with its parent, by both segments. A folder
// is offloaded only when git ignores it AND it matches a rule whose marker is
// present; everything else is left alone.
var offloadRules = map[string]offloadRule{
	// JavaScript and TypeScript.
	"node_modules":     {tier: tierInstall, packageManaged: true},
	".yarn/cache":      {tier: tierInstall, beside: []string{".yarnrc.yml"}, packageManaged: true},
	"dist":             {tier: tierRebuilds},
	"build":            {tier: tierRebuilds},
	"out":              {tier: tierRebuilds},
	"coverage":         {tier: tierRebuilds},
	".cache":           {tier: tierRebuilds},
	".next":            {tier: tierRebuilds},
	".nuxt":            {tier: tierRebuilds},
	".svelte-kit":      {tier: tierRebuilds},
	".turbo":           {tier: tierRebuilds},
	".vite":            {tier: tierRebuilds},
	".parcel-cache":    {tier: tierRebuilds},
	".angular":         {tier: tierRebuilds},
	".expo":            {tier: tierRebuilds},
	".webpack":         {tier: tierRebuilds},
	"storybook-static": {tier: tierRebuilds},
	".nx/cache":        {tier: tierRebuilds, beside: []string{"nx.json"}},
	// Python. A virtualenv is recognised by the pyvenv.cfg every tool writes.
	".venv":         {tier: tierInstall, inside: "pyvenv.cfg", packageManaged: true},
	"venv":          {tier: tierInstall, inside: "pyvenv.cfg", packageManaged: true},
	"env":           {tier: tierInstall, inside: "pyvenv.cfg", packageManaged: true},
	"__pycache__":   {tier: tierRebuilds, packageManaged: true},
	".pytest_cache": {tier: tierRebuilds, packageManaged: true},
	".mypy_cache":   {tier: tierRebuilds, packageManaged: true},
	".ruff_cache":   {tier: tierRebuilds, packageManaged: true},
	".tox":          {tier: tierRebuilds, packageManaged: true},
	// Rust and Maven.
	"target": {tier: tierRebuilds},
	// Gradle and Android.
	".gradle": {tier: tierRebuilds, packageManaged: true},
	".cxx":    {tier: tierRebuilds, beside: []string{"build.gradle", "build.gradle.kts"}},
	// Apple.
	"Pods":        {tier: tierInstall, beside: []string{"Podfile"}, packageManaged: true},
	"DerivedData": {tier: tierRebuilds},
	".build":      {tier: tierRebuilds, beside: []string{"Package.swift"}},
	// Dart and Flutter.
	".dart_tool": {tier: tierInstall, beside: []string{"pubspec.yaml"}, packageManaged: true},
	// .NET.
	"bin": {tier: tierRebuilds, beside: dotnetProjects},
	"obj": {tier: tierRebuilds, beside: dotnetProjects},
	// PHP and Ruby.
	"vendor":        {tier: tierInstall, beside: []string{"composer.json"}, packageManaged: true},
	"vendor/bundle": {tier: tierInstall, beside: []string{"Gemfile"}, packageManaged: true},
	// Elixir.
	"_build": {tier: tierRebuilds, beside: []string{"mix.exs"}},
	"deps":   {tier: tierInstall, beside: []string{"mix.exs"}, packageManaged: true},
	// Haskell.
	".stack-work":   {tier: tierRebuilds, beside: []string{"stack.yaml"}},
	"dist-newstyle": {tier: tierRebuilds, beside: []string{"cabal.project", "*.cabal"}},
	// Zig.
	".zig-cache": {tier: tierRebuilds, beside: []string{"build.zig"}},
	"zig-cache":  {tier: tierRebuilds, beside: []string{"build.zig"}},
	"zig-out":    {tier: tierRebuilds, beside: []string{"build.zig"}},
	// Terraform.
	".terraform": {tier: tierInstall, packageManaged: true},
}

var dotnetProjects = []string{"*.csproj", "*.fsproj", "*.vbproj"}

// offloadPrefixRules match folders named by a prefix, such as CLion's
// cmake-build-debug and cmake-build-release.
var offloadPrefixRules = map[string]offloadRule{
	"cmake-build-": {tier: tierRebuilds, beside: []string{"CMakeLists.txt"}},
}

// matchOffloadRule finds the rule for a folder (slash-separated, relative to
// worktree) and reports whether its marker is present.
func matchOffloadRule(worktree, rel string) (offloadRule, bool) {
	rule, anchor, ok := lookupOffloadRule(rel)
	if !ok {
		return offloadRule{}, false
	}
	abs := filepath.Join(worktree, filepath.FromSlash(rel))
	if rule.inside != "" && !isRegularFile(filepath.Join(abs, rule.inside)) {
		return rule, false
	}
	if len(rule.beside) > 0 && !dirHasFileMatching(filepath.Join(worktree, filepath.FromSlash(anchor)), rule.beside) {
		return rule, false
	}
	return rule, true
}

// lookupOffloadRule returns rel's rule and the folder its beside marker must
// sit in.
func lookupOffloadRule(rel string) (offloadRule, string, bool) {
	parent := path.Dir(rel)
	if parent != "." {
		if rule, ok := offloadRules[path.Base(parent)+"/"+path.Base(rel)]; ok {
			return rule, path.Dir(parent), true
		}
	}
	base := path.Base(rel)
	if rule, ok := offloadRules[base]; ok {
		return rule, parent, true
	}
	for prefix, rule := range offloadPrefixRules {
		if strings.HasPrefix(base, prefix) && len(base) > len(prefix) {
			return rule, parent, true
		}
	}
	return offloadRule{}, "", false
}

func isRegularFile(file string) bool {
	info, err := os.Lstat(file)
	return err == nil && info.Mode().IsRegular()
}

// dirHasFileMatching reports whether dir holds a regular file whose name
// matches one of patterns.
func dirHasFileMatching(dir string, patterns []string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		for _, pattern := range patterns {
			if matched, _ := path.Match(pattern, entry.Name()); matched {
				return true
			}
		}
	}
	return false
}

// offloadPolicy is what a project allows an offload to take.
type offloadPolicy struct {
	// ExtraPaths are the project's storageOffloadPaths: folders the project
	// itself declares rebuildable.
	ExtraPaths []string
	// HasSetup reports a Setup (postCreate) script that runs on restore and
	// can reinstall dependencies.
	HasSetup bool
	// Cloneable reports that node_modules can come back from a copy-on-write
	// template keyed by its lockfile.
	Cloneable bool
}

func (s *Service) offloadPolicyFor(project domain.ProjectRecord) offloadPolicy {
	policy := offloadPolicy{ExtraPaths: project.Config.StorageOffloadPaths, Cloneable: s.cloneSupported()}
	for _, command := range project.Config.PostCreate {
		if strings.TrimSpace(command) != "" {
			policy.HasSetup = true
			break
		}
	}
	return policy
}

// reinstallable reports whether an install-tier folder is guaranteed to come
// back on restore: the project's Setup script reinstalls it, or it is a
// node_modules a copy-on-write template keyed by its lockfile can restore.
func (p offloadPolicy) reinstallable(worktree, rel string) bool {
	if p.HasSetup {
		return true
	}
	if !p.Cloneable || path.Base(rel) != "node_modules" {
		return false
	}
	return hasLockfile(filepath.Join(worktree, filepath.FromSlash(path.Dir(rel))))
}

func hasLockfile(dir string) bool {
	for _, name := range lockfileNames {
		if isRegularFile(filepath.Join(dir, name)) {
			return true
		}
	}
	return false
}

// describeHeld names install-tier folders an offload left in place.
func describeHeld(held []string) string {
	if len(held) == 0 {
		return ""
	}
	const shown = 3
	names := strings.Join(held[:min(shown, len(held))], ", ")
	if len(held) > shown {
		names += fmt.Sprintf(" and %d more", len(held)-shown)
	}
	return "kept " + names + ": no Setup script would reinstall them"
}
