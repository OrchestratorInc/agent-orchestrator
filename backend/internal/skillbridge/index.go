package skillbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
)

const (
	indexRoot     = "_index"
	indexData     = "skills.json"
	indexDocument = "SKILLS.md"
)

// Skill is one of the user's skills a session's agent does not load itself.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Path is the skill's own folder: an index holds no copies.
	Path string `json:"path"`
}

// Catalog is one listing of a session's skills: the names it already has and
// the source skills. The index and the copies both read it, so a launch lists
// the user's skills once.
type Catalog struct {
	taken      nameSet
	nativeErr  error
	candidates []skill
	sourcesErr error
}

// Scan lists the native names and the source skills. When the native names
// cannot be listed, the sources are not listed either: nothing may be offered
// whose name the session could already have.
func Scan(ctx context.Context, sources, native []string) *Catalog {
	catalog := &Catalog{}
	catalog.taken, catalog.nativeErr = nativeNames(ctx, native)
	if catalog.nativeErr == nil {
		catalog.candidates, catalog.sourcesErr = listSkills(ctx, sources)
	}
	return catalog
}

// Missing lists the source skills the session does not already have.
func Missing(ctx context.Context, sources, native []string) ([]Skill, error) {
	return Scan(ctx, sources, native).Missing()
}

// Missing lists the catalog's source skills the session does not already have.
func (c *Catalog) Missing() ([]Skill, error) {
	if err := errors.Join(c.nativeErr, c.sourcesErr); err != nil {
		return nil, err
	}
	taken := maps.Clone(c.taken)
	var skills []Skill
	seen := map[string]struct{}{}
	for _, candidate := range c.candidates {
		if _, duplicate := seen[candidate.real]; duplicate || candidate.err != nil || taken.has(candidate) {
			continue
		}
		seen[candidate.real] = struct{}{}
		taken.add(candidate)
		skills = append(skills, Skill{Name: candidate.name, Description: candidate.description, Path: candidate.real})
	}
	return skills, nil
}

// IndexDocument is the file an agent reads to find a session's skills.
func IndexDocument(dataDir, sessionID string) string {
	dir := SessionDir(dataDir, indexRoot, sessionID)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, indexDocument)
}

// WriteIndex records the skills a session can be pointed at, or removes the
// record when there are none.
func WriteIndex(dataDir, sessionID string, skills []Skill) error {
	dir := SessionDir(dataDir, indexRoot, sessionID)
	if dir == "" {
		return nil
	}
	if len(skills) == 0 {
		return os.RemoveAll(dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(skills, "", "  ")
	if err != nil {
		return err
	}
	var document strings.Builder
	document.WriteString("# User skills\n\nTo use a skill, read SKILL.md in its folder and follow it.\n")
	for _, skill := range skills {
		fmt.Fprintf(&document, "\n## %s\n\n%s\n\nFolder: `%s`\n",
			oneLine(skill.Name), oneLine(skill.Description), filepath.ToSlash(skill.Path))
	}
	if err := writeAtomic(filepath.Join(dir, indexData), append(data, '\n')); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, indexDocument), []byte(document.String()))
}

// ReadIndex returns the skills a session can be pointed at.
func ReadIndex(dataDir, sessionID string) []Skill {
	dir := SessionDir(dataDir, indexRoot, sessionID)
	if dir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dir, indexData)) //nolint:gosec // dir is Orchestrator's own data folder.
	var skills []Skill
	if err != nil || json.Unmarshal(data, &skills) != nil {
		return nil
	}
	return skills
}

// Instructions reads what a skill tells the agent to do, as it is now. whole is
// false when the skill is too long to hand over and only its folder can be.
func (s Skill) Instructions() (body string, whole bool) {
	file, err := os.Open(filepath.Join(s.Path, skillFile))
	if err != nil {
		return "", false
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, skillFileLimit+1))
	if err != nil || len(data) > instructionsLimit {
		return "", false
	}
	_, text, ok := splitFrontmatter(data)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(string(text)), true
}

// Exists reports whether the skill is still there.
func (s Skill) Exists() bool {
	info, err := os.Stat(filepath.Join(s.Path, skillFile))
	return err == nil && info.Mode().IsRegular()
}

// instructionsLimit bounds what one skill adds to a message.
const instructionsLimit = 64 << 10

func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }
