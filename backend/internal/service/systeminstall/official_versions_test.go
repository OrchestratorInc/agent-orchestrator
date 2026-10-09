package systeminstall

import (
	"context"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
)

func TestParseOfficialVersionReadsEachVendorFormat(t *testing.T) {
	for _, tt := range []struct {
		name   string
		target Target
		body   string
		want   string
	}{
		{name: "plain text", target: TargetClaudeCode, body: "2.1.291\n", want: "2.1.291"},
		{name: "v-prefixed text", target: TargetFX, body: "v0.0.13", want: "0.0.13"},
		{name: "build metadata text", target: TargetAmp, body: "0.0.1791273659-g33d612\n", want: "0.0.1791273659-g33d612"},
		{name: "prefixed json tag", target: TargetCodex, body: `{"assets":[],"tag_name":"rust-v0.160.1"}`, want: "0.160.1"},
		{name: "json manifest", target: TargetDevin, body: `{"version":"3000.11.3","platforms":{}}`, want: "3000.11.3"},
		{name: "revisioned channel", target: TargetMuse, body: `{"channel":"muse-stable","version":"1.4.3-R5018.1"}`, want: "1.4.3-R5018.1"},
		{name: "github release", target: TargetOpencode, body: `{"tag_name":"v1.18.34"}`, want: "1.18.34"},
		{name: "pypi project", target: TargetAider, body: `{"info":{"version":"0.86.2"}}`, want: "0.86.2"},
		{name: "npm package", target: TargetDroid, body: `{"name":"droid","version":"0.234.0"}`, want: "0.234.0"},
		{name: "install script url", target: TargetCursor, body: `DOWNLOAD_URL="https://downloads.cursor.com/lab/2026.10.01-e373342/${OS}/${ARCH}/agent-cli-package.tar.gz"`, want: "2026.10.01-e373342"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source, ok := officialSourceFor(tt.target, "darwin", "arm64")
			if !ok {
				t.Fatalf("no official source for %s", tt.target)
			}
			got, err := parseOfficialVersion(source, []byte(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("version = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseOfficialVersionRejectsMetadataWithoutVersion(t *testing.T) {
	source, _ := officialSourceFor(TargetKiro, "linux", "amd64")
	if _, err := parseOfficialVersion(source, []byte(`{"packages":[]}`)); err == nil {
		t.Fatal("expected an error for a manifest without a version")
	}
	source, _ = officialSourceFor(TargetGrok, "linux", "amd64")
	if _, err := parseOfficialVersion(source, []byte("<html>maintenance</html>")); err == nil {
		t.Fatal("expected an error for a non-version body")
	}
}

func TestOfficialSourceForSelectsAntigravityPlatformManifest(t *testing.T) {
	source, ok := officialSourceFor(TargetAgy, "linux", "amd64")
	if !ok || source.ref != "https://antigravity-cli-auto-updater-974169037036.us-central1.run.app/manifests/linux_amd64.json" {
		t.Fatalf("source = %+v ok=%t", source, ok)
	}
	if _, ok := officialSourceFor(TargetAgy, "windows", "amd64"); ok {
		t.Fatal("windows has no verified Antigravity manifest")
	}
	if _, ok := officialSourceFor(TargetQwen, "darwin", "arm64"); ok {
		t.Fatal("Qwen has no verified official release source")
	}
}

func TestUpdateAdvisoryFallsBackToOfficialReleaseForUnownedBinary(t *testing.T) {
	s := newTestService("darwin", "npm", "brew")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/Users/test/.local/bin/claude", Output: "2.1.200 (Claude Code)\n"}, nil
	})
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return false, nil }
	var packageLookup atomic.Bool
	s.managedVersion = func(context.Context, Plan, updateVersion) (managedVersionResult, error) {
		packageLookup.Store(true)
		return managedVersionResult{Latest: "9.9.9", Channel: "latest"}, nil
	}
	s.officialVersion = func(_ context.Context, target Target) (string, error) {
		if target != TargetClaudeCode {
			t.Fatalf("official lookup for %s", target)
		}
		return "2.1.291", nil
	}
	advisory, err := s.UpdateAdvisory(context.Background(), TargetClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	if advisory.Status != UpdateStatusBehindLatest || advisory.Source != officialReleaseSource || advisory.CurrentVersion != "2.1.200" || advisory.LatestVersion != "2.1.291" {
		t.Fatalf("advisory = %+v", advisory)
	}
	if packageLookup.Load() {
		t.Fatal("package registry was queried for a binary no package manager owns")
	}
}

func TestUpdateAdvisoryPrefersOwningPackageOverOfficialRelease(t *testing.T) {
	s := newTestService("darwin", "npm")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{Output: "codex-cli 1.3.0"}, nil
	})
	s.ownsInstallation = func(_ context.Context, _ string, method, _ string, _ bool) (bool, error) { return method == "npm", nil }
	s.managedVersion = fixedManagedVersion("1.3.0", nil)
	s.officialVersion = func(context.Context, Target) (string, error) {
		t.Fatal("official release queried for a package-owned binary")
		return "", nil
	}
	advisory, err := s.UpdateAdvisory(context.Background(), TargetCodex)
	if err != nil {
		t.Fatal(err)
	}
	if advisory.Status != UpdateStatusCurrent || advisory.Source != "npm" {
		t.Fatalf("advisory = %+v", advisory)
	}
}

func TestUpdateAdvisoryUnknownWithoutOfficialSource(t *testing.T) {
	s := newTestService("darwin")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) { return VerifyResult{Output: "qwen 0.1.0"}, nil })
	s.officialVersion = func(context.Context, Target) (string, error) { return "", errNoOfficialSource }
	advisory, err := s.UpdateAdvisory(context.Background(), TargetQwen)
	if err != nil {
		t.Fatal(err)
	}
	if advisory.Status != UpdateStatusUnknown || advisory.LatestVersion != "" || advisory.Reason != "unsupported_source" {
		t.Fatalf("advisory = %+v", advisory)
	}
}

