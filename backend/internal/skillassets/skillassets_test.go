package skillassets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	yaml "gopkg.in/yaml.v3"
)

func TestEmbeddedSkillFrontmatterIsValidYAML(t *testing.T) {
	body, err := files.ReadFile("using-ao/SKILL.md")
	if err != nil {
		t.Fatalf("read embedded SKILL.md: %v", err)
	}

	parts := strings.SplitN(string(body), "---", 3)
	if len(parts) != 3 {
		t.Fatal("embedded SKILL.md is missing YAML frontmatter delimiters")
	}

	var frontmatter struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
		Trigger     string `yaml:"trigger"`
	}
	if err := yaml.Unmarshal([]byte(parts[1]), &frontmatter); err != nil {
		t.Fatalf("parse embedded SKILL.md frontmatter: %v", err)
	}
	if frontmatter.Name != SkillName {
		t.Fatalf("frontmatter name = %q, want %q", frontmatter.Name, SkillName)
	}
	if strings.TrimSpace(frontmatter.Description) == "" {
		t.Fatal("frontmatter description is empty")
	}
	if strings.TrimSpace(frontmatter.Trigger) == "" {
		t.Fatal("frontmatter trigger is empty")
	}
}

func TestEmbeddedPreviewGuidanceDoesNotScaffoldStaticSites(t *testing.T) {
	previewBody, err := files.ReadFile("using-ao/commands/preview.md")
	if err != nil {
		t.Fatalf("read embedded preview guidance: %v", err)
	}
	previewText := string(previewBody)
	normalizedPreviewText := strings.Join(strings.Fields(previewText), " ")
	for _, required := range []string{
		"Static HTML and Markdown do not need a development server",
		"Never create or modify `package.json`",
		"Do not create `.ao/launch.json` unless the user asks",
		"reuse the repository's existing dev command",
		"without waiting for a separate \"open it\" request",
		"Do not steal the browser from an active application",
		"open the primary requested artifact",
		"relative to the session workspace root, not the shell's current directory",
		"Do not compensate for the current directory with `../README.md`",
		"existing confined loopback preview origin",
		"not a deployment or a new development server",
		"Do not open workspace files with `file://`",
	} {
		if !strings.Contains(normalizedPreviewText, required) {
			t.Fatalf("preview guidance missing %q:\n%s", required, previewText)
		}
	}

	skillBody, err := files.ReadFile("using-ao/SKILL.md")
	if err != nil {
		t.Fatalf("read embedded SKILL.md: %v", err)
	}
	skillText := string(skillBody)
	normalizedSkillText := strings.Join(strings.Fields(skillText), " ")
	if !strings.Contains(normalizedSkillText, "[commands/preview.md](commands/preview.md) before acting") ||
		!strings.Contains(normalizedSkillText, "automatic-handoff rules are load-bearing") ||
		!strings.Contains(normalizedSkillText, "[commands/browser.md](commands/browser.md)") ||
		!strings.Contains(normalizedSkillText, "opt-in network policy") {
		t.Fatalf("skill catalog is missing focused preview/browser routing:\n%s", skillText)
	}
}

func TestEmbeddedBrowserGuidanceKeepsNetworkCaptureOptional(t *testing.T) {
	body, err := files.ReadFile("using-ao/commands/browser.md")
	if err != nil {
		t.Fatalf("read embedded browser guidance: %v", err)
	}
	text := strings.Join(strings.Fields(string(body)), " ")
	for _, required := range []string{
		"Network capture is optional and disabled by default",
		"Do not enable it for routine navigation or interaction",
		"no request or response bodies, credentials, cookies, or query values",
		"`network status` and `network list` never enable capture",
		"The user can select or close these same tabs",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("browser guidance missing %q:\n%s", required, body)
		}
	}
}

func TestEmbeddedSessionGuidanceDocumentsCanonicalInAppLinks(t *testing.T) {
	body, err := files.ReadFile("using-ao/commands/session.md")
	if err != nil {
		t.Fatalf("read embedded session guidance: %v", err)
	}
	text := strings.Join(strings.Fields(string(body)), " ")
	for _, required := range []string{
		"ao://sessions/{project-id}/{session-id}",
		"Never use display names as identity",
		"Encode either ID with URL percent encoding",
		"Do not append query strings, fragments, action names, credentials, authorities, or extra path segments",
		"only inside a currently running AO desktop app",
		"never sends a message, executes a command, or performs another action",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("session-link guidance missing %q:\n%s", required, body)
		}
	}
}

