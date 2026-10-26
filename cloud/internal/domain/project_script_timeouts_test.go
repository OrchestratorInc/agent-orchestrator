package domain

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A step without its own limit gets its kind's default: 30 minutes for
// Setup, 10 for Cleanup. A limit it sets wins over either, capped at 2 hours.
func TestScriptStepTimeout(t *testing.T) {
	for minutes, want := range map[int]time.Duration{
		0:   30 * time.Minute,
		-1:  30 * time.Minute,
		1:   time.Minute,
		10:  10 * time.Minute,
		45:  45 * time.Minute,
		120: 2 * time.Hour,
		500: 2 * time.Hour,
	} {
		if got := ScriptStepTimeout(minutes, DefaultSetupStepTimeoutMinutes); got != want {
			t.Errorf("ScriptStepTimeout(%d, setup) = %s, want %s", minutes, got, want)
		}
	}
	for minutes, want := range map[int]time.Duration{0: 10 * time.Minute, 30: 30 * time.Minute, 5: 5 * time.Minute} {
		if got := ScriptStepTimeout(minutes, DefaultCleanupStepTimeoutMinutes); got != want {
			t.Errorf("ScriptStepTimeout(%d, cleanup) = %s, want %s", minutes, got, want)
		}
	}
}

func TestTimedScriptCommandRoundTrip(t *testing.T) {
	if got := TimedScriptCommand(0, "npm ci"); got != "npm ci" {
		t.Fatalf("default = %q", got)
	}
	// A typed limit equal to a default stays the step's own: Setup's and
	// Cleanup's defaults differ.
	for _, minutes := range []int{DefaultSetupStepTimeoutMinutes, DefaultCleanupStepTimeoutMinutes} {
		if got, want := TimedScriptCommand(minutes, "npm ci"), "# ao-timeout: "+strconv.Itoa(minutes)+"m\nnpm ci"; got != want {
			t.Fatalf("explicit %d = %q, want %q", minutes, got, want)
		}
	}
	timed := TimedScriptCommand(45, "npm ci\nmake")
	if timed != "# ao-timeout: 45m\nnpm ci\nmake" {
		t.Fatalf("timed = %q", timed)
	}
	minutes, command, ok := ParseTimedScriptCommand(timed)
	if !ok || minutes != 45 || command != "npm ci\nmake" {
		t.Fatalf("parsed = %d %q %v", minutes, command, ok)
	}
	if _, command, ok := ParseTimedScriptCommand("# a comment\nnpm ci"); ok || command != "# a comment\nnpm ci" {
		t.Fatalf("a plain comment is not a marker: %q %v", command, ok)
	}
	if command, timeout := SplitScriptStepTimeout(timed, DefaultSetupStepTimeoutMinutes); command != "npm ci\nmake" || timeout != 45*time.Minute {
		t.Fatalf("split = %q %s", command, timeout)
	}
	if command, timeout := SplitScriptStepTimeout("npm ci", DefaultSetupStepTimeoutMinutes); command != "npm ci" || timeout != 30*time.Minute {
		t.Fatalf("split plain Setup = %q %s", command, timeout)
	}
	if command, timeout := SplitScriptStepTimeout("make down", DefaultCleanupStepTimeoutMinutes); command != "make down" || timeout != 10*time.Minute {
		t.Fatalf("split plain Cleanup = %q %s", command, timeout)
	}
	// A launch context carries every limit a step sets, a default's value too.
	if commands, limits := SplitTimedScriptCommands([]string{"npm ci", "# ao-timeout: 10m\nmake"}); !reflect.DeepEqual(commands, []string{"npm ci", "make"}) ||
		!reflect.DeepEqual(limits, []int{0, 10}) {
		t.Fatalf("split commands = %q %v", commands, limits)
	}
	if _, limits := SplitTimedScriptCommands([]string{"npm ci"}); limits != nil {
		t.Fatalf("limits = %v, want none when no step sets one", limits)
	}
}

