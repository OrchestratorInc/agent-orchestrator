package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// A cloud project works on one main repository (ao_projects.repository_url)
// plus any number of additional repositories, kept under the project's Config
// as {"repos": [{"url": ..., "branch": ...}]}. Every session clones them all
// side by side, on every sandbox provider, and the agent starts in the main
// repository with access to the others. Projects set up before "repos"
// existed carry them as the coder dev kit's {"coder": {"extraRepos": [...]}},
// which is still read when "repos" is absent.

// MaxProjectAdditionalRepos bounds a project's additional repositories.
const MaxProjectAdditionalRepos = 10

// ProjectAgentStartsInRepository (the default) starts the coding agent in the
// main repository's checkout; ProjectAgentStartsInParent starts it in the
// folder that holds every repository's checkout.
const (
	ProjectAgentStartsInRepository = "repository"
	ProjectAgentStartsInParent     = "parent"
)

// MaxProjectRulesBytes bounds each of a project's agentRules and
// orchestratorRules.
const MaxProjectRulesBytes = 16 << 10

// StandaloneRepositoryHost is the host of the placeholder repository URL a
// standalone project (one with no repository) stores in its repository_url:
// https://scratch.ao.local/<id>. A session of such a project starts in an empty,
// git-initialized folder instead of a checkout.
const StandaloneRepositoryHost = "scratch.ao.local"

// StandaloneProjectSource marks a standalone project's Config: {"source": "standalone"}.
const StandaloneProjectSource = "standalone"

// StandaloneRepositoryURL is the placeholder repository URL for a standalone
// project with the given id.
func StandaloneRepositoryURL(id string) string {
	return "https://" + StandaloneRepositoryHost + "/" + id
}

// IsStandaloneRepositoryURL reports whether raw is a standalone project's
// placeholder repository URL.
func IsStandaloneRepositoryURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && parsed.Scheme == "https" &&
		strings.EqualFold(parsed.Hostname(), StandaloneRepositoryHost) &&
		parsed.Port() == "" && parsed.User == nil
}

// RepoDirName is the folder a repository is checked out into beside the main
// repository, derived from its URL: "https://github.com/owner/ChartSnip" gives
// "ChartSnip".
func RepoDirName(repoURL string) string {
	trimmed := strings.TrimSuffix(strings.Trim(repoURL, "/"), ".git")
	name := trimmed
	if idx := strings.LastIndex(trimmed, "/"); idx >= 0 {
		name = trimmed[idx+1:]
	}
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}, name)
	if name == "" || name == "." || name == ".." {
		return "repo"
	}
	return name
}

// reservedRepoDirNames are folders beside the main checkout that AO itself
// uses: the managed providers' main checkout and worker state.
var reservedRepoDirNames = []string{"repository", ".ao"}

// ProjectAdditionalRepos returns a project's additional repositories: its
// "repos", or the legacy coder dev kit "extraRepos" when "repos" is absent.
func ProjectAdditionalRepos(config json.RawMessage) []RepoRef {
	if len(config) == 0 {
		return nil
	}
	var envelope struct {
		Repos *[]RepoRef `json:"repos"`
	}
	if err := json.Unmarshal(config, &envelope); err == nil && envelope.Repos != nil {
		return nonEmptyRepos(*envelope.Repos)
	}
	if coder, ok := DecodeProjectCoderConfig(config); ok {
		return nonEmptyRepos(coder.ExtraRepos)
	}
	return nil
}

