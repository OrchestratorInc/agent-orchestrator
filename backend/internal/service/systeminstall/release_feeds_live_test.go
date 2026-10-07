package systeminstall

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
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
					// Some packages publish prereleases on latest (DeepSeek ships
					// 0.2.0-rc.2 there). Check what a user who installed the
					// published release would see: a definitive answer on its channel.
					lookup = func() (string, error) {
						var distTags map[string]string
						if err := fetchRegistryJSON(ctx, client, "https://registry.npmjs.org/-/package/"+url.PathEscape(pkg)+"/dist-tags", &distTags); err != nil {
							return "", err
						}
						installed, ok := scheme.parse(distTags["latest"])
						if !ok {
							return "", fmt.Errorf("latest dist-tag %q is not comparable", distTags["latest"])
						}
						channel, err := updateChannel(installed)
						if err != nil {
							return "", err
						}
						result, err := npmRegistryVersion(ctx, client, pkg, channel, scheme)
						return result.Latest, err
					}
				case plan.Method == "homebrew" && !strings.Contains(pkg, "/"):
					// Tap packages are read from local brew metadata only.
					cask := plan.PackageCask
					lookup = func() (string, error) { return homebrewAPIVersion(ctx, client, pkg, cask) }
				case plan.Method == "uv" || plan.Method == "pipx":
					lookup = func() (string, error) {
						result, err := pypiManagedVersion(ctx, client, pkg, "latest", scheme)
						return result.Latest, err
					}
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