// A marker line ending in CRLF (a step pasted from a Windows editor) sets the
// step's limit too, as the desktop and the local daemon read it.
func TestTimedScriptCommandAcceptsCRLF(t *testing.T) {
	minutes, command, ok := ParseTimedScriptCommand("# ao-timeout: 45m\r\nmake down")
	if !ok || minutes != 45 || command != "make down" {
		t.Fatalf("parsed = %d %q %v", minutes, command, ok)
	}
	if command, timeout := SplitScriptStepTimeout("# ao-timeout: 90m\r\nmake down", DefaultCleanupStepTimeoutMinutes); command != "make down" || timeout != 90*time.Minute {
		t.Fatalf("split = %q %s", command, timeout)
	}
	if commands, limits := SplitTimedScriptCommands([]string{"# ao-timeout: 5m\r\nmake down"}); !reflect.DeepEqual(commands, []string{"make down"}) ||
		!reflect.DeepEqual(limits, []int{5}) {
		t.Fatalf("split commands = %q %v", commands, limits)
	}
}

func TestProjectScriptStepTimeoutJSON(t *testing.T) {
	var steps []ProjectScriptStep
	input := `["make up",{"command":"npm ci","timeoutMinutes":30},{"command":"seed","repository":"https://github.com/acme/web","timeoutMinutes":120},"# ao-timeout: 5m\n(\ncd -- '../web' || exit 1\nmake down\n)"]`
	if err := json.Unmarshal([]byte(input), &steps); err != nil {
		t.Fatal(err)
	}
	want := []ProjectScriptStep{
		{Command: "make up"},
		{Command: "npm ci", TimeoutMinutes: 30, object: true},
		{Command: "seed", Repository: "https://github.com/acme/web", TimeoutMinutes: 120, object: true},
		{Command: "make down", Folder: "web", TimeoutMinutes: 5},
	}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("steps = %+v", steps)
	}
	// Stored as plain strings: the time limit leads the repository form, so a
	// release that predates per-step limits keeps and runs the step.
	encoded, _ := json.Marshal(steps)
	wantStored := `["make up","# ao-timeout: 30m\nnpm ci","# ao-timeout: 120m\n(\ncd -- '../web' || exit 1\nseed\n)","# ao-timeout: 5m\n(\ncd -- '../web' || exit 1\nmake down\n)"]`
	if string(encoded) != wantStored {
		t.Fatalf("encoded = %s", encoded)
	}
	var back []ProjectScriptStep
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatal(err)
	}
	if again, _ := json.Marshal(back); string(again) != wantStored {
		t.Fatalf("re-encoded = %s", again)
	}
}

func TestParseProjectScriptStepsValidatesTimeouts(t *testing.T) {
	for _, valid := range []string{
		`["npm ci"]`,
		`[{"command":"npm ci","timeoutMinutes":1}]`,
		`[{"command":"npm ci","timeoutMinutes":120}]`,
		`[{"command":"npm ci","timeoutMinutes":null}]`,
		`["make up",{"command":"npm ci","timeoutMinutes":45},"# ao-timeout: 60m\nmake seed"]`,
	} {
		if _, err := ParseProjectScriptSteps("postCreate", json.RawMessage(valid)); err != nil {
			t.Errorf("%s: %v", valid, err)
		}
	}
	for _, invalid := range []string{
		`[{"command":"npm ci","timeoutMinutes":0}]`,
		`[{"command":"npm ci","timeoutMinutes":121}]`,
		`[{"command":"npm ci","timeoutMinutes":-5}]`,
		`[{"command":"npm ci","timeoutMinutes":2.5}]`,
		`[{"command":"npm ci","timeoutMinutes":"30"}]`,
		`["make up","# ao-timeout: 0m\nnpm ci"]`,
		`["# ao-timeout: 500m\nnpm ci"]`,
	} {
		_, err := ParseProjectScriptSteps("preRemove", json.RawMessage(invalid))
		if err == nil || !strings.Contains(err.Error(), "from 1 to 120") {
			t.Errorf("%s: err = %v, want the time limit refused", invalid, err)
		}
	}
	if _, err := ParseProjectScriptSteps("postCreate", json.RawMessage(`["ok",{"command":"x","timeoutMinutes":200}]`)); err == nil ||
		!strings.Contains(err.Error(), "postCreate step 2") {
		t.Fatalf("err = %v, want it to name the step", err)
	}
	// An object-form command is stored as typed, so a timeout marker line it
	// starts with would become the step's limit when read back.
	for _, typed := range []string{
		`{"postCreate":[{"command":"# ao-timeout: 500m\necho hi"}]}`,
		`{"postCreate":[{"command":"# ao-timeout: 30m\necho hi","timeoutMinutes":10}]}`,
	} {
		if _, err := ParseProjectScriptSteps("postCreate", json.RawMessage(typed[len(`{"postCreate":`):len(typed)-1])); err == nil ||
			!strings.Contains(err.Error(), "postCreate step 1") || !strings.Contains(err.Error(), "timeoutMinutes") {
			t.Errorf("%s: err = %v, want the typed marker line refused", typed, err)
		}
	}
	// A marker with nothing after it is an empty command.
	if _, err := ParseProjectScriptSteps("postCreate", json.RawMessage(`["# ao-timeout: 30m\n"]`)); err == nil {
		t.Fatal("an empty timed command must be refused")
	}
}

func TestNormalizeProjectRepoSettingsStoresTimeoutsAsStrings(t *testing.T) {
	config := json.RawMessage(`{
		"repos":[{"url":"https://github.com/acme/web"}],
		"postCreate":[{"command":"npm ci","timeoutMinutes":30},{"command":"make","repository":"https://github.com/acme/web","timeoutMinutes":90},{"command":"plain"}],
		"preRemove":["# ao-timeout: 15m\nmake down"]
	}`)
	normalized, err := NormalizeProjectRepoSettings(mainRepo, config)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	_ = json.Unmarshal(normalized, &got)
	if string(got["postCreate"]) != `["# ao-timeout: 30m\nnpm ci","# ao-timeout: 90m\n(\ncd -- '../web' || exit 1\nmake\n)","plain"]` {
		t.Fatalf("postCreate = %s", got["postCreate"])
	}
	if string(got["preRemove"]) != `["# ao-timeout: 15m\nmake down"]` {
		t.Fatalf("preRemove = %s", got["preRemove"])
	}
	if _, err := NormalizeProjectRepoSettings(mainRepo, json.RawMessage(`{"postCreate":[{"command":"x","timeoutMinutes":121}]}`)); err == nil {
		t.Fatal("an out-of-range limit must be refused")
	}
}

func TestProjectScriptsCarriesTimeoutMarkers(t *testing.T) {
	config := json.RawMessage(`{"postCreate":["make up","# ao-timeout: 45m\nnpm ci"],"preRemove":["# ao-timeout: 3m\n(\ncd -- '../web' || exit 1\nmake down\n)"]}`)
	postCreate, preRemove := ProjectScripts(config, mainRepo)
	if !reflect.DeepEqual(postCreate, []string{"make up", "# ao-timeout: 45m\nnpm ci"}) {
		t.Fatalf("postCreate = %q", postCreate)
	}
	if !reflect.DeepEqual(preRemove, []string{"# ao-timeout: 3m\n(\ncd -- '../web' || exit 1\nmake down\n)"}) {
		t.Fatalf("preRemove = %q", preRemove)
	}
}

func TestSettingsPatchValidatesScriptTimeouts(t *testing.T) {
	if err := ValidateProjectScriptsConfig(json.RawMessage(`{"postCreate":[{"command":"x","timeoutMinutes":60}]}`)); err != nil {
		t.Fatal(err)
	}
	if err := ValidateProjectScriptsConfig(json.RawMessage(`{"preRemove":[{"command":"x","timeoutMinutes":0}]}`)); err == nil {
		t.Fatal("a zero limit must be refused")
	}
}