func nonEmptyRepos(repos []RepoRef) []RepoRef {
	kept := make([]RepoRef, 0, len(repos))
	for _, repo := range repos {
		if strings.TrimSpace(repo.URL) != "" {
			kept = append(kept, RepoRef{URL: strings.TrimSpace(repo.URL), Branch: strings.TrimSpace(repo.Branch)})
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

// CanonicalGitHubRepoURL returns https://github.com/<owner>/<name> for a GitHub
// repository URL, and the owner/name pair.
func CanonicalGitHubRepoURL(raw string) (canonical, fullName string, err error) {
	parsed, parseErr := url.Parse(strings.TrimSpace(raw))
	if parseErr != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" ||
		!(strings.EqualFold(parsed.Hostname(), "github.com") || strings.EqualFold(parsed.Hostname(), "www.github.com")) ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", fmt.Errorf("%q is not a GitHub repository URL", raw)
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("%q is not a GitHub repository URL", raw)
	}
	owner, name := parts[0], strings.TrimSuffix(parts[1], ".git")
	if !validGitHubName(owner) || !validGitHubName(name) {
		return "", "", fmt.Errorf("%q is not a GitHub repository URL", raw)
	}
	return "https://github.com/" + owner + "/" + name, owner + "/" + name, nil
}

func validGitHubName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 100 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

// NormalizeProjectRepos validates a project's additional repositories against
// its main repository and returns them canonicalized: each a GitHub repository,
// none the main repository or listed twice, and each checking out into its own
// folder beside the main one. A blank URL is dropped. mainURL may be a
// standalone placeholder, in which case no additional repository is allowed.
func NormalizeProjectRepos(mainURL string, repos []RepoRef) ([]RepoRef, error) {
	kept := nonEmptyRepos(repos)
	if len(kept) == 0 {
		return nil, nil
	}
	if IsStandaloneRepositoryURL(mainURL) {
		return nil, errors.New("a project without a repository cannot have additional repositories")
	}
	if len(kept) > MaxProjectAdditionalRepos {
		return nil, fmt.Errorf("at most %d additional repositories are allowed", MaxProjectAdditionalRepos)
	}
	mainCanonical, _, mainErr := CanonicalGitHubRepoURL(mainURL)
	dirs := map[string]string{}
	for _, reserved := range reservedRepoDirNames {
		dirs[strings.ToLower(reserved)] = reserved
	}
	if mainErr == nil {
		dirs[strings.ToLower(RepoDirName(mainCanonical))] = mainCanonical
	}
	seen := map[string]bool{}
	out := make([]RepoRef, 0, len(kept))
	for _, repo := range kept {
		canonical, _, err := CanonicalGitHubRepoURL(repo.URL)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(canonical)
		if mainErr == nil && key == strings.ToLower(mainCanonical) {
			return nil, fmt.Errorf("%s is already the project's main repository", canonical)
		}
		if seen[key] {
			return nil, fmt.Errorf("%s is listed twice", canonical)
		}
		seen[key] = true
		dir := RepoDirName(canonical)
		if strings.HasPrefix(dir, ".") {
			// A sibling of the main checkout is often in the workspace user's
			// home, where a dot folder (.ssh, .aws, .docker, .claude) is
			// configuration, never a checkout.
			return nil, fmt.Errorf("%s would check out into the folder %q; a repository whose name starts with a dot cannot be an additional repository", canonical, dir)
		}
		if other, taken := dirs[strings.ToLower(dir)]; taken {
			if other == "repository" || other == ".ao" {
				return nil, fmt.Errorf("%s would check out into the folder %q, which AO uses itself", canonical, dir)
			}
			return nil, fmt.Errorf("%s and %s would check out into the same folder %q", other, canonical, dir)
		}
		dirs[strings.ToLower(dir)] = canonical
		if len(repo.Branch) > 255 || strings.ContainsAny(repo.Branch, " \t\r\n~^:?*[\\") {
			return nil, fmt.Errorf("branch %q of %s is not a valid branch name", repo.Branch, canonical)
		}
		out = append(out, RepoRef{URL: canonical, Branch: repo.Branch})
	}
	return out, nil
}

// ProjectScriptStep is one Setup or Cleanup command. Repository, when set, is
// the canonical URL of the project repository the command runs in; Folder,
// when set, is that repository's folder beside the main checkout as read back
// from a stored step. Both empty runs it in the main repository.
//
// A step is stored as a plain string either way (see RepoScriptCommand), so a
// control plane or desktop release that predates per-repository steps reads,
// runs and keeps it. The {command, repository} object form is accepted on
// input only.
type ProjectScriptStep struct {
	Command    string `json:"command"`
	Repository string `json:"repository,omitempty"`
	Folder     string `json:"-"`
}

// UnmarshalJSON accepts a plain command string, including the stored
// RepoScriptCommand form, or {"command", "repository"}.
func (s *ProjectScriptStep) UnmarshalJSON(raw []byte) error {
	var command string
	if err := json.Unmarshal(raw, &command); err == nil {
		if dir, inner, ok := ParseRepoScriptCommand(command); ok {
			*s = ProjectScriptStep{Command: inner, Folder: dir}
			return nil
		}
		*s = ProjectScriptStep{Command: command}
		return nil
	}
	var object struct {
		Command    *string `json:"command"`
		Repository string  `json:"repository"`
	}
	if err := json.Unmarshal(raw, &object); err != nil || object.Command == nil {
		return errors.New("each command must be a string or an object with a command")
	}
	*s = ProjectScriptStep{Command: *object.Command, Repository: strings.TrimSpace(object.Repository)}
	return nil
}

// MarshalJSON writes the stored, plain-string form of a step.
func (s ProjectScriptStep) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.storedForm())
}

