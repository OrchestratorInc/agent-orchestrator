package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
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

// Each Setup or Cleanup step runs for at most its time limit, in whole
// minutes: the one the step sets, from MinScriptStepTimeoutMinutes to
// MaxScriptStepTimeoutMinutes, else its kind's default. A Setup step gets
// DefaultSetupStepTimeoutMinutes, as the local daemon gives it; a Cleanup step
// gets DefaultCleanupStepTimeoutMinutes, as Cleanup as a whole also has the
// sandbox provider's deletion budget.
const (
	DefaultSetupStepTimeoutMinutes   = 30
	DefaultCleanupStepTimeoutMinutes = 10
	MinScriptStepTimeoutMinutes      = 1
	MaxScriptStepTimeoutMinutes      = 120
)

// ScriptStepTimeout is the time limit of a step that sets minutes (0, or a
// value out of range, is defaultMinutes, its kind's default; a value past the
// maximum is capped).
func ScriptStepTimeout(minutes, defaultMinutes int) time.Duration {
	if minutes < MinScriptStepTimeoutMinutes {
		minutes = defaultMinutes
	}
	return time.Duration(min(minutes, MaxScriptStepTimeoutMinutes)) * time.Minute
}

// ValidScriptStepTimeoutMinutes reports whether minutes is a time limit a
// step may set.
func ValidScriptStepTimeoutMinutes(minutes int) bool {
	return minutes >= MinScriptStepTimeoutMinutes && minutes <= MaxScriptStepTimeoutMinutes
}

// scriptTimeoutMarker starts the first line of a stored step that sets its
// own time limit: "# ao-timeout: 30m". It is a shell comment, so a control
// plane, worker or desktop release that predates per-step limits keeps, shows
// and runs the step (with the default limit).
const scriptTimeoutMarker = "# ao-timeout: "

var scriptTimeoutPattern = regexp.MustCompile(`^# ao-timeout: ([0-9]{1,4})m\r?\n`)

// TimedScriptCommand is the stored form of a step with its own time limit:
// command after a timeout marker line. A step without one (0, or a value that
// is not a limit) is command unchanged and runs with its kind's default. A
// limit equal to a default is kept too: Setup and Cleanup have different
// defaults, and a typed limit stays the step's own if a default changes.
func TimedScriptCommand(minutes int, command string) string {
	if minutes <= 0 {
		return command
	}
	return scriptTimeoutMarker + strconv.Itoa(minutes) + "m\n" + command
}

// ParseTimedScriptCommand splits a stored step written by TimedScriptCommand
// into its time limit and command; ok is false (and command is stored) for a
// step without a timeout marker. minutes is as written, so it may be out of
// range.
func ParseTimedScriptCommand(stored string) (minutes int, command string, ok bool) {
	match := scriptTimeoutPattern.FindStringSubmatch(stored)
	if match == nil {
		return 0, stored, false
	}
	minutes, _ = strconv.Atoi(match[1])
	return minutes, stored[len(match[0]):], true
}

// SplitScriptStepTimeout is a step's command without its timeout marker and
// the time limit it runs with (ScriptStepTimeout, with defaultMinutes, its
// kind's default).
func SplitScriptStepTimeout(stored string, defaultMinutes int) (string, time.Duration) {
	minutes, command, _ := ParseTimedScriptCommand(stored)
	return command, ScriptStepTimeout(minutes, defaultMinutes)
}

// SplitTimedScriptCommands splits commands that may lead with a timeout
// marker into the plain commands and each one's time limit in minutes (0 for
// its kind's default), for a worker's launch context. minutes is nil when no
// command sets its own limit.
func SplitTimedScriptCommands(timed []string) (commands []string, minutes []int) {
	if timed == nil {
		return nil, nil
	}
	commands = make([]string, len(timed))
	limits := make([]int, len(timed))
	custom := false
	for index, stored := range timed {
		limit, command, _ := ParseTimedScriptCommand(stored)
		if !ValidScriptStepTimeoutMinutes(limit) {
			limit = 0
		}
		commands[index], limits[index] = command, limit
		custom = custom || limit != 0
	}
	if custom {
		minutes = limits
	}
	return commands, minutes
}

