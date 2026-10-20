package domain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const mainRepo = "https://github.com/acme/alice"

func TestRepoDirName(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/acme/ChartSnip":      "ChartSnip",
		"https://github.com/acme/ChartSnip.git":  "ChartSnip",
		"https://github.com/acme/voice_calling/": "voice_calling",
		"https://github.com/acme/a b":            "a-b",
		"":                                       "repo",
		"https://github.com/acme/..":             "repo",
	} {
		if got := RepoDirName(url); got != want {
			t.Errorf("RepoDirName(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestStandaloneRepositoryURL(t *testing.T) {
	url := StandaloneRepositoryURL("0b1f")
	if url != "https://scratch.ao.local/0b1f" || !IsStandaloneRepositoryURL(url) {
		t.Fatalf("standalone url %q", url)
	}
	for _, other := range []string{mainRepo, "http://scratch.ao.local/x", "https://scratch.ao.local:8443/x", "https://u@scratch.ao.local/x", ""} {
		if IsStandaloneRepositoryURL(other) {
			t.Errorf("%q must not be a standalone url", other)
		}
	}
}

func TestCanonicalGitHubRepoURL(t *testing.T) {
	canonical, fullName, err := CanonicalGitHubRepoURL(" https://www.github.com/acme/voice.git/ ")
	if err != nil || canonical != "https://github.com/acme/voice" || fullName != "acme/voice" {
		t.Fatalf("canonical = %q %q %v", canonical, fullName, err)
	}
	for _, bad := range []string{
		"http://github.com/acme/x", "https://gitlab.com/acme/x", "https://github.com/acme",
		"https://github.com/acme/x/tree/main", "https://github.com/acme/x?y=1", "https://u@github.com/acme/x",
		"https://github.com/acme/..", "git@github.com:acme/x.git",
	} {
		if _, _, err := CanonicalGitHubRepoURL(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestNormalizeProjectRepos(t *testing.T) {
	repos, err := NormalizeProjectRepos(mainRepo, []RepoRef{
		{URL: " https://github.com/acme/11x.git ", Branch: " develop "},
		{URL: ""},
		{URL: "https://www.github.com/acme/voice-calling-ai"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []RepoRef{{URL: "https://github.com/acme/11x", Branch: "develop"}, {URL: "https://github.com/acme/voice-calling-ai"}}
	if !reflect.DeepEqual(repos, want) {
		t.Fatalf("repos = %+v, want %+v", repos, want)
	}
	if repos, err := NormalizeProjectRepos(mainRepo, []RepoRef{{URL: " "}}); err != nil || repos != nil {
		t.Fatalf("blank list = %+v %v, want nil", repos, err)
	}

	tooMany := make([]RepoRef, MaxProjectAdditionalRepos+1)
	for index := range tooMany {
		tooMany[index] = RepoRef{URL: "https://github.com/acme/r" + string(rune('a'+index))}
	}
	for name, tc := range map[string]struct {
		main  string
		repos []RepoRef
		want  string
	}{
		"not github":       {mainRepo, []RepoRef{{URL: "https://gitlab.com/acme/x"}}, "not a GitHub repository URL"},
		"the main repo":    {mainRepo, []RepoRef{{URL: "https://github.com/ACME/alice.git"}}, "already the project's main repository"},
		"listed twice":     {mainRepo, []RepoRef{{URL: "https://github.com/acme/x"}, {URL: "https://github.com/Acme/X"}}, "listed twice"},
		"same folder":      {mainRepo, []RepoRef{{URL: "https://github.com/acme/x"}, {URL: "https://github.com/other/x"}}, "same folder"},
		"main's folder":    {mainRepo, []RepoRef{{URL: "https://github.com/other/alice"}}, "same folder"},
		"reserved folder":  {mainRepo, []RepoRef{{URL: "https://github.com/acme/repository"}}, "AO uses itself"},
		"bad branch":       {mainRepo, []RepoRef{{URL: "https://github.com/acme/x", Branch: "a b"}}, "not a valid branch name"},
		"standalone":       {StandaloneRepositoryURL("p1"), []RepoRef{{URL: "https://github.com/acme/x"}}, "without a repository"},
		"too many":         {mainRepo, tooMany, "at most 10"},
		"case only folder": {mainRepo, []RepoRef{{URL: "https://github.com/acme/Lib"}, {URL: "https://github.com/other/lib"}}, "same folder"},
	} {
		if _, err := NormalizeProjectRepos(tc.main, tc.repos); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}

func TestProjectAdditionalReposPrefersReposOverTheLegacyCoderList(t *testing.T) {
	legacy := json.RawMessage(`{"coder":{"extraRepos":[{"url":"https://github.com/acme/old"}]}}`)
	if got := ProjectAdditionalRepos(legacy); len(got) != 1 || got[0].URL != "https://github.com/acme/old" {
		t.Fatalf("legacy = %+v", got)
	}
	both := json.RawMessage(`{"repos":[{"url":"https://github.com/acme/new","branch":"b"}],"coder":{"extraRepos":[{"url":"https://github.com/acme/old"}]}}`)
	if got := ProjectAdditionalRepos(both); len(got) != 1 || got[0] != (RepoRef{URL: "https://github.com/acme/new", Branch: "b"}) {
		t.Fatalf("repos = %+v", got)
	}
	// An explicit empty list clears the legacy one.
	if got := ProjectAdditionalRepos(json.RawMessage(`{"repos":[],"coder":{"extraRepos":[{"url":"https://github.com/acme/old"}]}}`)); got != nil {
		t.Fatalf("cleared = %+v", got)
	}
	for _, config := range []string{``, `{}`, `{"repos":"nope"}`, `not json`} {
		if got := ProjectAdditionalRepos(json.RawMessage(config)); got != nil {
			t.Errorf("%q = %+v, want nil", config, got)
		}
	}
}

func TestProjectScriptStepJSON(t *testing.T) {
	var steps []ProjectScriptStep
	if err := json.Unmarshal([]byte(`["make up",{"command":"npm ci","repository":" https://github.com/acme/web "}]`), &steps); err != nil {
		t.Fatal(err)
	}
	want := []ProjectScriptStep{{Command: "make up"}, {Command: "npm ci", Repository: "https://github.com/acme/web"}}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("steps = %+v", steps)
	}
	// Steps are stored as plain strings, so a release that predates
	// per-repository steps keeps them.
	encoded, err := json.Marshal(steps)
	if err != nil || string(encoded) != `["make up","(\ncd -- '../web' || exit 1\nnpm ci\n)"]` {
		t.Fatalf("encoded = %s %v", encoded, err)
	}
	var stored []ProjectScriptStep
	if err := json.Unmarshal(encoded, &stored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored, []ProjectScriptStep{{Command: "make up"}, {Command: "npm ci", Folder: "web"}}) {
		t.Fatalf("stored steps read back = %+v", stored)
	}
	if again, _ := json.Marshal(stored); string(again) != string(encoded) {
		t.Fatalf("stored steps re-encoded = %s", again)
	}
	for _, bad := range []string{`[1]`, `[{"repository":"x"}]`, `[["x"]]`} {
		if err := json.Unmarshal([]byte(bad), &steps); err == nil {
			t.Errorf("%s must be rejected", bad)
		}
	}
}

func TestProjectScriptStepShellCommand(t *testing.T) {
	if got := (ProjectScriptStep{Command: "make up"}).ShellCommand(mainRepo); got != "make up" {
		t.Fatalf("main step = %q", got)
	}
	if got := (ProjectScriptStep{Command: "make up", Repository: "https://github.com/ACME/alice"}).ShellCommand(mainRepo); got != "make up" {
		t.Fatalf("main step named explicitly = %q", got)
	}
	got := (ProjectScriptStep{Command: "./bin/setup\nmake up", Repository: "https://github.com/acme/voice-calling-ai"}).ShellCommand(mainRepo)
	if got != "(\ncd -- '../voice-calling-ai' || exit 1\n./bin/setup\nmake up\n)" {
		t.Fatalf("sibling step = %q", got)
	}
}

func TestProjectScriptsRunsStepsInTheirRepository(t *testing.T) {
	config := json.RawMessage(`{"postCreate":["make up",{"command":"npm ci","repository":"https://github.com/acme/web"}," "],"preRemove":[{"command":"make down","repository":"https://github.com/acme/web"}]}`)
	postCreate, preRemove := ProjectScripts(config, mainRepo)
	if !reflect.DeepEqual(postCreate, []string{"make up", "(\ncd -- '../web' || exit 1\nnpm ci\n)"}) {
		t.Fatalf("postCreate = %q", postCreate)
	}
	if !reflect.DeepEqual(preRemove, []string{"(\ncd -- '../web' || exit 1\nmake down\n)"}) {
		t.Fatalf("preRemove = %q", preRemove)
	}
	// A plain list reads exactly as before.
	postCreate, _ = ProjectScripts(json.RawMessage(`{"postCreate":["npm ci"]}`), mainRepo)
	if !reflect.DeepEqual(postCreate, []string{"npm ci"}) {
		t.Fatalf("plain postCreate = %q", postCreate)
	}
}

func TestNormalizeProjectRepoSettings(t *testing.T) {
	config := json.RawMessage(`{
		"repos":[{"url":"https://www.github.com/acme/web.git"}],
		"agentStartsIn":"parent",
		"workingDirectory":"~/work/{repo}",
		"agentRules":"Read AGENTS.md first.",
		"postCreate":["make up",{"command":"npm ci","repository":"https://github.com/Acme/Web"},{"command":"make seed","repository":"https://github.com/acme/alice"}],
		"worker":{"agent":"claude-code"}
	}`)
	normalized, err := NormalizeProjectRepoSettings(mainRepo, config)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(normalized, &got); err != nil {
		t.Fatal(err)
	}
	if string(got["repos"]) != `[{"url":"https://github.com/acme/web"}]` {
		t.Fatalf("repos = %s", got["repos"])
	}
	if string(got["postCreate"]) != `["make up","(\ncd -- '../web' || exit 1\nnpm ci\n)","make seed"]` {
		t.Fatalf("postCreate = %s", got["postCreate"])
	}
	// Normalizing the stored form again keeps it as it is.
	if again, err := NormalizeProjectRepoSettings(mainRepo, normalized); err != nil {
		t.Fatal(err)
	} else if err := json.Unmarshal(again, &got); err != nil || string(got["postCreate"]) != `["make up","(\ncd -- '../web' || exit 1\nnpm ci\n)","make seed"]` {
		t.Fatalf("renormalized postCreate = %s %v", got["postCreate"], err)
	}
	if string(got["worker"]) != `{"agent":"claude-code"}` || string(got["agentStartsIn"]) != `"parent"` {
		t.Fatalf("other keys changed: %s", normalized)
	}

	// A config without multi-repo settings comes back byte for byte.
	plain := json.RawMessage(`{"postCreate": ["npm ci"], "worker": {"agent": "codex"}}`)
	if same, err := NormalizeProjectRepoSettings(mainRepo, plain); err != nil || string(same) != string(plain) {
		t.Fatalf("plain config = %s %v", same, err)
	}

	for name, tc := range map[string]struct{ main, config, want string }{
		"step in unknown repo":  {mainRepo, `{"postCreate":[{"command":"x","repository":"https://github.com/acme/other"}]}`, "not one of the project's repositories"},
		"step names non github": {mainRepo, `{"preRemove":[{"command":"x","repository":"../web"}]}`, "not one of the project's repositories"},
		"standalone step repo":  {StandaloneRepositoryURL("p"), `{"postCreate":[{"command":"x","repository":"https://github.com/acme/alice"}]}`, "not one of the project's repositories"},
		"bad start":             {mainRepo, `{"agentStartsIn":"home"}`, "agentStartsIn must be"},
		"relative folder":       {mainRepo, `{"workingDirectory":"work/{repo}"}`, "absolute path"},
		"rules too long":        {mainRepo, `{"agentRules":"` + strings.Repeat("a", MaxProjectRulesBytes+1) + `"}`, "at most 16 KB"},
		"rules not a string":    {mainRepo, `{"orchestratorRules":3}`, "must be a string"},
		"repos not objects":     {mainRepo, `{"repos":["https://github.com/acme/web"]}`, "array of {url, branch}"},
		"repos unknown field":   {mainRepo, `{"repos":[{"url":"https://github.com/acme/web","path":"x"}]}`, "array of {url, branch}"},
		"standalone repos":      {StandaloneRepositoryURL("p"), `{"repos":[{"url":"https://github.com/acme/web"}]}`, "without a repository"},
	} {
		if _, err := NormalizeProjectRepoSettings(tc.main, json.RawMessage(tc.config)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}

	// A step may name a legacy coder.extraRepos repository.
	legacy := json.RawMessage(`{"coder":{"extraRepos":[{"url":"https://github.com/acme/web"}]},"postCreate":[{"command":"x","repository":"https://github.com/acme/web"}]}`)
	if _, err := NormalizeProjectRepoSettings(mainRepo, legacy); err != nil {
		t.Fatalf("legacy repo step: %v", err)
	}
}

func TestAdoptLegacyExtraRepos(t *testing.T) {
	adopted, err := AdoptLegacyExtraRepos(json.RawMessage(`{"coder":{"templateId":"t","extraRepos":[{"url":"https://github.com/acme/web"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(adopted) != `{"coder":{"templateId":"t"},"repos":[{"url":"https://github.com/acme/web"}]}` {
		t.Fatalf("adopted = %s", adopted)
	}
	// An explicit "repos" wins; the legacy list is dropped.
	adopted, err = AdoptLegacyExtraRepos(json.RawMessage(`{"repos":[],"coder":{"extraRepos":[{"url":"https://github.com/acme/web"}]}}`))
	if err != nil || string(adopted) != `{"coder":{},"repos":[]}` {
		t.Fatalf("explicit repos = %s %v", adopted, err)
	}
	for _, config := range []string{`{}`, `{"coder":{"templateId":"t"}}`} {
		if adopted, err := AdoptLegacyExtraRepos(json.RawMessage(config)); err != nil || string(adopted) != config {
			t.Errorf("%s = %s %v", config, adopted, err)
		}
	}
}

func TestProjectAgentStartsInAndWorkingDirectory(t *testing.T) {
	if got := ProjectAgentStartsIn(json.RawMessage(`{"agentStartsIn":"parent"}`)); got != ProjectAgentStartsInParent {
		t.Fatalf("parent = %q", got)
	}
	for _, config := range []string{``, `{}`, `{"agentStartsIn":"repository"}`, `{"agentStartsIn":"elsewhere"}`, `{"agentStartsIn":1}`} {
		if got := ProjectAgentStartsIn(json.RawMessage(config)); got != ProjectAgentStartsInRepository {
			t.Errorf("%q = %q", config, got)
		}
	}
	if got := ProjectWorkingDirectory(json.RawMessage(`{"workingDirectory":" ~/work/{repo} "}`)); got != "~/work/{repo}" {
		t.Fatalf("working directory = %q", got)
	}
	if got := ProjectWorkingDirectory(json.RawMessage(`{}`)); got != "" {
		t.Fatalf("no working directory = %q", got)
	}
}

func TestSettingsPatchAcceptsMultiRepoSettings(t *testing.T) {
	patch := `{"config":{"repos":[{"url":"https://github.com/acme/web","branch":"main"}],"agentStartsIn":"parent",` +
		`"workingDirectory":"~/work/{repo}","agentRules":"rules","orchestratorRules":"more rules",` +
		`"postCreate":["make up",{"command":"npm ci","repository":"https://github.com/acme/web"}]}}`
	if _, err := ParseProjectSettingsPatch(json.RawMessage(patch)); err != nil {
		t.Fatalf("patch: %v", err)
	}
	for _, bad := range []string{
		`{"config":{"repos":{"url":"x"}}}`,
		`{"config":{"agentStartsIn":"home"}}`,
		`{"config":{"workingDirectory":"relative"}}`,
		`{"config":{"agentRules":1}}`,
		`{"config":{"postCreate":[{"repository":"https://github.com/acme/web"}]}}`,
		`{"config":{"repos":null}}`,
	} {
		if _, err := ParseProjectSettingsPatch(json.RawMessage(bad)); err == nil {
			t.Errorf("%s must be rejected", bad)
		}
	}
	if err := ValidateProjectScriptsConfig(json.RawMessage(`{"postCreate":[{"command":"npm ci","repository":"https://github.com/acme/web"}]}`)); err != nil {
		t.Fatalf("object step: %v", err)
	}
	if err := ValidateProjectScriptsConfig(json.RawMessage(`{"postCreate":[{"command":"","repository":"https://github.com/acme/web"}]}`)); err == nil {
		t.Fatal("an empty object step must be rejected")
	}
}

func TestNormalizeProjectRepoSettingsChecksStoredStepFolders(t *testing.T) {
	// A stored step whose folder is one of the project's repositories is kept
	// (in that repository's spelling); the main repository's folder becomes a
	// plain command; any other folder, including one whose repository was just
	// removed, is refused with the step named.
	config := json.RawMessage(`{"repos":[{"url":"https://github.com/acme/web"}],"postCreate":["(\ncd -- '../WEB' || exit 1\nnpm ci\n)","(\ncd -- '../alice' || exit 1\nmake seed\n)"]}`)
	normalized, err := NormalizeProjectRepoSettings(mainRepo, config)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(normalized, &got); err != nil {
		t.Fatal(err)
	}
	if string(got["postCreate"]) != `["(\ncd -- '../web' || exit 1\nnpm ci\n)","make seed"]` {
		t.Fatalf("postCreate = %s", got["postCreate"])
	}
	_, err = NormalizeProjectRepoSettings(mainRepo, json.RawMessage(`{"repos":[],"preRemove":["(\ncd -- '../web' || exit 1\nmake down\n)"]}`))
	if err == nil || !strings.Contains(err.Error(), "preRemove step 1") {
		t.Fatalf("step in a removed repository: err = %v", err)
	}
}

func TestNormalizeProjectReposRejectsDotFolders(t *testing.T) {
	for _, name := range []string{".aws", ".ssh", ".docker", ".claude", ".github"} {
		if _, err := NormalizeProjectRepos(mainRepo, []RepoRef{{URL: "https://github.com/acme/" + name}}); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
}

func TestStoredStepsStayWithinTheCommandLimit(t *testing.T) {
	long := strings.Repeat("x", MaxProjectScriptCommandBytes-5)
	raw, _ := json.Marshal([]ProjectScriptStep{{Command: long, Repository: "https://github.com/acme/web"}})
	if _, err := ParseProjectScriptSteps("postCreate", raw); err == nil {
		t.Fatal("a step whose stored form passes the 4 KB limit must be refused")
	}
}

func TestStandaloneProjectRejectsRepoPlaceholderInWorkingFolder(t *testing.T) {
	standalone := StandaloneRepositoryURL("6f2c5d7e-0000-4000-8000-000000000000")
	if _, err := NormalizeProjectRepoSettings(standalone, json.RawMessage(`{"workingDirectory":"~/work/{repo}"}`)); err == nil {
		t.Fatal("a standalone project accepted {repo} in its working folder")
	}
	if _, err := NormalizeProjectRepoSettings(standalone, json.RawMessage(`{"workingDirectory":"~/work/scratch"}`)); err != nil {
		t.Fatalf("a named working folder was refused: %v", err)
	}
	if _, err := NormalizeProjectRepoSettings("https://github.com/acme/alice", json.RawMessage(`{"workingDirectory":"~/work/{repo}"}`)); err != nil {
		t.Fatalf("a repository project refused {repo}: %v", err)
	}
}