func (s ProjectScriptStep) storedForm() string {
	switch {
	case s.Repository != "":
		return RepoScriptCommand(RepoDirName(s.Repository), s.Command)
	case s.Folder != "":
		return RepoScriptCommand(s.Folder, s.Command)
	default:
		return s.Command
	}
}

// ShellCommand is the command the worker runs from the main repository's
// checkout: the command itself, or, for a step in another repository, the
// command run in a subshell from that repository's folder beside the main one.
func (s ProjectScriptStep) ShellCommand(mainURL string) string {
	if s.Repository != "" && strings.EqualFold(s.Repository, mainURL) {
		return s.Command
	}
	return s.storedForm()
}

// RepoScriptCommand wraps a Setup or Cleanup command so it runs in another
// repository's folder beside the main checkout. A project stores a
// per-repository step in this plain-string form, so a control plane or desktop
// release that predates per-repository steps still reads, runs and keeps it;
// ParseRepoScriptCommand recovers the folder and the command for the editor.
func RepoScriptCommand(dir, command string) string {
	return "(\ncd -- '../" + dir + "' || exit 1\n" + command + "\n)"
}

var repoScriptCommandPattern = regexp.MustCompile(`^\(\ncd -- '\.\./([A-Za-z0-9_.-]+)' \|\| exit 1\n([\s\S]*)\n\)$`)

// ParseRepoScriptCommand reports the repository folder and inner command of a
// step written by RepoScriptCommand; ok is false for a main-repository step.
func ParseRepoScriptCommand(stored string) (dir, command string, ok bool) {
	match := repoScriptCommandPattern.FindStringSubmatch(stored)
	if match == nil {
		return "", stored, false
	}
	return match[1], match[2], true
}

// ProjectAgentStartsIn returns where a project's coding agent starts:
// ProjectAgentStartsInRepository (the default) or ProjectAgentStartsInParent.
func ProjectAgentStartsIn(config json.RawMessage) string {
	var envelope struct {
		AgentStartsIn string `json:"agentStartsIn"`
	}
	if len(config) > 0 && json.Unmarshal(config, &envelope) == nil &&
		strings.TrimSpace(envelope.AgentStartsIn) == ProjectAgentStartsInParent {
		return ProjectAgentStartsInParent
	}
	return ProjectAgentStartsInRepository
}

// ProjectWorkingDirectory returns the project's own working directory for a
// bring-your-own Coder workspace (for example ~/work/{repo}), which overrides
// the organization connection's. Empty keeps the connection's.
func ProjectWorkingDirectory(config json.RawMessage) string {
	var envelope struct {
		WorkingDirectory string `json:"workingDirectory"`
	}
	if len(config) == 0 || json.Unmarshal(config, &envelope) != nil {
		return ""
	}
	return strings.TrimSpace(envelope.WorkingDirectory)
}

