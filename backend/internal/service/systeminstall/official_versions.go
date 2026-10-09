package systeminstall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// officialReleaseSource marks an advisory compared against the vendor's own
// release channel. It is deliberately not an install method ID: a matching
// version does not prove which installer produced the binary, so clients must
// not pick an update method from it.
const officialReleaseSource = "official-release"

// errNoOfficialSource means AO knows no vendor release channel for a harness.
var errNoOfficialSource = errors.New("systeminstall: no official release source")

type officialSourceKind int

const (
	// officialText is a plain-text body holding only the version.
	officialText officialSourceKind = iota
	// officialJSON is a JSON object with the version in one string field.
	officialJSON
	// officialGitHub is a GitHub repository's latest release tag.
	officialGitHub
	// officialPyPI is a PyPI project's current release.
	officialPyPI
	// officialNPM is an npm package whose release matches the native build.
	officialNPM
	// officialPage is a document that embeds the version in a URL.
	officialPage
)

// officialSource is where a vendor's installer resolves its latest release.
// Each entry mirrors the lookup the vendor's own install script performs.
type officialSource struct {
	kind  officialSourceKind
	ref   string
	field string
	match *regexp.Regexp
}

var cursorDownloadVersion = regexp.MustCompile(`downloads\.cursor\.com/lab/(\d[0-9A-Za-z.-]*)/`)

func officialSourceFor(target Target, goos, goarch string) (officialSource, bool) {
	switch target {
	case TargetClaudeCode:
		return officialSource{kind: officialText, ref: "https://downloads.claude.ai/claude-code-releases/latest"}, true
	case TargetCodex:
		return officialSource{kind: officialJSON, ref: "https://releases.openai.com/codex/channels/latest", field: "tag_name"}, true
	case TargetGrok:
		return officialSource{kind: officialText, ref: "https://x.ai/cli/stable"}, true
	case TargetKimi:
		return officialSource{kind: officialText, ref: "https://code.kimi.com/kimi-code/latest"}, true
	case TargetFX:
		return officialSource{kind: officialText, ref: "https://releases.fx.sh/latest.txt"}, true
	case TargetPrimeAgent:
		return officialSource{kind: officialText, ref: "https://pub-728493de92a943e2a9b2d17b4719f318.r2.dev/stable"}, true
	case TargetAmp:
		return officialSource{kind: officialText, ref: "https://static.ampcode.com/cli/cli-version.txt"}, true
	case TargetDevin:
		return officialSource{kind: officialJSON, ref: "https://static.devin.ai/cli/current/manifest.json", field: "version"}, true
	case TargetMuse:
		return officialSource{kind: officialJSON, ref: "https://api.meta.ai/muse-code/channels/muse-stable", field: "version"}, true
	case TargetKiro:
		return officialSource{kind: officialJSON, ref: "https://prod.download.cli.kiro.dev/stable/latest/manifest.json", field: "version"}, true
	case TargetAgy:
		if (goos != "darwin" && goos != "linux") || (goarch != "amd64" && goarch != "arm64") {
			return officialSource{}, false
		}
		return officialSource{kind: officialJSON, ref: "https://antigravity-cli-auto-updater-974169037036.us-central1.run.app/manifests/" + goos + "_" + goarch + ".json", field: "version"}, true
	case TargetCursor:
		return officialSource{kind: officialPage, ref: "https://cursor.com/install", match: cursorDownloadVersion}, true
	case TargetOpencode:
		return officialSource{kind: officialGitHub, ref: "anomalyco/opencode"}, true
	case TargetGoose:
		return officialSource{kind: officialGitHub, ref: "aaif-goose/goose"}, true
	case TargetKimchi:
		return officialSource{kind: officialGitHub, ref: "getkimchi/kimchi"}, true
	case TargetAutohand:
		return officialSource{kind: officialGitHub, ref: "autohandai/code-cli"}, true
	case TargetOMP:
		return officialSource{kind: officialGitHub, ref: "can1357/oh-my-pi"}, true
	case TargetAider:
		// Aider's installer runs `uv tool install aider-chat@latest`.
		return officialSource{kind: officialPyPI, ref: "aider-chat"}, true
	case TargetVibe:
		return officialSource{kind: officialPyPI, ref: "mistral-vibe"}, true
	case TargetDroid:
		// Factory publishes the native CLI and the npm package at one version.
		return officialSource{kind: officialNPM, ref: "droid"}, true
	case TargetPi:
		// Pi's installer is an npm install into a managed prefix.
		return officialSource{kind: officialNPM, ref: "@earendil-works/pi-coding-agent"}, true
	default:
		return officialSource{}, false
	}
}

// officialReleaseVersion fetches the vendor release channel for a harness and
// returns a bare version such as 1.2.3 or 1.4.3-R5018.1.
func officialReleaseVersion(goos, goarch string) func(context.Context, Target) (string, error) {
	return officialReleaseVersionWith(&http.Client{Timeout: 6 * time.Second}, goos, goarch)
}

func officialReleaseVersionWith(client *http.Client, goos, goarch string) func(context.Context, Target) (string, error) {
	return func(ctx context.Context, target Target) (string, error) {
		source, ok := officialSourceFor(target, goos, goarch)
		if !ok {
			return "", errNoOfficialSource
		}
		if source.kind == officialGitHub {
			if tag, err := githubLatestReleaseTag(ctx, client, source.ref); err == nil {
				return parseOfficialVersion(officialSource{kind: officialText}, []byte(tag))
			}
			// Fall back to the REST API, which also follows renamed repositories.
		}
		endpoint := source.ref
		switch source.kind {
		case officialGitHub:
			endpoint = "https://api.github.com/repos/" + source.ref + "/releases/latest"
		case officialPyPI:
			endpoint = "https://pypi.org/pypi/" + source.ref + "/json"
		case officialNPM:
			endpoint = "https://registry.npmjs.org/" + source.ref + "/latest"
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
		if err != nil {
			return "", err
		}
		if source.kind == officialGitHub {
			request.Header.Set("Accept", "application/vnd.github+json")
		}
		response, err := client.Do(request)
		if err != nil {
			return "", err
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != http.StatusOK {
			return "", fmt.Errorf("%s returned status %d", endpoint, response.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if err != nil {
			return "", err
		}
		return parseOfficialVersion(source, body)
	}
}

func parseOfficialVersion(source officialSource, body []byte) (string, error) {
	var raw string
	switch source.kind {
	case officialText:
		raw = strings.TrimSpace(string(body))
	case officialJSON, officialGitHub, officialNPM:
		field := source.field
		switch source.kind {
		case officialGitHub:
			field = "tag_name"
		case officialNPM:
			field = "version"
		}
		var document map[string]any
		if err := json.Unmarshal(body, &document); err != nil {
			return "", err
		}
		raw, _ = document[field].(string)
	case officialPyPI:
		var document struct {
			Info struct {
				Version string `json:"version"`
			} `json:"info"`
		}
		if err := json.Unmarshal(body, &document); err != nil {
			return "", err
		}
		raw = document.Info.Version
	case officialPage:
		if found := source.match.FindSubmatch(body); found != nil {
			raw = string(found[1])
		}
	}
	// Tags such as v1.2.3 and rust-v0.160.1 carry the version after a prefix.
	version, ok := findUpdateVersion(raw)
	if !ok {
		return "", fmt.Errorf("no version in official release metadata %q", raw)
	}
	return version.display, nil
}

// githubLatestReleaseTag reads the tag that github.com/<repo>/releases/latest
// redirects to. Like the REST endpoint it skips drafts and prereleases, but it
// is not subject to the REST API's 60 requests per hour unauthenticated limit
// and returns no body.
func githubLatestReleaseTag(ctx context.Context, client *http.Client, repo string) (string, error) {
	noFollow := *client
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, "https://github.com/"+repo+"/releases/latest", http.NoBody)
	if err != nil {
		return "", err
	}
	response, err := noFollow.Do(request)
	if err != nil {
		return "", err
	}
	_ = response.Body.Close()
	if response.StatusCode < 300 || response.StatusCode > 399 {
		return "", fmt.Errorf("github releases/latest for %s returned status %d", repo, response.StatusCode)
	}
	location, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		return "", err
	}
	// A repository without releases redirects to its releases list instead.
	prefix := strings.ToLower("/" + repo + "/releases/tag/")
	if !strings.HasPrefix(strings.ToLower(location.Path), prefix) {
		return "", fmt.Errorf("github releases/latest for %s redirected to %q", repo, location.Path)
	}
	tag, err := url.PathUnescape(location.Path[len(prefix):])
	if err != nil || tag == "" || strings.Contains(tag, "/") {
		return "", fmt.Errorf("github releases/latest for %s has no release tag", repo)
	}
	return tag, nil
}
