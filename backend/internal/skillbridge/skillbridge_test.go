package skillbridge

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

func writeSkill(t *testing.T, root, folder, name, body string) string {
	t.Helper()
	dir := filepath.Join(root, folder)
	writeFile(t, filepath.Join(dir, "SKILL.md"),
		"---\nname: "+name+"\ndescription: Does "+name+".\n---\n"+body+"\n")
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func sync(t *testing.T, cfg Config) Result {
	t.Helper()
	if cfg.Layout == "" {
		cfg.Layout = AgentsLayout
	}
	result, err := Sync(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	sort.Strings(result.Skills)
	return result
}

func bridged(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, ".agents", "skills"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestSyncCopiesSkillsFromEveryRootPreferringTheFirst(t *testing.T) {
	home, dir := t.TempDir(), filepath.Join(t.TempDir(), "session")
	roots := UserRoots(home)
	writeSkill(t, roots[0], "shared", "shared", "from agents")
	writeSkill(t, roots[1], "shared", "shared", "from claude")
	writeSkill(t, roots[1], "renamed", "SHARED", "same name, other folder")
	writeSkill(t, roots[1], "claude-only", "claude-only", "c")
	writeSkill(t, roots[2], "codex-only", "codex-only", "x")
	writeSkill(t, roots[2], ".system", "system", "codex's own")

	result := sync(t, Config{Dir: dir, Sources: roots})
	want := []string{"claude-only", "codex-only", "shared"}
	if !reflect.DeepEqual(result.Skills, want) || !reflect.DeepEqual(bridged(t, dir), want) {
		t.Fatalf("skills = %v, on disk %v, want %v", result.Skills, bridged(t, dir), want)
	}
	if got := readFile(t, filepath.Join(dir, ".agents", "skills", "shared", "SKILL.md")); !strings.Contains(got, "from agents") {
		t.Fatalf("shared skill = %q, want the ~/.agents copy", got)
	}
}

func TestSyncLeavesOutSkillsTheSessionAlreadyHas(t *testing.T) {
	home, workspace, global := t.TempDir(), t.TempDir(), t.TempDir()
	dir := filepath.Join(t.TempDir(), "session")
	source := UserRoots(home)[0]
	for _, name := range []string{"project", "claude-project", "global", "by-name", "declared", "mine"} {
		writeSkill(t, source, name, name, "user copy")
	}
	writeSkill(t, filepath.Join(workspace, ".agents", "skills"), "project", "project", "")
	writeSkill(t, filepath.Join(workspace, ".claude", "skills"), "claude-project", "claude-project", "")
	writeSkill(t, global, "global", "global", "")
	writeSkill(t, global, "another-folder", "by-name", "")
	single := writeSkill(t, t.TempDir(), "declared", "declared", "")

	native := append(ProjectRoots(workspace), global, single)
	if got := sync(t, Config{Dir: dir, Sources: []string{source}, Native: native}).Skills; !reflect.DeepEqual(got, []string{"mine"}) {
		t.Fatalf("skills = %v, want only the one the session lacks", got)
	}
}

func TestSyncSkipsSkillsAgentsWouldNotLoad(t *testing.T) {
	home, dir := t.TempDir(), filepath.Join(t.TempDir(), "session")
	root := UserRoots(home)[0]
	writeSkill(t, root, "good", "good", "")
	writeFile(t, filepath.Join(root, "crlf", "SKILL.md"), "\xef\xbb\xbf---\r\nname: crlf\r\ndescription: ok\r\n---\r\n")
	writeFile(t, filepath.Join(root, "no-name", "SKILL.md"), "---\ndescription: d\n---\n")
	writeFile(t, filepath.Join(root, "no-description", "SKILL.md"), "---\nname: n\n---\n")
	writeFile(t, filepath.Join(root, "no-frontmatter", "SKILL.md"), "# Title\n")
	writeFile(t, filepath.Join(root, "bad-yaml", "SKILL.md"), "---\nname: [\n---\n")
	writeFile(t, filepath.Join(root, "not-a-skill", "README.md"), "hi")
	writeSkill(t, root, "huge", "huge", strings.Repeat("x", 4096))

	result := sync(t, Config{Dir: dir, Sources: []string{root}, Limits: Limits{SkillBytes: 1024, SkillFiles: 10, TotalBytes: 1 << 20}})
	if !reflect.DeepEqual(result.Skills, []string{"crlf", "good"}) {
		t.Fatalf("skills = %v", result.Skills)
	}
	if len(result.Skipped) != 5 {
		t.Fatalf("skipped = %+v, want the five unusable skills", result.Skipped)
	}
}

func TestSyncStopsAtTheTotalLimit(t *testing.T) {
	root, dir := t.TempDir(), filepath.Join(t.TempDir(), "session")
	writeSkill(t, root, "a", "a", strings.Repeat("x", 600))
	writeSkill(t, root, "b", "b", strings.Repeat("x", 600))
	result := sync(t, Config{Dir: dir, Sources: []string{root}, Limits: Limits{SkillBytes: 2048, SkillFiles: 10, TotalBytes: 1000}})
	if !reflect.DeepEqual(result.Skills, []string{"a"}) || len(result.Skipped) != 1 {
		t.Fatalf("result = %+v, want only the first skill", result)
	}
}

func TestSyncMakesRealCopiesAndKeepsLinksInsideTheSkill(t *testing.T) {
	home, outside, dir := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "session")
	root := UserRoots(home)[0]
	elsewhere := writeSkill(t, t.TempDir(), "linked", "linked", "")
	writeFile(t, filepath.Join(elsewhere, "scripts", "run.sh"), "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(elsewhere, "scripts", "run.sh"), 0o555); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(elsewhere, ".git", "config"), "[core]")
	writeFile(t, filepath.Join(outside, "secret"), "s3cret")
	symlink(t, filepath.Join(elsewhere, "scripts", "run.sh"), filepath.Join(elsewhere, "alias.sh"))
	symlink(t, filepath.Join(outside, "secret"), filepath.Join(elsewhere, "leak"))
	symlink(t, outside, filepath.Join(elsewhere, "leakdir"))
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	symlink(t, elsewhere, filepath.Join(root, "linked"))
	// The same skill reached through a second root is one skill.
	if err := os.MkdirAll(UserRoots(home)[1], 0o755); err != nil {
		t.Fatal(err)
	}
	symlink(t, elsewhere, filepath.Join(UserRoots(home)[1], "linked-again"))

	if got := sync(t, Config{Dir: dir, Sources: UserRoots(home)}).Skills; !reflect.DeepEqual(got, []string{"linked"}) {
		t.Fatalf("skills = %v", got)
	}
	copied := filepath.Join(dir, ".agents", "skills", "linked")
	err := filepath.WalkDir(copied, func(path string, entry os.DirEntry, err error) error {
		if err == nil && entry.Type()&os.ModeSymlink != 0 {
			t.Errorf("%s is a symlink, want a real copy", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(copied, "alias.sh")); got != "#!/bin/sh\n" {
		t.Fatalf("alias.sh = %q", got)
	}
	for _, absent := range []string{"leak", "leakdir", ".git"} {
		if _, err := os.Lstat(filepath.Join(copied, absent)); !os.IsNotExist(err) {
			t.Errorf("%s was copied", absent)
		}
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(copied, "scripts", "run.sh"))
		if err != nil || info.Mode().Perm() != 0o755 {
			t.Fatalf("run.sh mode = %v, %v, want executable and replaceable", info.Mode(), err)
		}
	}
}

func TestSyncUpdatesOnlyWhatChangedAndRemovesWhatIsGone(t *testing.T) {
	root, project, dir := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "session")
	writeSkill(t, root, "kept", "kept", "v1")
	edited := writeSkill(t, root, "edited", "edited", "v1")
	gone := writeSkill(t, root, "gone", "gone", "")
	writeSkill(t, root, "claimed", "claimed", "")
	cfg := Config{Dir: dir, Sources: []string{root}, Native: []string{project}}
	if first := sync(t, cfg); first.Copied != 4 {
		t.Fatalf("first sync copied %d, want 4", first.Copied)
	}
	if again := sync(t, cfg); again.Copied != 0 || len(again.Skills) != 4 {
		t.Fatalf("unchanged sync = %+v, want nothing copied", again)
	}

	writeFile(t, filepath.Join(edited, "SKILL.md"), "---\nname: edited\ndescription: d\n---\nv2\n")
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(edited, "SKILL.md"), later, later); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	// The project gains its own skill of that name.
	writeSkill(t, project, "claimed", "claimed", "")
	result := sync(t, cfg)
	if result.Copied != 1 || !reflect.DeepEqual(bridged(t, dir), []string{"edited", "kept"}) {
		t.Fatalf("result = %+v, on disk %v", result, bridged(t, dir))
	}
	if got := readFile(t, filepath.Join(dir, ".agents", "skills", "edited", "SKILL.md")); !strings.Contains(got, "v2") {
		t.Fatalf("edited skill = %q", got)
	}
}

// stopAfter is a context that ends after a number of checks.
type stopAfter struct {
	context.Context
	checks int
}

func (c *stopAfter) Err() error {
	c.checks--
	if c.checks < 0 {
		return context.DeadlineExceeded
	}
	return nil
}

func TestSyncInterruptedKeepsOnlyWhatMayStay(t *testing.T) {
	root, project := t.TempDir(), t.TempDir()
	writeSkill(t, root, "a", "a", "")
	writeSkill(t, root, "claimed", "claimed", "")
	for checks := range 40 {
		dir := filepath.Join(t.TempDir(), "session")
		sync(t, Config{Dir: dir, Sources: []string{root}})
		// Since then the project gained its own "claimed".
		native := filepath.Join(project, "skills")
		writeSkill(t, native, "other-folder", "claimed", "")
		writeFile(t, filepath.Join(dir, ".staging-crashed", "skill", "SKILL.md"), "half")
		_, err := Sync(&stopAfter{Context: context.Background(), checks: checks},
			Config{Dir: dir, Layout: AgentsLayout, Sources: []string{root}, Native: []string{native}})
		got := bridged(t, dir)
		for _, name := range got {
			if name != "a" {
				t.Fatalf("after %d checks: %v, want no copy of the project's skill", checks, got)
			}
		}
		if err == nil && len(got) != 1 {
			t.Fatalf("after %d checks: %v", checks, got)
		}
		if len(got) == 0 && err == nil {
			t.Fatal("a finished sync lost the skill")
		}
		if _, statErr := os.Stat(filepath.Join(dir, ".staging-crashed")); !os.IsNotExist(statErr) {
			t.Fatal("a crashed sync's leftovers were kept")
		}
		again := sync(t, Config{Dir: dir, Sources: []string{root}, Native: []string{native}})
		if len(got) == 1 && again.Copied != 0 {
			t.Fatalf("after %d checks the earlier copy was forgotten: %+v", checks, again)
		}
	}
}

func TestSyncInterruptedKeepsOneCopyOfEachName(t *testing.T) {
	preferred, other := t.TempDir(), t.TempDir()
	writeSkill(t, other, "old-folder", "shared", "old")
	for checks := range 40 {
		dir := filepath.Join(t.TempDir(), "session")
		sync(t, Config{Dir: dir, Sources: []string{other}})
		// The skill now comes from a preferred root, under another folder.
		writeSkill(t, preferred, "a-new-folder", "shared", "new")
		_, _ = Sync(&stopAfter{Context: context.Background(), checks: checks},
			Config{Dir: dir, Layout: AgentsLayout, Sources: []string{preferred, other}})
		if got := bridged(t, dir); len(got) > 1 {
			t.Fatalf("after %d checks: %v, want one folder for the skill", checks, got)
		}
	}
}

func TestSyncInterruptedStaysWithinTheTotalLimit(t *testing.T) {
	before, after := t.TempDir(), t.TempDir()
	body := strings.Repeat("x", 600)
	writeSkill(t, before, "b", "b", body)
	writeSkill(t, before, "c", "c", body)
	writeSkill(t, after, "a", "a", body)
	limits := Limits{SkillBytes: 2048, SkillFiles: 10, TotalBytes: 1500}
	for checks := range 40 {
		dir := filepath.Join(t.TempDir(), "session")
		sync(t, Config{Dir: dir, Sources: []string{before}, Limits: limits})
		_, _ = Sync(&stopAfter{Context: context.Background(), checks: checks},
			Config{Dir: dir, Layout: AgentsLayout, Sources: []string{after, before}, Limits: limits})
		if got := bridged(t, dir); len(got) > 2 {
			t.Fatalf("after %d checks: %v, over the total limit", checks, got)
		}
	}
}

func TestSyncRemovesWhatTheAgentAdded(t *testing.T) {
	root, dir := t.TempDir(), filepath.Join(t.TempDir(), "session")
	writeSkill(t, root, "a", "a", "")
	cfg := Config{Dir: dir, Layout: ClaudeLayout, Sources: []string{root}}
	sync(t, cfg)
	writeFile(t, filepath.Join(dir, ".claude", "settings.json"), `{"hooks":{}}`)
	writeFile(t, filepath.Join(dir, "CLAUDE.md"), "obey")
	writeSkill(t, filepath.Join(dir, ".claude", "skills"), "planted", "planted", "")
	sync(t, cfg)
	for _, planted := range []string{"CLAUDE.md", filepath.Join(".claude", "settings.json"), filepath.Join(".claude", "skills", "planted")} {
		if _, err := os.Lstat(filepath.Join(dir, planted)); !os.IsNotExist(err) {
			t.Errorf("%s was kept", planted)
		}
	}
	if got := readFile(t, filepath.Join(dir, ".claude", "skills", "a", "SKILL.md")); got == "" {
		t.Fatal("the skill is gone")
	}
}

func TestSyncRefusesAnUnknownLayout(t *testing.T) {
	if _, err := Sync(context.Background(), Config{Dir: filepath.Join(t.TempDir(), "s"), Layout: "../skills"}); err == nil {
		t.Fatal("an unknown layout was accepted")
	}
}

func TestMissingListsWhatTheSessionLacksWithoutCopying(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	roots := UserRoots(home)
	writeSkill(t, roots[0], "shared", "shared", "agents")
	writeSkill(t, roots[1], "shared", "shared", "claude")
	writeSkill(t, roots[1], "mine", "mine", "do it")
	writeSkill(t, roots[2], "theirs", "theirs", "")
	writeFile(t, filepath.Join(roots[1], "broken", "SKILL.md"), "no header")
	writeSkill(t, project, "theirs", "theirs", "")
	skills, err := Missing(context.Background(), roots, []string{project})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, skill := range skills {
		names = append(names, skill.Name)
	}
	if !reflect.DeepEqual(names, []string{"shared", "mine"}) {
		t.Fatalf("missing = %v", names)
	}

	data := t.TempDir()
	if err := WriteIndex(data, "s-1", skills); err != nil {
		t.Fatal(err)
	}
	if got := ReadIndex(data, "s-1"); !reflect.DeepEqual(got, skills) {
		t.Fatalf("index = %+v, want %+v", got, skills)
	}
	document := readFile(t, IndexDocument(data, "s-1"))
	if !strings.Contains(document, "## mine\n\nDoes mine.\n\nFolder: `"+filepath.ToSlash(skills[1].Path)+"`") {
		t.Fatalf("document = %s", document)
	}
	if body, whole := skills[1].Instructions(); !whole || body != "do it" {
		t.Fatalf("instructions = %q, %v", body, whole)
	}
	if err := Remove(data, "s-1"); err != nil || ReadIndex(data, "s-1") != nil {
		t.Fatalf("index outlived its session: %v", err)
	}
	if err := WriteIndex(data, "s-1", nil); err != nil || ReadIndex(data, "s-1") != nil {
		t.Fatalf("an empty index was kept: %v", err)
	}
}

func TestInstructionsTooLongToHandOver(t *testing.T) {
	dir := writeSkill(t, t.TempDir(), "long", "long", strings.Repeat("x", instructionsLimit))
	if _, whole := (Skill{Path: dir}).Instructions(); whole {
		t.Fatal("a long skill was handed over whole")
	}
	if !(Skill{Path: dir}).Exists() || (Skill{Path: t.TempDir()}).Exists() {
		t.Fatal("Exists does not follow the skill file")
	}
}

// The agent can write in its own folder. Links it leaves there must not lead
// the next sync to the user's skills.
func TestSyncNeverFollowsLinksTheAgentLeft(t *testing.T) {
	for _, at := range []string{".agents", filepath.Join(".agents", "skills"), filepath.Join(".agents", "skills", "a")} {
		t.Run(filepath.ToSlash(at), func(t *testing.T) {
			root, home, dir := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "session")
			writeSkill(t, root, "a", "a", "")
			user := filepath.Join(home, ".claude")
			mine := writeSkill(t, filepath.Join(user, "skills"), "mine", "mine", "")
			cfg := Config{Dir: dir, Sources: []string{root}}
			sync(t, cfg)
			if err := os.RemoveAll(filepath.Join(dir, at)); err != nil {
				t.Fatal(err)
			}
			target := user
			if at != ".agents" {
				target = filepath.Join(user, "skills")
			}
			symlink(t, target, filepath.Join(dir, at))
			result := sync(t, cfg)
			if _, err := os.Stat(filepath.Join(mine, "SKILL.md")); err != nil {
				t.Fatalf("the user's skill was touched: %v", err)
			}
			entries, err := os.ReadDir(filepath.Join(user, "skills"))
			if err != nil || len(entries) != 1 {
				t.Fatalf("the user's folder holds %v, %v, want only their skill", entries, err)
			}
			if !reflect.DeepEqual(result.Skills, []string{"a"}) || !reflect.DeepEqual(bridged(t, dir), []string{"a"}) {
				t.Fatalf("skills = %v, on disk %v", result.Skills, bridged(t, dir))
			}
		})
	}
}

func TestSyncSkipsASkillWhoseSkillFileLinksElsewhere(t *testing.T) {
	root, other, dir := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "session")
	writeSkill(t, other, "x", "x", "")
	writeFile(t, filepath.Join(root, "x", "notes.md"), "n")
	symlink(t, filepath.Join(other, "x", "SKILL.md"), filepath.Join(root, "x", "SKILL.md"))
	writeSkill(t, root, "y", "y", "")
	result := sync(t, Config{Dir: dir, Sources: []string{root}})
	if !reflect.DeepEqual(result.Skills, []string{"y"}) || len(result.Skipped) != 1 {
		t.Fatalf("result = %+v, want x skipped", result)
	}
}

func TestProjectRootsReachTheRepositoryRootAndNoFurther(t *testing.T) {
	outer := t.TempDir()
	repo := filepath.Join(outer, "repo")
	nested := filepath.Join(repo, "services", "api")
	writeFile(t, filepath.Join(repo, ".git"), "gitdir: elsewhere")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	var folders []string
	for _, root := range ProjectRoots(nested) {
		if filepath.Base(filepath.Dir(root)) == ".agents" {
			folders = append(folders, filepath.Dir(filepath.Dir(root)))
		}
	}
	if want := []string{nested, filepath.Join(repo, "services"), repo}; !reflect.DeepEqual(folders, want) {
		t.Fatalf("folders = %v, want %v", folders, want)
	}
	if got := ProjectRoots(outer); len(got) != 4 {
		t.Fatalf("outside a repository: %v, want only the folder itself", got)
	}
}

func TestAttachedOnlyOnceThereAreSkillsAndRemoveDeletesThem(t *testing.T) {
	data, root := t.TempDir(), t.TempDir()
	dir := SessionDir(data, "agy", "s-1")
	if Attached(data, "agy", "s-1") != "" {
		t.Fatal("attached before a sync")
	}
	sync(t, Config{Dir: dir, Sources: []string{root}})
	if _, err := os.Stat(dir); !os.IsNotExist(err) || Attached(data, "agy", "s-1") != "" {
		t.Fatal("a session with no skills to give kept a folder")
	}
	writeSkill(t, root, "a", "a", "")
	sync(t, Config{Dir: dir, Sources: []string{root}})
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil || Attached(data, "agy", "s-1") != resolved {
		t.Fatalf("attached = %q, want %q", Attached(data, "agy", "s-1"), resolved)
	}
	if err := Remove(data, "s-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("the folder outlived its session")
	}
	if err := Remove(t.TempDir(), "s-1"); err != nil {
		t.Fatalf("nothing to remove: %v", err)
	}
}

func TestSessionDirRefusesIdsThatLeaveTheFolder(t *testing.T) {
	for _, id := range []string{"", ".", "..", "../x", "a/b", `a\b`} {
		if got := SessionDir("/data", "agy", id); got != "" {
			t.Errorf("SessionDir(%q) = %q", id, got)
		}
	}
}

func TestDeclaredRoots(t *testing.T) {
	base, home := t.TempDir(), t.TempDir()
	path := filepath.Join(base, "skills.json")
	abs := filepath.Join(t.TempDir(), "abs")
	writeFile(t, path, `{"entries":[{"path":"rel/skills"},{"path":"~/mine"},{"path":`+quote(abs)+`},{"path":" "}]}`)
	got := DeclaredRoots(path, base, home)
	want := []string{filepath.Join(base, "rel", "skills"), filepath.Join(home, "mine"), abs}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("roots = %v, want %v", got, want)
	}
	if DeclaredRoots(filepath.Join(base, "missing.json"), base, home) != nil {
		t.Fatal("a missing file declared roots")
	}
}

func quote(path string) string { return `"` + strings.ReplaceAll(path, `\`, `\\`) + `"` }