// JoinTimedScriptCommands is the inverse of SplitTimedScriptCommands: each
// command led by its time limit's marker when minutes sets one. A missing or
// zero entry is the default limit.
func JoinTimedScriptCommands(commands []string, minutes []int) []string {
	if commands == nil {
		return nil
	}
	timed := make([]string, len(commands))
	for index, command := range commands {
		limit := 0
		if index < len(minutes) && ValidScriptStepTimeoutMinutes(minutes[index]) {
			limit = minutes[index]
		}
		timed[index] = TimedScriptCommand(limit, command)
	}
	return timed
}

// ProjectScriptStep is one Setup or Cleanup command. Repository, when set, is
// the canonical URL of the project repository the command runs in; Folder,
// when set, is that repository's folder beside the main checkout as read back
// from a stored step. Both empty runs it in the main repository.
// TimeoutMinutes is the step's own time limit; 0 is its kind's default
// (DefaultSetupStepTimeoutMinutes or DefaultCleanupStepTimeoutMinutes).
//
// A step is stored as a plain string either way (see RepoScriptCommand and
// TimedScriptCommand), so a control plane or desktop release that predates
// per-repository steps or per-step time limits reads, runs and keeps it. The
// {command, repository, timeoutMinutes} object form is accepted on input
// only.
type ProjectScriptStep struct {
	Command        string `json:"command"`
	Repository     string `json:"repository,omitempty"`
	TimeoutMinutes int    `json:"timeoutMinutes,omitempty"`
	Folder         string `json:"-"`
	// object is true for a step given in the object form, which is stored
	// as a plain string.
	object bool
}

// invalidScriptStepTimeout stands for a time limit that is not a whole number
// of minutes, which validation reports.
const invalidScriptStepTimeout = -1

// UnmarshalJSON accepts a plain command string, including the stored
// RepoScriptCommand and TimedScriptCommand forms, or {"command",
// "repository", "timeoutMinutes"}.
func (s *ProjectScriptStep) UnmarshalJSON(raw []byte) error {
	var command string
	if err := json.Unmarshal(raw, &command); err == nil {
		minutes, rest, timed := ParseTimedScriptCommand(command)
		if timed && minutes == 0 {
			minutes = invalidScriptStepTimeout
		}
		if dir, inner, ok := ParseRepoScriptCommand(rest); ok {
			*s = ProjectScriptStep{Command: inner, Folder: dir, TimeoutMinutes: minutes}
			return nil
		}
		*s = ProjectScriptStep{Command: rest, TimeoutMinutes: minutes}
		return nil
	}
	var object struct {
		Command        *string         `json:"command"`
		Repository     string          `json:"repository"`
		TimeoutMinutes json.RawMessage `json:"timeoutMinutes"`
	}
	if err := json.Unmarshal(raw, &object); err != nil || object.Command == nil {
		return errors.New("each command must be a string or an object with a command")
	}
	*s = ProjectScriptStep{Command: *object.Command, Repository: strings.TrimSpace(object.Repository), object: true}
	if text := strings.TrimSpace(string(object.TimeoutMinutes)); text != "" && text != "null" {
		minutes, err := strconv.Atoi(text)
		if err != nil || minutes == 0 {
			minutes = invalidScriptStepTimeout
		}
		s.TimeoutMinutes = minutes
	}
	return nil
}

// MarshalJSON writes the stored, plain-string form of a step.
func (s ProjectScriptStep) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.storedForm())
}

func (s ProjectScriptStep) storedForm() string {
	return TimedScriptCommand(s.TimeoutMinutes, s.repositoryForm())
}

