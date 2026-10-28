package litmus

import (
	"bytes"
	"cmp"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

//go:embed recipes/*.yaml
var recipeFiles embed.FS

// Recipe says how to install, build, run and reach a repo's target. Strings in
// run args and seed commands see the leg's environment ($LITMUS_ROOT, ...).
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
	Isolation Isolation         `json:"isolation" yaml:"isolation"`
	Notes     string            `json:"notes,omitempty" yaml:"notes,omitempty"`
	Timings   []string          `json:"timings,omitempty" yaml:"-"` // filled by `recipe validate --sha`
}

// Isolation supplies launch-only settings. A nil Home defaults to private for Electron.
type Isolation struct {
	Env  map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	Home *bool             `json:"home,omitempty" yaml:"home,omitempty"`
	Dirs []string          `json:"dirs,omitempty" yaml:"dirs,omitempty"` // expanded paths strictly inside the leg dir
}

func (r Recipe) privateHome() bool {
	if r.Isolation.Home != nil {
		return *r.Isolation.Home
	}
	return r.Kind == "electron"
}

var isolationVariables = []string{"LITMUS_ROOT", "LITMUS_LEG", "LITMUS_LEG_DIR", "LITMUS_RUN_ID", "LITMUS_RUN_NUM", "LITMUS_APP_PORT", "LITMUS_DEBUG_PORT", "LITMUS_HOME"}

func (r Recipe) expandIsolation(value string, lookup func(string) string) (string, error) {
	var err error
	value = os.Expand(value, func(key string) string {
		if !slices.Contains(isolationVariables, key) {
			err = fmt.Errorf("unknown expansion $%s in isolation", key)
			return ""
		}
		if key == "LITMUS_HOME" && !r.privateHome() {
			err = errors.New("$LITMUS_HOME requires isolation.home: true")
			return ""
		}
		return lookup(key)
	})
	return value, err
}

func referencesVariable(value string, keys ...string) bool {
	found := false
	os.Expand(value, func(key string) string {
		found = found || slices.Contains(keys, key)
		return ""
	})
	return found
}

func (r Recipe) isolationDirs(lookup func(string) string) ([]string, error) {
	var dirs []string
	for _, raw := range r.Isolation.Dirs {
		path, err := r.expandIsolation(raw, lookup)
		if err != nil {
			return nil, fmt.Errorf("isolation.dirs: %w", err)
		}
		rel, err := filepath.Rel(lookup("LITMUS_LEG_DIR"), path)
		if err != nil || !filepath.IsAbs(path) || !filepath.IsLocal(rel) || rel == "." {
			return nil, fmt.Errorf("isolation.dirs path %q must be inside $LITMUS_LEG_DIR", raw)
		}
		dirs = append(dirs, filepath.Clean(path))
	}
	return dirs, nil
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
	validationDir := filepath.Join(string(filepath.Separator), "litmus-validation", "leg")
	validationLookup := func(key string) string {
		switch key {
		case "LITMUS_LEG_DIR":
			return validationDir
		case "LITMUS_HOME":
			return filepath.Join(validationDir, "home")
		default:
			return "value"
		}
	}
	for _, key := range slices.Sorted(maps.Keys(r.Isolation.Env)) {
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.HasPrefix(key, "LITMUS_") {
			return Recipe{}, fmt.Errorf("recipe: invalid isolation.env key %q", key)
		}
		if slices.Contains([]string{"TMPDIR", "TMP", "TEMP"}, key) {
			return Recipe{}, fmt.Errorf("recipe: isolation.env key %q is managed by Litmus", key)
		}
		if r.privateHome() {
			_, managed := privateHomeEnv("")[key]
			if managed || strings.HasPrefix(key, "XDG_") {
				return Recipe{}, fmt.Errorf("recipe: isolation.env key %q is managed by isolation.home", key)
			}
		}
		if _, err := r.expandIsolation(r.Isolation.Env[key], validationLookup); err != nil {
			return Recipe{}, fmt.Errorf("recipe: %s: %w", key, err)
		}
	}
	if _, err := r.isolationDirs(validationLookup); err != nil {
		return Recipe{}, fmt.Errorf("recipe: %w", err)
	}
	if !r.privateHome() {
		values := append([]string{r.Install.Cmd, r.Build.Cmd, r.Run.Cmd}, r.Run.Args...)
		for _, value := range r.Env {
			values = append(values, value)
		}
		for _, seed := range r.Seed {
			values = append(values, seed.Cmd, seed.WaitFor)
		}
		for _, value := range values {
			if referencesVariable(value, "LITMUS_HOME") {
				return Recipe{}, errors.New("recipe: $LITMUS_HOME requires isolation.home: true")
			}
		}
	}
	return r, nil
}

// recipePath is where a repo's stored recipe lives: <dataDir>/litmus/recipes/<owner>__<name>.yaml.
func (s *Store) recipePath(repo string) string {
	return filepath.Join(filepath.Dir(s.root), "recipes", strings.ReplaceAll(repo, "/", "__")+".yaml")
}

// LoadRecipe returns the stored recipe, or an embedded recipe naming this repo.
func (s *Store) LoadRecipe(repo string) (Recipe, error) {
	text, err := s.recipeText(repo)
	if err != nil {
		return Recipe{}, err
	}
	r, err := ParseRecipe([]byte(text))
	if err != nil {
		return Recipe{}, apierr.Invalid("TESTING_RECIPE_INVALID", err.Error(), nil)
	}
	return r, nil
}

// recipeText selects the same source for preparation and the run's context files.
func (s *Store) recipeText(repo string) (string, error) {
	text, err := s.storedRecipeText(repo)
	if !isCode(err, "TESTING_RECIPE_MISSING") {
		return text, err
	}
	files, ferr := recipeFiles.ReadDir("recipes")
	if ferr != nil {
		return "", ferr
	}
	for _, file := range files {
		data, ferr := recipeFiles.ReadFile("recipes/" + file.Name())
		if ferr != nil {
			return "", ferr
		}
		r, ferr := ParseRecipe(data)
		if ferr != nil {
			return "", ferr
		}
		if r.Repo != "" && strings.EqualFold(repo, r.Repo) {
			return string(data), nil
		}
	}
	return "", err
}

func (s *Store) storedRecipeText(repo string) (string, error) {
	path := s.recipePath(repo)
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
	case errors.Is(err, fs.ErrNotExist):
		return "", apierr.NotFound("TESTING_RECIPE_MISSING", "no recipe for "+repo+"; save one at "+path)
	default:
		return "", errors.Join(apierr.Internal("TESTING_RECIPE_UNREADABLE", "read stored recipe: "+err.Error()), err)
	}
	return string(data), nil
}

// RecipeValidateRequest selects the project's stored recipe when Path is nil.
// SHA is optional; without it validation only parses the recipe.
type RecipeValidateRequest struct {
	Path      *string `json:"path,omitempty"`
	SHA       string  `json:"sha,omitempty"`
	ProjectID string  `json:"projectId,omitempty"`
}

// Validate rejects empty file paths and malformed commit SHAs before any I/O.
func (r RecipeValidateRequest) Validate() error {
	if r.Path != nil && strings.TrimSpace(*r.Path) == "" {
		return invalid("TESTING_RECIPE_INVALID", "recipe file must not be empty")
	}
	if r.SHA != "" && !shaPattern.MatchString(r.SHA) {
		return invalid("TESTING_RECIPE_INVALID", fmt.Sprintf("--sha %q is not a commit SHA (7-40 lowercase hex)", r.SHA))
	}
	return nil
}
