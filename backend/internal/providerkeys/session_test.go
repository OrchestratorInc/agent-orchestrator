package providerkeys

import (
	"context"
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