// repositoryForm is the command, wrapped to run in its repository's folder
// when that is not the main one.
func (s ProjectScriptStep) repositoryForm() string {
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
// It carries no time limit (see TimedShellCommand).
func (s ProjectScriptStep) ShellCommand(mainURL string) string {
	if s.Repository != "" && strings.EqualFold(s.Repository, mainURL) {
		return s.Command
	}
	return s.repositoryForm()
}

// TimedShellCommand is ShellCommand with the step's own time limit, when it
// sets one, as a timeout marker line (TimedScriptCommand).
func (s ProjectScriptStep) TimedShellCommand(mainURL string) string {
	return TimedScriptCommand(s.TimeoutMinutes, s.ShellCommand(mainURL))
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

// ProjectWorkingDirectory returns the project's own working folder in a Coder
// workspace (for example ~/work/{repo}). With the workspace worker user it
// overrides the organization connection's working directory; with the
// dedicated ao-worker user it is where AO checks the main repository out
// (see CoderCheckoutDirectory). Empty keeps the default.
func ProjectWorkingDirectory(config json.RawMessage) string {
	var envelope struct {
		WorkingDirectory string `json:"workingDirectory"`
	}
	if len(config) == 0 || json.Unmarshal(config, &envelope) != nil {
		return ""
	}
	return strings.TrimSpace(envelope.WorkingDirectory)
}

// MaxCoderCheckoutSiblings bounds the additional-repository folders a Coder
// session's bootstrap prepares beside a working-folder checkout.
const MaxCoderCheckoutSiblings = 20

// ErrCoderCheckoutHiddenInHome refuses an ao-worker checkout folder that is
// a hidden folder directly in the home (coderFolderHiddenInHome).
var ErrCoderCheckoutHiddenInHome = errors.New("The working folder must not be a hidden folder directly in the home folder.")

// CoderCheckoutDirectory resolves a project's working folder for a session
// whose worker runs as the dedicated ao-worker user: {repo} becomes the main
// repository's folder name (RepoDirName), so the session's checkout path is
// fixed when the session is created. A leading "~/" or "$HOME/" is kept: it is
// the workspace login user's home, known only inside the workspace.
func CoderCheckoutDirectory(template, repositoryURL string) (string, error) {
	template = strings.TrimSpace(template)
	if err := ValidateCoderWorkingDirectory(template); err != nil {
		return "", err
	}
	if template == "" {
		return "", errors.New("The working folder is empty.")
	}
	if strings.Contains(template, CoderWorkingDirectoryRepo) {
		if strings.TrimSpace(repositoryURL) == "" {
			return "", errors.New("The working folder names {repo}, but the project has no repository.")
		}
		template = strings.ReplaceAll(template, CoderWorkingDirectoryRepo, RepoDirName(repositoryURL))
	}
	if err := ValidateCoderCheckoutDirectory(template); err != nil {
		return "", err
	}
	if coderFolderHiddenInHome(template) {
		return "", ErrCoderCheckoutHiddenInHome
	}
	return template, nil
}

// ValidateCoderCheckoutDirectory checks a resolved working-folder checkout
// path for the dedicated ao-worker user: a valid working directory
// (ValidateCoderWorkingDirectory) with no placeholder left and not inside a
// hidden folder (coderFolderInsideHiddenFolder). It is checked again on every
// start of a session (its stored profile), so it does not refuse a hidden
// folder directly in the home: CoderCheckoutDirectory refuses that when the
// session is created, and a session created before that rule keeps the
// folder AO made for it (the workspace bootstrap creates no such folder).
func ValidateCoderCheckoutDirectory(value string) error {
	if err := ValidateCoderWorkingDirectory(value); err != nil {
		return err
	}
	if strings.ContainsAny(value, "{}") {
		return errors.New("The working folder must not contain a placeholder.")
	}
	if coderFolderInsideHiddenFolder(value) {
		return errors.New("The working folder must not be inside a hidden folder.")
	}
	return nil
}

// CoderCheckoutDirectoryUnusableMessage is the startup error a session shows
// when its project's working folder (template, as saved) cannot be its
// ao-worker checkout (CoderCheckoutDirectory refused it with err).
func CoderCheckoutDirectoryUnusableMessage(template string, err error) string {
	return fmt.Sprintf("AO couldn't use this project's working folder (%s): %s. "+
		"Change it in Project settings > Repositories, then start a new session.",
		strings.TrimSpace(template), strings.TrimSuffix(err.Error(), "."))
}

// coderFolderInsideHiddenFolder reports whether one of a working
// folder's parent folders is hidden. The ao-worker bootstrap lets the worker
// user enter the checkout's parent folders (chmod o+x), and a hidden one
// (~/.ssh, ~/.aws) holds credentials. The checkout's own folder may be hidden
// (a {repo} of .github) below another folder: the bootstrap never opens up a
// folder that already exists, and the worker refuses one it did not create.
// Directly in the home it may not be (coderFolderHiddenInHome). Only the
// ao-worker checkout is checked: a workspace-user session works in the
// template's own folder and changes no permissions.
func coderFolderInsideHiddenFolder(value string) bool {
	rest, ok := strings.CutPrefix(value, "~/")
	if !ok {
		rest, ok = strings.CutPrefix(value, "$HOME/")
	}
	if !ok {
		rest = strings.TrimPrefix(value, "/")
	}
	segments := strings.Split(strings.Trim(rest, "/"), "/")
	for _, segment := range segments[:len(segments)-1] {
		if strings.HasPrefix(segment, ".") {
			return true
		}
	}
	return false
}

// coderFolderHiddenInHome reports a working folder that is a hidden folder
// directly in the home (~/.local, $HOME/.github). A missing ~/.local would be
// created owned by ao-worker, and the login user's shell profile puts
// ~/.local/bin on its PATH. An absolute path's home is only known in the
// workspace, whose bootstrap refuses to create the same folder.
func coderFolderHiddenInHome(value string) bool {
	rest, ok := strings.CutPrefix(value, "~/")
	if !ok {
		rest, ok = strings.CutPrefix(value, "$HOME/")
	}
	rest = strings.Trim(rest, "/")
	return ok && !strings.Contains(rest, "/") && strings.HasPrefix(rest, ".")
}

// CoderCheckoutSiblings names the folders the project's additional
// repositories are cloned into beside a working-folder checkout, in the order
// the worker clones them (the worker's SessionRepoCheckouts): RepoDirName of
// each repository, without dot folders, the main checkout's own folder or
// duplicates.
func CoderCheckoutSiblings(config json.RawMessage, checkoutDirectory string) []string {
	main := checkoutDirectory[strings.LastIndex(checkoutDirectory, "/")+1:]
	seen := map[string]bool{main: true}
	var names []string
	for _, repo := range ProjectAdditionalRepos(config) {
		if strings.TrimSpace(repo.URL) == "" {
			continue
		}
		name := RepoDirName(repo.URL)
		if !ValidCoderCheckoutSibling(name) || seen[name] || reservedCoderCheckoutSiblings[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
		if len(names) == MaxCoderCheckoutSiblings {
			break
		}
	}
	return names
}

// reservedCoderCheckoutSiblings are folder names the bootstrap never
// pre-creates beside a working-folder checkout: "repository" is the durable
// root's own checkout folder (<durable root>/repository), so with a working
// folder directly in the durable root an empty folder of that name would pass
// for AO's checkout. The worker still clones such a repository when it can
// create the folder itself.
var reservedCoderCheckoutSiblings = map[string]bool{"repository": true}

// ValidCoderCheckoutSibling reports whether name is a folder name a Coder
// bootstrap may create beside a working-folder checkout: one RepoDirName
// segment that is not a dot folder.
func ValidCoderCheckoutSibling(name string) bool {
	if name == "" || len(name) > 255 || strings.HasPrefix(name, ".") {
		return false
	}
	return strings.IndexFunc(name, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.')
	}) < 0
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
		normalized := NormalizeCoderWorkingDirectory(directory)
		if err := ValidateCoderWorkingDirectory(normalized); err != nil {
			return nil, err
		}
		if normalized != directory {
			values["workingDirectory"], _ = json.Marshal(normalized)
			changed = true
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
			if step.object || step.TimeoutMinutes != 0 {
				// Stored as a plain string, its time limit in the stored form.
				rewrite = true
			}
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
				steps[index] = ProjectScriptStep{Command: step.Command, TimeoutMinutes: step.TimeoutMinutes}
				if stored != "" {
					steps[index].Folder = RepoDirName(stored)
				}
			case step.Folder != "":
				rewrite = true
				folder, isProjectFolder := folders[strings.ToLower(step.Folder)]
				if !isProjectFolder {
					return nil, fmt.Errorf("%s step %d runs in the folder %q, which is not one of the project's repositories", key, index+1, step.Folder)
				}
				steps[index] = ProjectScriptStep{Command: step.Command, Folder: folder, TimeoutMinutes: step.TimeoutMinutes}
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
