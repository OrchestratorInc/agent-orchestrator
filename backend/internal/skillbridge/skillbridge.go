// Package skillbridge gives one session the user's own skills, whichever agent
// runs it.
//
// Each agent looks for SKILL.md folders in its own places, so a skill the user
// wrote for one agent is often invisible to another. Sync copies the skills an
// agent misses into a folder Orchestrator owns, and the agent's adapter adds
// that folder to the one session. WriteIndex lists them for the agents that
// cannot take a folder, and for the chat's `/` menu. Nothing is written to the
// user's agent config or skill folders, so agents behave the same outside
// Orchestrator.
//
// The folder holds real copies: agy asks for approval before it reads a file
// whose resolved path is outside its workspace, which a symlink's would be.
package skillbridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	rootName     = "skill-bridge"
	manifestName = ".orchestrator-skill-bridge.json"
	skillFile    = "SKILL.md"

	// skillFileLimit bounds how much of a SKILL.md is read for its frontmatter.
	skillFileLimit = 256 << 10
)

// Limits bound what one session's folder may hold.
type Limits struct {
	SkillBytes int64
	SkillFiles int
	TotalBytes int64
}

// Layouts are where an agent looks for skills inside a directory added to its
// session.
const (
	AgentsLayout = ".agents/skills"
	ClaudeLayout = ".claude/skills"
)

// DefaultLimits leave out a skill that carries a build or a dataset (one real
// skill was 385 MB) while keeping ordinary ones whole.
var DefaultLimits = Limits{SkillBytes: 32 << 20, SkillFiles: 4000, TotalBytes: 256 << 20}

// Config describes one session's folder.
type Config struct {
	// Dir is the session's folder, from SessionDir.
	Dir string
	// Layout is the skills folder inside Dir: AgentsLayout or ClaudeLayout.
	Layout string
	// Sources are folders of skills to copy from, the most preferred first.
	Sources []string
	// Native are folders of skills the session already has. agy prefers a skill
	// from an added folder to a project or global one of the same name, so a
	// name found here is left out rather than overridden.
	Native []string
	// Limits defaults to DefaultLimits.
	Limits Limits
}

// Skipped is a skill left out, and why.
type Skipped struct {
	Path   string
	Reason string
}

// Result reports one Sync.
type Result struct {
	// Skills are the folder names the session now has.
	Skills []string
	// Copied counts those copied by this Sync; the rest were already current.
	Copied  int
	Skipped []Skipped
}

// manifest marks the folder as Orchestrator's and records what each copy was
// made from, so an unchanged skill is not copied again when a session resumes.
type manifest struct {
	Owner  string            `json:"owner"`
	Skills map[string]string `json:"skills"`
}

// SessionDir is where a session's skills go, or "" for an unusable id.
func SessionDir(dataDir, agent, sessionID string) string {
	if strings.TrimSpace(dataDir) == "" || !singleSegment(agent) || !singleSegment(sessionID) {
		return ""
	}
	return filepath.Join(dataDir, rootName, agent, sessionID)
}

// Attached returns the session's folder when it has been prepared, with
// symlinks resolved so the agent's own path check matches it, or "".
func Attached(dataDir, agent, sessionID string) string {
	dir := SessionDir(dataDir, agent, sessionID)
	if dir == "" {
		return ""
	}
	if info, err := os.Stat(filepath.Join(dir, manifestName)); err != nil || !info.Mode().IsRegular() {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return ""
	}
	return resolved
}

