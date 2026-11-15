package systeminstall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const officialReleaseSource = "official-release"

var errNoOfficialSource = errors.New("systeminstall: no official release source")

type officialSourceKind int

const (
	officialText officialSourceKind = iota
	officialJSON
	officialGitHub
	officialPyPI
	officialNPM
	officialPage
)

type officialSource struct {
	kind  officialSourceKind
	ref   string
	field string
	match *regexp.Regexp
}

var cursorDownloadVersion = regexp.MustCompile(`downloads\.cursor\.com/lab/(\d[0-9A-Za-z.-]*)/`)

var opencodeV1Release = regexp.MustCompile(`^1\.`)

var stableRelease = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

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
		return officialSource{kind: officialNPM, ref: "opencode-ai", match: opencodeV1Release}, true
	case TargetOpencodeV2:
		return officialSource{kind: officialJSON, ref: "https://opencode.ai/update/api/latest/cli/npm", field: "version"}, true
	case TargetMemcode:
		return officialSource{kind: officialGitHub, ref: "memcode-ai/memcode"}, true
	case TargetQwen:
		return officialSource{kind: officialNPM, ref: "@qwen-code/qwen-code", match: stableRelease}, true
	case TargetGoose:
		return officialSource{kind: officialGitHub, ref: "aaif-goose/goose"}, true
	case TargetKimchi:
		return officialSource{kind: officialGitHub, ref: "getkimchi/kimchi"}, true
	case TargetAutohand:
		return officialSource{kind: officialGitHub, ref: "autohandai/code-cli"}, true
	case TargetOMP:
		return officialSource{kind: officialGitHub, ref: "can1357/oh-my-pi"}, true
	case TargetHermes:
		return officialSource{kind: officialGitHub, ref: "NousResearch/hermes-agent"}, true
	case TargetStakpak:
		return officialSource{kind: officialGitHub, ref: "stakpak/agent"}, true
	case TargetAider:
		return officialSource{kind: officialPyPI, ref: "aider-chat"}, true
	case TargetVibe:
		return officialSource{kind: officialPyPI, ref: "mistral-vibe"}, true
	case TargetDroid:
		return officialSource{kind: officialNPM, ref: "droid"}, true
	case TargetPi:
		return officialSource{kind: officialNPM, ref: "@earendil-works/pi-coding-agent"}, true
	default:
		return officialSource{}, false
	}
}

func officialReleaseVersion(goos, goarch string) func(context.Context, Target) (string, error) {
	return officialReleaseVersionWith(&http.Client{Timeout: 6 * time.Second}, goos, goarch)
}

func officialReleaseVersionWith(client *http.Client, goos, goarch string) func(context.Context, Target) (string, error) {
	return func(ctx context.Context, target Target) (string, error) {
		source, ok := officialSourceFor(target, goos, goarch)
		if !ok {
			return "", errNoOfficialSource
		}
		var raw string
		var err error
		switch source.kind {
		case officialPyPI:
			raw, err = pypiLatestRelease(ctx, client, source.ref)
		case officialNPM:
			raw, err = npmLatestTag(ctx, client, source.ref)
			if err == nil && source.match != nil && !source.match.MatchString(strings.TrimPrefix(strings.TrimSpace(raw), "v")) {
				return "", fmt.Errorf("%s latest %q is not a release its installer installs", source.ref, raw)
			}
		case officialGitHub:
			if tag, tagErr := githubLatestReleaseTag(ctx, client, source.ref); tagErr == nil {
				return parseOfficialVersion(officialSource{kind: officialText}, []byte(tag))
			}
			body, fetchErr := fetchRegistry(ctx, client, "https://api.github.com/repos/"+source.ref+"/releases/latest", "application/vnd.github+json")
			if fetchErr != nil {
				return "", fetchErr
			}
			return parseOfficialVersion(source, body)
		default:
			body, fetchErr := fetchRegistry(ctx, client, source.ref, "")
			if fetchErr != nil {
				return "", fetchErr
			}
			return parseOfficialVersion(source, body)
		}
		if err != nil {
			return "", err
		}
		return parseOfficialVersion(officialSource{kind: officialText}, []byte(raw))
	}
}

func parseOfficialVersion(source officialSource, body []byte) (string, error) {
	var raw string
	switch source.kind {
	case officialText:
		raw = strings.TrimSpace(string(body))
	case officialJSON, officialGitHub:
		field := source.field
		if source.kind == officialGitHub {
			field = "tag_name"
		}
		var document map[string]any
		if err := json.Unmarshal(body, &document); err != nil {
			return "", err
		}
		raw, _ = document[field].(string)
	case officialPage:
		if found := source.match.FindSubmatch(body); found != nil {
			raw = string(found[1])
		}
	}
	version, ok := versionSemver.find(raw)
	if !ok {
		return "", fmt.Errorf("no version in official release metadata %q", raw)
	}
	return version.display, nil
}

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