// ValidateProjectRules checks agentRules or orchestratorRules text.
func ValidateProjectRules(key, rules string) error {
	if len(rules) > MaxProjectRulesBytes {
		return fmt.Errorf("%s must be at most 16 KB", key)
	}
	if strings.ContainsRune(rules, 0) || !utf8.ValidString(rules) {
		return fmt.Errorf("%s contains invalid characters", key)
	}
	return nil
}

// decodeProjectRepoList decodes a "repos" value: an array of {url, branch}.
func decodeProjectRepoList(raw json.RawMessage) ([]RepoRef, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var repos []RepoRef
	if err := decoder.Decode(&repos); err != nil {
		return nil, errors.New("repos must be an array of {url, branch} objects")
	}
	return repos, nil
}

func validateAgentStartsIn(raw json.RawMessage) error {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return errors.New("agentStartsIn must be a string")
	}
	switch strings.TrimSpace(value) {
	case "", ProjectAgentStartsInRepository, ProjectAgentStartsInParent:
		return nil
	}
	return fmt.Errorf("agentStartsIn must be %q or %q", ProjectAgentStartsInRepository, ProjectAgentStartsInParent)
}

// AdoptLegacyExtraRepos moves a new project's coder dev kit "extraRepos" (the
// shape the project form sent before "repos" existed) to "repos", unless the
// config already carries "repos". Run on create only: an existing project's
// legacy list keeps being read in place by ProjectAdditionalRepos.
func AdoptLegacyExtraRepos(config json.RawMessage) (json.RawMessage, error) {
	var values map[string]json.RawMessage
	if len(config) == 0 {
		return config, nil
	}
	if err := json.Unmarshal(config, &values); err != nil || values == nil {
		return nil, errors.New("project config must be an object")
	}
	rawCoder, ok := values["coder"]
	if !ok {
		return config, nil
	}
	var coder map[string]json.RawMessage
	if err := json.Unmarshal(rawCoder, &coder); err != nil || coder == nil {
		return config, nil
	}
	legacy, ok := coder["extraRepos"]
	if !ok {
		return config, nil
	}
	delete(coder, "extraRepos")
	if _, hasRepos := values["repos"]; !hasRepos {
		values["repos"] = legacy
	}
	encodedCoder, err := json.Marshal(coder)
	if err != nil {
		return nil, err
	}
	values["coder"] = encodedCoder
	return json.Marshal(values)
}