// Remove deletes the session's folder for every agent.
func Remove(dataDir, sessionID string) error {
	if strings.TrimSpace(dataDir) == "" || !singleSegment(sessionID) {
		return nil
	}
	agents, err := os.ReadDir(filepath.Join(dataDir, rootName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, agent := range agents {
		errs = append(errs, os.RemoveAll(filepath.Join(dataDir, rootName, agent.Name(), sessionID)))
	}
	return errors.Join(errs...)
}

// UserRoots are the folders users keep skills in, the most preferred first.
func UserRoots(home string) []string {
	if strings.TrimSpace(home) == "" {
		return nil
	}
	return []string{
		filepath.Join(home, ".agents", "skills"),
		filepath.Join(home, ".claude", "skills"),
		filepath.Join(home, ".codex", "skills"),
	}
}

// ProjectRoots are the folders a project keeps its own skills in, in dir and,
// for a dir inside a repository, in each folder up to the repository's root. A
// project skill wins over a user skill of the same name, whichever agent it
// was written for.
func ProjectRoots(dir string) []string {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	var roots []string
	for _, folder := range repositoryFolders(filepath.Clean(dir)) {
		roots = append(roots,
			filepath.Join(folder, ".agents", "skills"),
			filepath.Join(folder, ".agent", "skills"),
			filepath.Join(folder, ".claude", "skills"),
			filepath.Join(folder, ".codex", "skills"),
		)
	}
	return roots
}

// repositoryFolders is dir and its parents up to the repository root, or dir
// alone outside a repository: the user's own folders are not a project's.
func repositoryFolders(dir string) []string {
	folders := []string{dir}
	for current := dir; ; {
		if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
			return folders
		}
		parent := filepath.Dir(current)
		if parent == current {
			return []string{dir}
		}
		current = parent
		folders = append(folders, current)
	}
}

// Sync makes cfg.Dir hold the source skills the session does not already have,
// as <dir>/.agents/skills/<folder>. Skills that are gone from the sources, or
// that the session now has another way, are removed. A skill that cannot be
// copied is skipped. With no skills to give, the folder is removed.
func Sync(ctx context.Context, cfg Config) (Result, error) {
	if cfg.Dir == "" || !filepath.IsAbs(cfg.Dir) {
		return Result{}, fmt.Errorf("skill bridge folder must be absolute, got %q", cfg.Dir)
	}
	if cfg.Limits == (Limits{}) {
		cfg.Limits = DefaultLimits
	}
	if cfg.Layout != AgentsLayout && cfg.Layout != ClaudeLayout {
		return Result{}, fmt.Errorf("skill bridge layout %q is not supported", cfg.Layout)
	}
	skillsPath := filepath.FromSlash(cfg.Layout)
	root, err := openFolder(cfg.Dir, skillsPath)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = root.Close() }()
	previous := readManifest(cfg.Dir)
	if err := removeForeign(root, skillsPath); err != nil {
		return Result{}, err
	}
	next := manifest{Owner: "orchestrator", Skills: map[string]string{}}
	var result Result
	var total int64
	taken, err := nativeNames(ctx, cfg.Native)
	if err != nil {
		// Which names the session already has is unknown, and a copy of one of
		// them would override it. Leave the session without copies this time.
		_ = root.Close()
		return result, errors.Join(err, os.RemoveAll(cfg.Dir))
	}
	native := maps.Clone(taken)
	stop := func(cause error) (Result, error) {
		return result, errors.Join(cause, keepEarlier(root, skillsPath, native, previous, next))
	}
	candidates, err := listSkills(ctx, cfg.Sources)
	if err != nil {
		return stop(err)
	}
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return stop(err)
		}
		skip := func(reason string) {
			result.Skipped = append(result.Skipped, Skipped{Path: candidate.path, Reason: reason})
		}
		if _, duplicate := seen[candidate.real]; duplicate {
			continue
		}
		seen[candidate.real] = struct{}{}
		if candidate.err != nil {
			skip(candidate.err.Error())
			continue
		}
		if taken.has(candidate) {
			continue
		}
		contents, err := scanSkill(ctx, candidate.real, cfg.Limits)
		if err != nil {
			if ctx.Err() != nil {
				return stop(ctx.Err())
			}
			skip(err.Error())
			continue
		}
		if total+contents.size > cfg.Limits.TotalBytes {
			skip("the session's skill folder is full")
			continue
		}
		target := filepath.Join(skillsPath, candidate.folder)
		if previous.Skills[candidate.folder] != contents.fingerprint || !isDir(root, target) {
			if err := replaceSkill(ctx, contents, root, target); err != nil {
				if ctx.Err() != nil {
					return stop(ctx.Err())
				}
				skip(err.Error())
				continue
			}
			result.Copied++
		}
		total += contents.size
		taken.add(candidate)
		next.Skills[candidate.folder] = contents.fingerprint
		result.Skills = append(result.Skills, candidate.folder)
	}
	if len(next.Skills) == 0 {
		_ = root.Close()
		return result, os.RemoveAll(cfg.Dir)
	}
	if err := removeStale(root, skillsPath, next); err != nil {
		return result, err
	}
	return result, writeManifest(cfg.Dir, next)
}

// openFolder opens the session's folder with its skills directory in place.
// The agent can write here, so a link it left in place of a directory is
// removed, and everything is replaced and deleted through an os.Root, which
// refuses to leave the folder. Following such a link would delete what it
// points at: the user's own skills, say.
func openFolder(dir, skillsPath string) (*os.Root, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	for _, path := range []string{filepath.Dir(skillsPath), skillsPath} {
		if !isDir(root, path) {
			err = root.Remove(path)
			if err == nil || errors.Is(err, fs.ErrNotExist) {
				err = root.Mkdir(path, 0o700)
			}
		}
		if err != nil {
			_ = root.Close()
			return nil, err
		}
	}
	return root, nil
}

