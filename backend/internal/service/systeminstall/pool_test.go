package systeminstall

import (
	"context"
	"strings"
	"testing"
)

func TestPoolAppearsInSettingsWithManualOnboarding(t *testing.T) {
	if !Valid(Target("pool")) || !IsAgentTarget(Target("pool")) || IsSystemTarget(Target("pool")) {
		t.Fatal("Pool must be a Settings harness target, not a legacy system installer")
	}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			plans, err := newTestService(goos, "npm", "brew").AgentPlans(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, plan := range plans {
				if plan.AgentID != "pool" {
					continue
				}
				if plan.Available || plan.Automatic || plan.Method != "manual" || plan.Command != "" || plan.DocumentationURL != "https://docs.poolside.ai/cli/install" {
					t.Fatalf("Pool plan = %#v, want upstream manual onboarding", plan)
				}
				if len(plan.Methods) != 1 || plan.Methods[0].ReinstallAvailable {
					t.Fatalf("Pool must not offer unattended install/reinstall: %#v", plan.Methods)
				}
				if goos == "windows" && !strings.Contains(plan.Reason, "macOS or Linux") {
					t.Fatalf("missing AO platform blocker: %q", plan.Reason)
				}
				return
			}
			t.Fatal("Pool is missing from Settings installation plans")
		})
	}
}
