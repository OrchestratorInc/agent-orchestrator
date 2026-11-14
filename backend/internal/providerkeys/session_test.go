package providerkeys

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestForSessionWithholdsSubscriptionKeysSavedForOtherHarnesses(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	for name, value := range map[string]string{"ANTHROPIC_API_KEY": "ant", "OPENAI_API_KEY": "oai", "GROQ_API_KEY": "groq"} {
		if err := Save(ctx, dir, name, value); err != nil {
			t.Fatal(err)
		}
	}
	inject, withhold, err := ForSession(dir, "claude-code")
	if err != nil || inject["ANTHROPIC_API_KEY"] != "" || !reflect.DeepEqual(withhold, []string{"ANTHROPIC_API_KEY"}) || inject["OPENAI_API_KEY"] != "oai" || inject["GROQ_API_KEY"] != "groq" {
		t.Fatalf("claude-code inject=%v withhold=%v err=%v", inject, withhold, err)
	}
	if inject, withhold, _ := ForSession(dir, "goose"); len(withhold) != 0 || len(inject) != 3 {
		t.Fatalf("goose inject=%v withhold=%v", inject, withhold)
	}
	if inject, withhold, _ := ForSession(dir, ""); len(withhold) != 0 || len(inject) != 3 {
		t.Fatalf("unknown harness inject=%v withhold=%v", inject, withhold)
	}
	// Saved from Claude Code's own panel, the key reaches Claude Code.
	if err := ClaimForHarness(ctx, dir, "ANTHROPIC_API_KEY", "claude-code"); err != nil {
		t.Fatal(err)
	}
	if inject, withhold, _ := ForSession(dir, "claude-code"); len(withhold) != 0 || inject["ANTHROPIC_API_KEY"] != "ant" {
		t.Fatalf("claimed inject=%v withhold=%v", inject, withhold)
	}
	// Removing the key forgets the claim.
	if err := Remove(ctx, dir, "ANTHROPIC_API_KEY"); err != nil {
		t.Fatal(err)
	}
	if err := Save(ctx, dir, "ANTHROPIC_API_KEY", "ant2"); err != nil {
		t.Fatal(err)
	}
	if _, withhold, _ := ForSession(dir, "claude-code"); !reflect.DeepEqual(withhold, []string{"ANTHROPIC_API_KEY"}) {
		t.Fatalf("claim survived removal: withhold=%v", withhold)
	}
}

func TestReleaseHarnessDropsOnlyThatHarnessesClaim(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	if err := Save(ctx, dir, "ANTHROPIC_API_KEY", "ant"); err != nil {
		t.Fatal(err)
	}
	for _, harness := range []string{"claude-code", "crush"} {
		if err := ClaimForHarness(ctx, dir, "ANTHROPIC_API_KEY", harness); err != nil {
			t.Fatal(err)
		}
	}
	// A harness that owns no subscription variable has nothing to release.
	if released, err := ReleaseHarness(ctx, dir, "goose"); err != nil || released != "" {
		t.Fatalf("released=%q err=%v", released, err)
	}
	if released, err := ReleaseHarness(ctx, dir, "claude-code"); err != nil || released != "ANTHROPIC_API_KEY" {
		t.Fatalf("released=%q err=%v", released, err)
	}
	if _, withhold, _ := ForSession(dir, "claude-code"); !reflect.DeepEqual(withhold, []string{"ANTHROPIC_API_KEY"}) {
		t.Fatalf("Claude Code still receives its released key: withhold=%v", withhold)
	}
	// The key stays saved, and other harnesses keep receiving it.
	if inject, withhold, _ := ForSession(dir, "crush"); len(withhold) != 0 || inject["ANTHROPIC_API_KEY"] != "ant" {
		t.Fatalf("crush inject=%v withhold=%v", inject, withhold)
	}
	claims, err := loadClaims(dir)
	if err != nil || !reflect.DeepEqual(claims["ANTHROPIC_API_KEY"], []string{"crush"}) {
		t.Fatalf("claims=%v err=%v", claims, err)
	}
	// Releasing again is a no-op, and says nothing was released.
	if released, err := ReleaseHarness(ctx, dir, "claude-code"); err != nil || released != "" {
		t.Fatalf("second release released=%q err=%v", released, err)
	}
}

func TestWithheldKeepsTheUsersValueAndFailsClosed(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	t.Setenv("ANTHROPIC_API_KEY", "users-own")
	t.Setenv("OPENAI_API_KEY", "")
	t.Cleanup(func() {
		_ = RestoreInherited("ANTHROPIC_API_KEY")
		_ = RestoreInherited("OPENAI_API_KEY")
	})
	for name, value := range map[string]string{"ANTHROPIC_API_KEY": "ao-ant", "OPENAI_API_KEY": "ao-oai"} {
		if err := Save(ctx, dir, name, value); err != nil {
			t.Fatal(err)
		}
		if err := ExportForProbes(name, value); err != nil {
			t.Fatal(err)
		}
	}
	// Claude Code gets back the key the user exported before AO replaced it.
	if got := Withheld(dir, "claude-code"); !reflect.DeepEqual(got, map[string]string{"ANTHROPIC_API_KEY": "users-own"}) {
		t.Fatalf("Withheld(claude-code) = %v", got)
	}
	// Codex had none of its own, so AO's copy is blanked; other harnesses
	// receive every key.
	if got := Withheld(dir, "codex"); !reflect.DeepEqual(got, map[string]string{"OPENAI_API_KEY": ""}) {
		t.Fatalf("Withheld(codex) = %v", got)
	}
	if got := Withheld(dir, "goose"); len(got) != 0 {
		t.Fatalf("Withheld(goose) = %v", got)
	}
	// A claimed key reaches its harness's sessions, but never its sign-in.
	if err := ClaimForHarness(ctx, dir, "OPENAI_API_KEY", "codex"); err != nil {
		t.Fatal(err)
	}
	if got := Withheld(dir, "codex"); len(got) != 0 {
		t.Fatalf("claimed key withheld: %v", got)
	}
	if got := WithheldForSignIn("codex"); !reflect.DeepEqual(got, map[string]string{"OPENAI_API_KEY": ""}) {
		t.Fatalf("WithheldForSignIn(codex) = %v", got)
	}
	// An unreadable claim list withholds rather than handing over AO's key.
	if err := os.WriteFile(filepath.Join(dir, harnessFilename), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Withheld(dir, "codex"); !reflect.DeepEqual(got, map[string]string{"OPENAI_API_KEY": ""}) {
		t.Fatalf("Withheld with an unreadable store = %v", got)
	}
}

func TestEnvListReplacesEarlierEntries(t *testing.T) {
	got := EnvList([]string{"PATH=/bin", "OPENAI_API_KEY=ao", "TERM=dumb"}, map[string]string{"OPENAI_API_KEY": ""})
	if !reflect.DeepEqual(got, []string{"PATH=/bin", "TERM=dumb", "OPENAI_API_KEY="}) {
		t.Fatalf("EnvList = %q", got)
	}
}

// Work run for a harness outside a session holds back what its sessions do,
// but keeps a value the work set itself.
func TestHoldBackAddsWithheldKeysWithoutReplacingSetOnes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Cleanup(func() { _ = RestoreInherited("ANTHROPIC_API_KEY") })
	if err := Save(context.Background(), dir, "ANTHROPIC_API_KEY", "ao-ant"); err != nil {
		t.Fatal(err)
	}
	if err := ExportForProbes("ANTHROPIC_API_KEY", "ao-ant"); err != nil {
		t.Fatal(err)
	}
	if got := HoldBack(t.Context(), nil, dir, "claude-code"); !reflect.DeepEqual(got, map[string]string{"ANTHROPIC_API_KEY": ""}) {
		t.Fatalf("HoldBack(nil) = %v, want AO's copy blanked", got)
	}
	env := map[string]string{"ANTHROPIC_API_KEY": "launch-own", "CLAUDE_CODE_OAUTH_TOKEN": "tok"}
	if got := HoldBack(t.Context(), env, dir, "claude-code"); got["ANTHROPIC_API_KEY"] != "launch-own" || got["CLAUDE_CODE_OAUTH_TOKEN"] != "tok" {
		t.Fatalf("HoldBack replaced a value the work set: %v", got)
	}
	if got := HoldBack(t.Context(), nil, dir, "goose"); got != nil {
		t.Fatalf("HoldBack for a harness without subscription keys = %v, want nil", got)
	}
	if got := HoldBack(t.Context(), nil, "", "claude-code"); got != nil {
		t.Fatalf("HoldBack without a data directory = %v, want nil", got)
	}
}

// An ended context reads nothing and fails closed: a key Claude Code's own
// sign-in would yield to is blanked even though the store would allow it.
func TestHoldBackFailsClosedOnAnEndedContext(t *testing.T) {
	dir := t.TempDir()
	if err := Save(context.Background(), dir, "ANTHROPIC_API_KEY", "ao-ant"); err != nil {
		t.Fatal(err)
	}
	if err := ClaimForHarness(context.Background(), dir, "ANTHROPIC_API_KEY", "claude-code"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got := HoldBack(ctx, nil, dir, "claude-code"); !reflect.DeepEqual(got, map[string]string{"ANTHROPIC_API_KEY": ""}) {
		t.Fatalf("HoldBack after cancellation = %v, want ANTHROPIC_API_KEY blanked", got)
	}
}