// removeForeign clears what is not Orchestrator's: what a Sync cut short by a
// crash left half written, and anything the agent added. Agents read more than
// skills from some folders, such as settings beside .claude/skills.
func removeForeign(root *os.Root, skillsPath string) error {
	top, skills := filepath.Split(skillsPath)
	top = filepath.Clean(top)
	var errs []error
	for dir, keep := range map[string][]string{".": {top, manifestName}, top: {skills}} {
		entries, err := fs.ReadDir(root.FS(), filepath.ToSlash(dir))
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !slices.Contains(keep, entry.Name()) {
				errs = append(errs, root.RemoveAll(filepath.Join(dir, entry.Name())))
			}
		}
	}
	return errors.Join(errs...)
}

// keepEarlier ends an interrupted Sync: the session keeps what this one copied
// and what earlier ones left, except a copy of a skill it now has another way,
// and anything unknown.
func keepEarlier(root *os.Root, skillsPath string, native nameSet, previous, next manifest) error {
	for folder, fingerprint := range previous.Skills {
		if _, replaced := next.Skills[folder]; replaced {
			continue
		}
		earlier, ok := readSkill(filepath.Join(root.Name(), skillsPath, folder))
		if ok && !native.has(earlier) {
			next.Skills[folder] = fingerprint
		}
	}
	if err := removeStale(root, skillsPath, next); err != nil {
		return err
	}
	return writeManifest(root.Name(), next)
}

type skill struct {
	folder      string
	name        string
	description string
	path        string
	real        string
	err         error
}

// nameSet matches skills by folder and by name, ignoring case: agents key
// skills on either, and some filesystems do not distinguish case.
type nameSet map[string]struct{}

func (s nameSet) has(skill skill) bool {
	_, folder := s[strings.ToLower(skill.folder)]
	_, name := s[strings.ToLower(skill.name)]
	return folder || (skill.name != "" && name)
}

func (s nameSet) add(skill skill) {
	s[strings.ToLower(skill.folder)] = struct{}{}
	if skill.name != "" {
		s[strings.ToLower(skill.name)] = struct{}{}
	}
}

func nativeNames(ctx context.Context, roots []string) (nameSet, error) {
	skills, err := listSkills(ctx, roots)
	names := nameSet{}
	for _, skill := range skills {
		names.add(skill)
	}
	return names, err
}

// listSkills finds the skills in each root, or the root itself when it is one.
// Its only error is the context's.
func listSkills(ctx context.Context, roots []string) ([]skill, error) {
	var skills []skill
	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return skills, err
		}
		if found, ok := readSkill(root); ok {
			skills = append(skills, found)
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return skills, err
			}
			// Dot folders hold an agent's own skills (Codex's .system), not the user's.
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			if found, ok := readSkill(filepath.Join(root, entry.Name())); ok {
				skills = append(skills, found)
			}
		}
	}
	return skills, nil
}

func readSkill(path string) (skill, bool) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return skill{}, false
	}
	if info, err := os.Stat(filepath.Join(resolved, skillFile)); err != nil || !info.Mode().IsRegular() {
		return skill{}, false
	}
	found := skill{folder: filepath.Base(path), path: path, real: resolved}
	found.name, found.description, found.err = readFrontmatter(filepath.Join(resolved, skillFile))
	return found, true
}

// readFrontmatter returns a skill's name and description, or why agents would
// not load it.
func readFrontmatter(path string) (name, description string, err error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", errors.New("SKILL.md cannot be read")
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, skillFileLimit))
	if err != nil {
		return "", "", errors.New("SKILL.md cannot be read")
	}
	front, _, ok := splitFrontmatter(data)
	if !ok {
		return "", "", errors.New("SKILL.md has no frontmatter")
	}
	var meta struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if yaml.Unmarshal(front, &meta) != nil {
		return "", "", errors.New("SKILL.md frontmatter is not valid YAML")
	}
	name, description = strings.TrimSpace(meta.Name), strings.TrimSpace(meta.Description)
	if name == "" || description == "" {
		return name, description, errors.New("SKILL.md has no name or description")
	}
	return name, description, nil
}

// splitFrontmatter separates a SKILL.md's YAML header from its instructions.
func splitFrontmatter(data []byte) (front, body []byte, ok bool) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	rest, ok := bytes.CutPrefix(data, []byte("---\n"))
	if !ok {
		return nil, data, false
	}
	front, body, ok = bytes.Cut(rest, []byte("\n---"))
	if !ok {
		return nil, data, false
	}
	// The closing fence's own line ends the header.
	_, body, _ = bytes.Cut(body, []byte("\n"))
	return front, body, true
}

type skillContents struct {
	files       []skillEntry
	size        int64
	fingerprint string
}

type skillEntry struct {
	rel    string
	source string
	mode   fs.FileMode
}

