package litmus

import (
	"bytes"
	"cmp"
	_ "embed" // the default recipe
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

//go:embed recipes/ao-recipe.yaml
var defaultRecipe []byte

// DefaultRepo is the repo the embedded recipe describes; it applies when no
// recipe has been stored for that repo.
const DefaultRepo = "orchestratorinc/agent-orchestrator"

// Recipe says how to install, build, run and reach a repo's target. Strings in
// run args and seed commands see the leg's environment ($LITMUS_ROOT, $AO_PORT, ...).
type Recipe struct {
	Kind      string            `json:"kind" yaml:"kind"` // electron | web | cli | api
	Repo      string            `json:"repo,omitempty" yaml:"repo,omitempty"`
	Toolchain Toolchain         `json:"toolchain" yaml:"toolchain"`
	Install   Step              `json:"install" yaml:"install"`
	Build     Step              `json:"build" yaml:"build"`
	Run       Step              `json:"run" yaml:"run"`
	Ready     Ready             `json:"ready" yaml:"ready"`
	Seed      []SeedStep        `json:"seed,omitempty" yaml:"seed,omitempty"`
	Env       map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	Isolation bool              `json:"isolation" yaml:"isolation"`
	Timings   []string          `json:"timings,omitempty" yaml:"-"` // filled by `recipe validate --sha`
}

// Toolchain pins what the target needs on PATH.
type Toolchain struct {
	Node string `json:"node,omitempty" yaml:"node,omitempty"` // major version, e.g. "24"
}

// Step is a command run in Dir (relative to the clone). With Args it is
// executed directly, otherwise Cmd goes through sh -c. CacheKey lists the files
// whose contents decide whether a cached install is still valid.
type Step struct {
	Dir      string   `json:"dir,omitempty" yaml:"dir,omitempty"`
	Cmd      string   `json:"cmd,omitempty" yaml:"cmd,omitempty"`
	Args     []string `json:"args,omitempty" yaml:"args,omitempty"`
	CacheKey []string `json:"cacheKey,omitempty" yaml:"cacheKey,omitempty"`
}

// Ready says when a launched target counts as up.
type Ready struct {
	DaemonPath  string `json:"daemonPath,omitempty" yaml:"daemonPath,omitempty"`   // on the daemon port
	DebugPath   string `json:"debugPath,omitempty" yaml:"debugPath,omitempty"`     // on the remote-debugging port
	PageTargets int    `json:"pageTargets,omitempty" yaml:"pageTargets,omitempty"` // exact count of page targets
}

// SeedStep is a shell command run once the target is ready. WaitFor, when set,
// is a shell command retried until it exits 0.
type SeedStep struct {
	Cmd     string `json:"cmd" yaml:"cmd"`
	WaitFor string `json:"waitFor,omitempty" yaml:"waitFor,omitempty"`
}

var nodeMajor = regexp.MustCompile(`^\d{1,3}$`)

// ParseRecipe decodes and validates recipe YAML; unknown fields are errors.
func ParseRecipe(data []byte) (Recipe, error) {
	var r Recipe
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil {
		if errors.Is(err, io.EOF) {
			err = errors.New("empty file")
		}
		return Recipe{}, fmt.Errorf("recipe: %w", err)
	}
	r.Ready.DaemonPath = cmp.Or(r.Ready.DaemonPath, "/readyz")
	r.Ready.DebugPath = cmp.Or(r.Ready.DebugPath, "/json/version")
	r.Ready.PageTargets = cmp.Or(r.Ready.PageTargets, 1)
	for _, c := range []struct {
		ok  bool
		msg string
	}{
		{slices.Contains([]string{"electron", "web", "cli", "api"}, r.Kind), "kind must be electron, web, cli or api"},
		{r.Run.Cmd != "", "run.cmd is required"},
		{r.Toolchain.Node == "" || nodeMajor.MatchString(r.Toolchain.Node), "toolchain.node must be a major version number"},
		{r.Install.Cmd == "" || len(r.Install.CacheKey) > 0, "install.cacheKey is required with install.cmd"},
		{r.Ready.PageTargets > 0, "ready.pageTargets must not be negative"},
		{slices.IndexFunc(append([]string{r.Install.Dir, r.Build.Dir, r.Run.Dir}, r.Install.CacheKey...), func(p string) bool { return p != "" && !filepath.IsLocal(p) }) < 0,
			"dirs and cacheKey entries must be relative paths inside the repo"},
	} {
		if !c.ok {
			return Recipe{}, errors.New("recipe: " + c.msg)
		}
	}
	return r, nil
}

// recipePath is where a repo's stored recipe lives: <dataDir>/litmus/recipes/<owner>__<name>.yaml.
func (s *Store) recipePath(repo string) string {
	return filepath.Join(filepath.Dir(s.root), "recipes", strings.ReplaceAll(repo, "/", "__")+".yaml")
}

// LoadRecipe returns the stored recipe for repo, or the embedded default for DefaultRepo.
func (s *Store) LoadRecipe(repo string) (Recipe, error) {
	path := s.recipePath(repo)
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
	case errors.Is(err, fs.ErrNotExist) && strings.EqualFold(repo, DefaultRepo):
		data = defaultRecipe
	case errors.Is(err, fs.ErrNotExist):
		return Recipe{}, apierr.NotFound("TESTING_RECIPE_MISSING", "no recipe for "+repo+"; save one at "+path)
	default:
		return Recipe{}, err
	}
	r, err := ParseRecipe(data)
	if err != nil {
		return Recipe{}, apierr.Invalid("TESTING_RECIPE_INVALID", err.Error(), nil)
	}
	return r, nil
}

// RecipeRef joins a recipe path and a SHA into the single string the validate
// endpoint carries; SplitRecipeRef undoes it. Only a trailing @<7-40 hex> counts as a SHA.
func RecipeRef(path, sha string) (string, error) {
	switch {
	case sha == "":
		return path, nil
	case !shaPattern.MatchString(sha):
		return "", fmt.Errorf("--sha %q is not a commit SHA (7-40 lowercase hex)", sha)
	}
	return path + "@" + sha, nil
}

var refSuffix = regexp.MustCompile(`@([0-9a-f]{7,40})$`)

// SplitRecipeRef splits "path@sha" into its parts; without a SHA suffix sha is "".
func SplitRecipeRef(ref string) (path, sha string) {
	if m := refSuffix.FindStringSubmatch(ref); m != nil {
		return strings.TrimSuffix(ref, m[0]), m[1]
	}
	return ref, ""
}
