package systeminstall

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveHarnessReleaseFeeds queries every release source the update
// advisory depends on and fails when one stops returning a version AO can
// compare. Vendors change these endpoints without notice and the unit tests
// only see fakes, so the scheduled harness-release-feeds workflow runs this
// daily to surface a broken feed before users see "unknown".
//
// Run explicitly with AO_LIVE_HARNESS_FEEDS=1.
func TestLiveHarnessReleaseFeeds(t *testing.T) {
	if os.Getenv("AO_LIVE_HARNESS_FEEDS") != "1" {
		t.Skip("set AO_LIVE_HARNESS_FEEDS=1 to query live harness release feeds")
	}
	ctx := context.Background()
	client := &http.Client{Timeout: 15 * time.Second}

	official := officialReleaseVersionWith(client, "darwin", "arm64")
	for _, target := range agentTargets {
		if _, ok := officialSourceFor(target, "darwin", "arm64"); !ok {
			continue
		}
		t.Run("official/"+string(target), func(t *testing.T) {
			latest, err := official(ctx, target)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := versionSchemeFor(target).parse(latest); !ok {
				t.Fatalf("release %q is not comparable", latest)
			}
			t.Logf("latest %s", latest)
		})
	}

	seen := map[string]bool{}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		planner, err := newTestService(goos, "npm", "brew", "bun", "uv", "pipx", "winget", "bash", "sh").newRequestPlanner(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range agentTargets {
			for _, plan := range planner.agentMethodPlans(target, AgentOperationInstall) {
				pkg := packageWithoutLatest(plan.Package)
				name := plan.Method + "/" + pkg
				if pkg == "" || seen[name] {
					continue
				}
				seen[name] = true
				scheme := versionSchemeFor(target)
				var lookup func() (string, error)
				switch {
				case plan.Method == "npm" || plan.Method == "bun":
					lookup = func() (string, error) { return npmRegistryVersion(ctx, client, pkg, scheme) }
				case plan.Method == "homebrew" && !strings.Contains(pkg, "/"):
					// Tap packages are read from local brew metadata only.
					cask := plan.PackageCask
					lookup = func() (string, error) { return homebrewAPIVersion(ctx, client, pkg, cask) }
				case plan.Method == "uv" || plan.Method == "pipx":
					lookup = func() (string, error) { return pypiManagedVersion(ctx, client, pkg, scheme) }
				default:
					continue
				}
				t.Run("package/"+name, func(t *testing.T) {
					latest, err := lookup()
					if err != nil {
						t.Fatal(err)
					}
					if _, ok := scheme.parse(latest); !ok {
						t.Fatalf("release %q is not comparable", latest)
					}
					t.Logf("latest %s", latest)
				})
			}
		}
	}
}