func TestUpdateAdvisoryDoesNotMoveOfficialPrereleaseToStableChannel(t *testing.T) {
	s := newTestService("darwin")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/Users/test/.local/bin/claude", Output: "claude 2.0.0-beta.1"}, nil
	})
	s.officialVersion = func(context.Context, Target) (string, error) { return "2.0.0", nil }
	advisory, err := s.UpdateAdvisory(context.Background(), TargetClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	if advisory.Status != UpdateStatusUnknown || advisory.LatestVersion != "" || advisory.Reason != "channel_unconfirmed" {
		t.Fatalf("advisory = %+v", advisory)
	}
}

func TestUpdateAdvisoryComparesBuildSuffixedOfficialReleases(t *testing.T) {
	for _, tt := range []struct {
		name       string
		target     Target
		path       string
		output     string
		latest     string
		wantStatus UpdateStatus
		wantReason UpdateUnknownReason
	}{
		{name: "amp behind", target: TargetAmp, path: "/Users/test/.amp/bin/amp", output: "0.0.1791033893-g28ae98 (released 2026-10-03T13:24:53.000Z, 4d ago)\n", latest: "0.0.1791388870-g4d32fb", wantStatus: UpdateStatusBehindLatest},
		{name: "cursor current", target: TargetCursor, path: "/Users/test/.local/share/cursor-agent/versions/2026.10.01-e373342/cursor-agent", output: "2026.10.01-e373342\n", latest: "2026.10.01-e373342", wantStatus: UpdateStatusCurrent},
		{name: "cursor same-day rebuild", target: TargetCursor, path: "/Users/test/.local/share/cursor-agent/versions/2026.10.01-e373342/cursor-agent", output: "2026.10.01-e373342\n", latest: "2026.10.01-a1b2c3d", wantStatus: UpdateStatusUnknown, wantReason: UpdateReasonBuildUnordered},
		{name: "muse current", target: TargetMuse, path: "/Users/test/.local/bin/muse", output: "Muse Code 1.4.3 (1.4.3-R5018.1)\n", latest: "1.4.3-R5018.1", wantStatus: UpdateStatusCurrent},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService("darwin")
			s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
				return VerifyResult{ResolvedPath: tt.path, Output: tt.output}, nil
			})
			s.officialVersion = func(context.Context, Target) (string, error) { return tt.latest, nil }
			advisory, err := s.UpdateAdvisory(context.Background(), tt.target)
			if err != nil {
				t.Fatal(err)
			}
			if advisory.Status != tt.wantStatus || advisory.Reason != tt.wantReason || advisory.Source != officialReleaseSource {
				t.Fatalf("advisory = %+v", advisory)
			}
		})
	}
}

func TestOfficialGitHubVersionReadsReleaseRedirect(t *testing.T) {
	var requests []string
	client := &http.Client{Transport: managerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request.Method+" "+request.URL.String())
		response := managerHTTPResponse(http.StatusFound, "")
		response.Header.Set("Location", "https://github.com/anomalyco/opencode/releases/tag/v1.18.35")
		return response, nil
	})}
	got, err := officialReleaseVersionWith(client, "darwin", "arm64")(context.Background(), TargetOpencode)
	if err != nil || got != "1.18.35" {
		t.Fatalf("version=%q err=%v", got, err)
	}
	if want := []string{"HEAD https://github.com/anomalyco/opencode/releases/latest"}; !slices.Equal(requests, want) {
		t.Fatalf("requests = %v, want %v", requests, want)
	}
}

func TestOfficialGitHubVersionFallsBackToAPIWithoutReleaseRedirect(t *testing.T) {
	for _, tt := range []struct {
		name     string
		redirect func() *http.Response
	}{
		{name: "no releases", redirect: func() *http.Response {
			response := managerHTTPResponse(http.StatusFound, "")
			response.Header.Set("Location", "https://github.com/aaif-goose/goose/releases")
			return response
		}},
		{name: "blocked", redirect: func() *http.Response { return managerHTTPResponse(http.StatusTooManyRequests, "") }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: managerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.Host == "github.com" {
					return tt.redirect(), nil
				}
				if request.URL.String() != "https://api.github.com/repos/aaif-goose/goose/releases/latest" {
					t.Fatalf("URL = %s", request.URL)
				}
				return managerHTTPResponse(http.StatusOK, `{"tag_name":"v1.9.0"}`), nil
			})}
			got, err := officialReleaseVersionWith(client, "darwin", "arm64")(context.Background(), TargetGoose)
			if err != nil || got != "1.9.0" {
				t.Fatalf("version=%q err=%v", got, err)
			}
		})
	}
}