// scanSkill lists a skill's files and fingerprints them by path, size, mode
// and modification time. A symlink is followed only to a file inside the skill,
// so a skill cannot pull other files of the user's into an agent's workspace.
func scanSkill(ctx context.Context, root string, limits Limits) (skillContents, error) {
	var contents skillContents
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%s\n", root)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		source := path
		if entry.Type()&fs.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil || !within(root, resolved) {
				return nil //nolint:nilerr // a link out of the skill is left out
			}
			source = resolved
		}
		info, err := os.Lstat(source)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		contents.size += info.Size()
		contents.files = append(contents.files, skillEntry{rel: rel, source: source, mode: info.Mode().Perm()})
		if contents.size > limits.SkillBytes || len(contents.files) > limits.SkillFiles {
			return fmt.Errorf("the skill is over the limit of %d MB or %d files", limits.SkillBytes>>20, limits.SkillFiles)
		}
		_, _ = fmt.Fprintf(hash, "%q %d %o %d\n", rel, info.Size(), info.Mode().Perm(), info.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		return skillContents{}, err
	}
	if !slices.ContainsFunc(contents.files, func(file skillEntry) bool { return file.rel == skillFile }) {
		return skillContents{}, errors.New("SKILL.md is a link out of the skill")
	}
	contents.fingerprint = hex.EncodeToString(hash.Sum(nil))
	return contents, nil
}

// replaceSkill copies a skill aside, then swaps it in, so the agent never sees
// half of one.
func replaceSkill(ctx context.Context, contents skillContents, root *os.Root, target string) error {
	staging, err := os.MkdirTemp(root.Name(), ".staging-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	for _, file := range contents.files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := copyFile(file, filepath.Join(staging, "skill", file.rel)); err != nil {
			return err
		}
	}
	if err := root.RemoveAll(target); err != nil {
		return err
	}
	return root.Rename(filepath.Join(filepath.Base(staging), "skill"), target)
}

func copyFile(file skillEntry, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	// The owner can always write the copy, so it can be replaced later; the
	// source's execute bits are kept for a skill's scripts.
	mode := file.mode | 0o600
	if cloneFile(file.source, target) == nil {
		return os.Chmod(target, mode)
	}
	in, err := os.Open(file.source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return errors.Join(err, out.Close())
}

func removeStale(root *os.Root, skillsPath string, keep manifest) error {
	entries, err := fs.ReadDir(root.FS(), filepath.ToSlash(skillsPath))
	if err != nil {
		return err
	}
	var errs []error
	for _, entry := range entries {
		if _, kept := keep.Skills[entry.Name()]; !kept {
			errs = append(errs, root.RemoveAll(filepath.Join(skillsPath, entry.Name())))
		}
	}
	return errors.Join(errs...)
}

func readManifest(dir string) manifest {
	var m manifest
	data, err := os.ReadFile(filepath.Join(dir, manifestName)) //nolint:gosec // dir is Orchestrator's own data folder.
	if err != nil || json.Unmarshal(data, &m) != nil {
		return manifest{}
	}
	return m
}

func writeManifest(dir string, m manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, manifestName), append(data, '\n'))
}

func writeAtomic(path string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".write-")
	if err != nil {
		return err
	}
	_, err = temp.Write(data)
	if err = errors.Join(err, temp.Close()); err == nil {
		err = os.Rename(temp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(temp.Name())
	}
	return err
}

func isDir(root *os.Root, path string) bool {
	info, err := root.Lstat(path)
	return err == nil && info.IsDir()
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && filepath.IsLocal(rel)
}

func singleSegment(name string) bool {
	return name != "" && name != "." && filepath.IsLocal(name) && !strings.ContainsAny(name, `/\`)
}

// DeclaredRoots reads the folders a skills.json lists, the file agy takes extra
// skill folders from. Relative paths resolve against base; ~ is home.
func DeclaredRoots(path, base, home string) []string {
	data, err := os.ReadFile(path) //nolint:gosec // a known agent config file, read only.
	if err != nil {
		return nil
	}
	var file struct {
		Entries []struct {
			Path string `json:"path"`
		} `json:"entries"`
	}
	if json.Unmarshal(data, &file) != nil {
		return nil
	}
	var roots []string
	for _, entry := range file.Entries {
		root := strings.TrimSpace(entry.Path)
		switch {
		case root == "":
			continue
		case root == "~" || strings.HasPrefix(root, "~/"):
			root = filepath.Join(home, strings.TrimPrefix(root, "~"))
		case !filepath.IsAbs(root):
			root = filepath.Join(base, root)
		}
		roots = append(roots, root)
	}
	sort.Strings(roots)
	return roots
}