// NormalizeProjectRepoSettings validates and canonicalizes the multi-repo
// settings of a project config whose main repository is mainURL: "repos" (see
// NormalizeProjectRepos), "agentStartsIn", "workingDirectory", "agentRules",
// "orchestratorRules", and the repository each Setup and Cleanup step names,
// which must be the main repository or one of the additional ones. A step in
// the main repository is stored as a plain command. Keys it does not know are
// left as they are, so a config without any of these keys comes back
// unchanged.
func NormalizeProjectRepoSettings(mainURL string, config json.RawMessage) (json.RawMessage, error) {
	var values map[string]json.RawMessage
	if len(config) == 0 {
		return config, nil
	}
	if err := json.Unmarshal(config, &values); err != nil || values == nil {
		return nil, errors.New("project config must be an object")
	}
	changed := false
	if raw, ok := values["repos"]; ok {
		listed, err := decodeProjectRepoList(raw)
		if err != nil {
			return nil, err
		}
		repos, err := NormalizeProjectRepos(mainURL, listed)
		if err != nil {
			return nil, err
		}
		if repos == nil {
			repos = []RepoRef{}
		}
		values["repos"], _ = json.Marshal(repos)
		changed = true
	}
	if raw, ok := values["agentStartsIn"]; ok {
		if err := validateAgentStartsIn(raw); err != nil {
			return nil, err
		}
	}
	if raw, ok := values["workingDirectory"]; ok {
		var directory string
		if json.Unmarshal(raw, &directory) != nil {
			return nil, errors.New("workingDirectory must be a string")
		}
		if err := ValidateCoderWorkingDirectory(strings.TrimSpace(directory)); err != nil {
			return nil, err
		}
		// A standalone project has no repository to name the folder after; the
		// worker would ignore such a folder and start in its default one.
		if IsStandaloneRepositoryURL(mainURL) && strings.Contains(directory, "{repo}") {
			return nil, errors.New("a project without a repository cannot use {repo} in its working folder; name the folder, for example ~/work/scratch")
		}
	}
	for _, key := range []string{"agentRules", "orchestratorRules"} {
		raw, ok := values[key]
		if !ok {
			continue
		}
		var rules string
		if json.Unmarshal(raw, &rules) != nil {
			return nil, fmt.Errorf("%s must be a string", key)
		}
		if err := ValidateProjectRules(key, rules); err != nil {
			return nil, err
		}
	}
	known := map[string]string{}
	mainCanonical, _, mainErr := CanonicalGitHubRepoURL(mainURL)
	if mainErr == nil {
		known[strings.ToLower(mainCanonical)] = ""
	}
	interim, _ := json.Marshal(values)
	for _, repo := range ProjectAdditionalRepos(interim) {
		if canonical, _, err := CanonicalGitHubRepoURL(repo.URL); err == nil {
			known[strings.ToLower(canonical)] = canonical
		}
	}
	// Folders a per-repository step may run in, lower-cased, mapped to the
	// folder as the repository checks out into it ("" for the main one).
	folders := map[string]string{}
	if mainErr == nil {
		folders[strings.ToLower(RepoDirName(mainCanonical))] = ""
	}
	for _, canonical := range known {
		if canonical != "" {
			folders[strings.ToLower(RepoDirName(canonical))] = RepoDirName(canonical)
		}
	}
	for _, key := range []string{"postCreate", "preRemove"} {
		raw, ok := values[key]
		if !ok {
			continue
		}
		steps, err := ParseProjectScriptSteps(key, raw)
		if err != nil {
			return nil, err
		}
		rewrite := false
		for index, step := range steps {
			switch {
			case step.Repository != "":
				rewrite = true
				canonical, _, err := CanonicalGitHubRepoURL(step.Repository)
				if err != nil {
					return nil, fmt.Errorf("%s step %d names %q, which is not one of the project's repositories", key, index+1, step.Repository)
				}
				stored, isProjectRepo := known[strings.ToLower(canonical)]
				if !isProjectRepo {
					return nil, fmt.Errorf("%s step %d runs in %s, which is not one of the project's repositories", key, index+1, canonical)
				}
				// Stored as a plain command for the main repository, and as the
				// folder form for an additional one.
				steps[index] = ProjectScriptStep{Command: step.Command}
				if stored != "" {
					steps[index].Folder = RepoDirName(stored)
				}
			case step.Folder != "":
				rewrite = true
				folder, isProjectFolder := folders[strings.ToLower(step.Folder)]
				if !isProjectFolder {
					return nil, fmt.Errorf("%s step %d runs in the folder %q, which is not one of the project's repositories", key, index+1, step.Folder)
				}
				steps[index] = ProjectScriptStep{Command: step.Command, Folder: folder}
			}
		}
		if rewrite {
			if err := validateStoredScriptSteps(key, steps); err != nil {
				return nil, err
			}
			values[key], _ = json.Marshal(steps)
			changed = true
		}
	}
	if !changed {
		return config, nil
	}
	return json.Marshal(values)
}

// GitHubRepositoryAccess is what project setup knows about a project's
// repositories through the organization's GitHub App grants: the granted main
// repository's URL and installation, and the installation each additional
// repository (keyed by lower-cased owner/name) is granted through.
type GitHubRepositoryAccess struct {
	MainURL          string
	MainInstallation string
	Installations    map[string]string
}

// validateStoredScriptSteps checks the stored form of each step against the
// command limits, so a release that predates per-repository steps accepts the
// list when it is saved back.
func validateStoredScriptSteps(kind string, steps []ProjectScriptStep) error {
	stored := make([]string, len(steps))
	for index, step := range steps {
		stored[index] = step.storedForm()
	}
	return ValidateProjectScriptCommands(kind, stored)
}