func TestEmbeddedSpawnGuidanceListsOpenCodeV2(t *testing.T) {
	body, err := files.ReadFile("using-ao/commands/spawn.md")
	if err != nil {
		t.Fatalf("read embedded spawn guidance: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, "`opencode`, `opencode-v2`") {
		t.Fatalf("spawn guidance does not distinguish OpenCode 1 and 2:\n%s", text)
	}
}

func TestEmbeddedReviewGuidanceSeparatesExternalReviewFromOwnership(t *testing.T) {
	for _, tc := range []struct {
		path string
		want string
	}{
		{"using-ao/commands/review.md", "normal review-only worker without claiming or attaching the target PR"},
		{"using-ao/commands/spawn.md", "Spawn a normal worker to review someone else's PR without taking ownership"},
		{"using-ao/commands/session.md", "Reviewing someone else's PR"},
	} {
		body, err := files.ReadFile(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.Join(strings.Fields(string(body)), " "), tc.want) {
			t.Fatalf("%s missing %q", tc.path, tc.want)
		}
	}
	spawn, _ := files.ReadFile("using-ao/commands/spawn.md")
	if strings.Contains(string(spawn), `--name "review-pr-88" --claim-pr`) {
		t.Fatal("spawn guidance still claims the review target")
	}
}

// TestInstall_WritesSkillAndIsIdempotent: Install must lay down the embedded
// skill (SKILL.md plus a commands file) under <dataDir>/skills/using-ao, and a
// second run must clobber cleanly, leaving no stale files. This is the whole
// contract the daemon boot hook relies on.
func TestInstall_WritesSkillAndIsIdempotent(t *testing.T) {
	dataDir := t.TempDir()

	if err := Install(dataDir); err != nil {
		t.Fatalf("Install: %v", err)
	}

	skillFile := filepath.Join(Dir(dataDir), "SKILL.md")
	if b, err := os.ReadFile(skillFile); err != nil {
		t.Fatalf("read %s: %v", skillFile, err)
	} else if len(b) == 0 {
		t.Fatalf("SKILL.md is empty")
	}
	if _, err := os.Stat(filepath.Join(Dir(dataDir), "commands", "spawn.md")); err != nil {
		t.Fatalf("commands/spawn.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(Dir(dataDir), "commands", "cue.md")); err != nil {
		t.Fatalf("commands/cue.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(Dir(dataDir), "commands", "browser.md")); err != nil {
		t.Fatalf("commands/browser.md missing: %v", err)
	}

	// A stale file inside the skill dir must not survive a reinstall (clobber).
	stale := filepath.Join(Dir(dataDir), "stale.md")
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatalf("seed stale file: %v", err)
	}
	if err := Install(dataDir); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale file survived reinstall (err=%v)", err)
	}
}

// TestMaterialize_WritesIntoArbitraryDest covers the opencode adapter path:
// materialize the skill into .opencode/skills/using-ao (not the data-dir layout).
func TestMaterialize_WritesIntoArbitraryDest(t *testing.T) {
	dest := filepath.Join(t.TempDir(), ".opencode", "skills", SkillName)
	if err := Materialize(dest); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "SKILL.md")); err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	} else if len(b) == 0 {
		t.Fatal("SKILL.md is empty")
	}
	if _, err := os.Stat(filepath.Join(dest, "commands", "spawn.md")); err != nil {
		t.Fatalf("commands/spawn.md missing: %v", err)
	}
}

func TestEmbeddedReportGuidanceDistinguishesDeliverablesFromDiagnostics(t *testing.T) {
	body, err := files.ReadFile("using-ao/commands/report.md")
	if err != nil {
		t.Fatal(err)
	}
	guidance := strings.Join(strings.Fields(string(body)), " ")
	for _, want := range []string{
		"working material, not deliverables",
		"Do not attach them unless requested or needed to explain an actionable failure",
		"Summarize validation in the report note",
		"not external publishing authorization",
		"In a chat session, an HTML file from the artifact directory that you attach with `--artifact` also shows in the thread.",
	} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("report guidance missing %q", want)
		}
	}
}

func TestEmbeddedRenderGuidanceSeparatesRendersFromArtifacts(t *testing.T) {
	body, err := files.ReadFile("using-ao/commands/render.md")
	if err != nil {
		t.Fatal(err)
	}
	guidance := strings.Join(strings.Fields(string(body)), " ")
	for _, want := range []string{
		"## ao render or a session artifact",
		"when the page answers a question in the thread",
		"attach it with `ao report --artifact <path>`",
		"In a chat session, an HTML file from the artifact directory that you attach with `--artifact` also shows in the thread.",
		"add `--artifact` to `ao render` (or set `artifact: true` in `html_render`). Do this only when the user asks to keep the page.",
	} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("render guidance missing %q", want)
		}
	}
}

func TestLitmusSkillFrontmatterWordCapAndInstall(t *testing.T) {
	body, err := files.ReadFile("litmus/SKILL.md")
	if err != nil {
		t.Fatalf("read embedded litmus SKILL.md: %v", err)
	}
	parts := strings.SplitN(string(body), "---", 3)
	if len(parts) != 3 {
		t.Fatal("litmus SKILL.md is missing YAML frontmatter delimiters")
	}
	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
		Trigger     string `yaml:"trigger"`
	}
	if err := yaml.Unmarshal([]byte(parts[1]), &fm); err != nil {
		t.Fatalf("parse litmus frontmatter: %v", err)
	}
	if fm.Name != LitmusSkillName || strings.TrimSpace(fm.Description) == "" || strings.TrimSpace(fm.Trigger) == "" {
		t.Fatalf("litmus frontmatter incomplete: %+v", fm)
	}
	if n := len(strings.Fields(string(body))); n > 920 {
		t.Fatalf("litmus SKILL.md is %d words, want at most 920", n)
	}
	for _, verb := range []string{"intake", "prepare", "launch", "check add", "snapshot", "compare", "replay <headCheckId> --leg base", "--shot <N>", "submit", "--leg base", "only through `ao testing`", "never push or post to GitHub", "You have 25 minutes", "gate notes",
		"You test one pull request. Litmus builds the PR and its base commit in private app windows. Check the PR's claim and likely nearby breakage.",
		"Notes go to the PR author. Say what you saw in plain words: buttons present, what happened after a click, what was missing. Omit check ids, build names, receipt numbers and comparisons; Litmus adds those.",
		"at most 4 for a PR over 30 files, 6 otherwise", "(a guide, not a limit)", "split a check that mixes two things", "it is done only when it has a result on both",
		"Litmus saves unfinished work at the deadline", "Verify a broken claim or new failure first", "record a proved failure even when main cannot be compared",
		"end the main side as `absent` with a screenshot of where it would be; `fail` means the feature is there and broken", "--result pass|fail|partial|absent|skipped",
		"Use spare time to pin down a suspected bug, not to add breadth", "never on the PR build alone", "do the UI checks in the pack's suggested order",
		"Never run the PR's tests or any package tests; Litmus judges what a user sees.", "If the PR has nothing to click, add one check, end it `skipped --note \"nothing to click; not tested by Litmus\"`, and submit.",
		"At check end, Litmus saves light and dark shots to the receipt and comment.", "Inspect both on each changed screen.", "theme pair not taken: the app ignored the system theme", "Add differences between similar items (row summaries, list order, mixed names) to the check's `--note` as `Worth a look: <what differs>` at its start or end.", "Litmus moves that text to a `Worth a look` comment line; it does not change the verdict.",
		"`absent` is for main only", "Use `--same-fault` on a base `fail` or `partial` only after proving it has the same cause as the head failure",
		"If a `fail` is recorded, never submit `--blocked`", "Never open another run's folder; it is not evidence.",
		"Never import, export, sign in to or read the owner's real browser profiles, cookies, passwords, keychain or accounts; use test-window data or skip and say why.", "Accounts marked Device, Active, In use, ChatGPT or Claude are the owner's real logins, and a harness tab's Login, Logout or Refresh login acts on them too: look, never click Use, Switch, Log out, Sign out, Remove, Renew, Sign in again, Login, Logout, Refresh login or Use reset on them.", "End such a check `skipped` with 'would touch the owner's login'.", "Accounts you add yourself (API key) are fully testable.", `Billing and plan links are off limits, including a "Billing" tab.`} {
		if !strings.Contains(string(body), verb) {
			t.Fatalf("litmus SKILL.md does not mention %q", verb)
		}
	}

	dataDir := t.TempDir()
	if err := Install(dataDir); err != nil {
		t.Fatalf("Install: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(LitmusDir(dataDir), "SKILL.md"))
	if err != nil {
		t.Fatalf("installed litmus skill: %v", err)
	}
	if string(got) != string(body) {
		t.Fatal("installed litmus SKILL.md differs from the embedded copy")
	}
}

func TestEmbeddedTestingGuidanceRequiresApproval(t *testing.T) {
	body, err := files.ReadFile("using-ao/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"ao testing show <runId>", "ao testing post <runId>", "ao testing discard <runId>", "ask the user whether to post them", "Never post without the user's yes."} {
		if !strings.Contains(string(body), required) {
			t.Errorf("testing guidance missing %q", required)
		}
	}
}
