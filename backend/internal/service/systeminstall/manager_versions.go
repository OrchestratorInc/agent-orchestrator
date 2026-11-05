package systeminstall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const maxRegistryMetadataBytes = 1 << 20

// errLatestUnparseable means the package source answered with a release AO
// cannot compare. It is not a lookup failure, so no fallback source is tried.
var errLatestUnparseable = errors.New("latest release version could not be parsed")

// managedVersionChecker reads the release the package source publishes as
// latest. Updates always install that release, so a prerelease install is
// compared with it rather than with its own prerelease channel.
type managedVersionChecker func(context.Context, Plan) (string, error)

func newManagedVersionChecker(commands ports.CommandRunner, client *http.Client) managedVersionChecker {
	if client == nil {
		client = &http.Client{Timeout: 4 * time.Second}
	}
	return func(ctx context.Context, plan Plan) (string, error) {
		plan.Package = packageWithoutLatest(plan.Package)
		scheme := versionSchemeFor(plan.Target)
		switch plan.Method {
		case "npm", "pnpm", "yarn", "bun":
			// The public registry answers without starting a package manager. Ask
			// the manager, which honours the user's configured registry, only when
			// that registry is unreachable, e.g. behind a corporate mirror.
			result, registryErr := npmRegistryVersion(ctx, client, plan.Package, scheme)
			if registryErr == nil || errors.Is(registryErr, errLatestUnparseable) {
				return result, registryErr
			}
			if result, ok := nodeManagerVersion(ctx, commands, plan, scheme); ok {
				return result, nil
			}
			return "", fmt.Errorf("%s registry lookup failed: %w", plan.Method, registryErr)
		case "homebrew":
			return homebrewManagedVersion(ctx, commands, client, plan, scheme)
		case "winget":
			return wingetManagedVersion(ctx, commands, plan, scheme)
		case "uv", "pipx":
			return pypiManagedVersion(ctx, client, plan.Package, scheme)
		default:
			return "", fmt.Errorf("unsupported version source %s", plan.Method)
		}
	}
}

func nodeManagerVersion(ctx context.Context, commands ports.CommandRunner, plan Plan, scheme versionScheme) (string, bool) {
	if plan.Method != "bun" {
		return checkNodeManager(ctx, commands, plan, scheme)
	}
	output, _ := runProbe(ctx, commands, managerProbeTimeout, false, []string{"bun", "pm", "view", plan.Package, "version"})
	return managedResult(output, scheme)
}

func checkNodeManager(ctx context.Context, commands ports.CommandRunner, plan Plan, scheme versionScheme) (string, bool) {
	if commands == nil {
		return "", false
	}
	var outdated, view []string
	switch plan.Method {
	case "npm":
		outdated = []string{"npm", "outdated", "-g", "--json"}
		if plan.PackagePrefix != "" {
			outdated = append(outdated, "--prefix", plan.PackagePrefix)
		}
		outdated = append(outdated, plan.Package)
		view = []string{"npm", "view", plan.Package + "@latest", "version", "--json"}
	case "pnpm":
		outdated = []string{"pnpm", "outdated", "--global", "--format", "json", plan.Package}
		view = []string{"pnpm", "view", plan.Package + "@latest", "version", "--json"}
	case "yarn":
		outdated = []string{"yarn", "global", "outdated", "--json", plan.Package}
		view = []string{"yarn", "info", plan.Package + "@latest", "version", "--json"}
	}
	out, _ := runProbe(ctx, commands, managerProbeTimeout, false, outdated)
	if latest := parseNodeOutdated(plan.Method, plan.Package, out); latest != "" {
		if result, ok := managedResult(latest, scheme); ok {
			return result, true
		}
	}
	out, _ = runProbe(ctx, commands, managerProbeTimeout, false, view)
	if latest := parseNodeView(plan.Method, out); latest != "" {
		if result, ok := managedResult(latest, scheme); ok {
			return result, true
		}
	}
	return "", false
}

func parseNodeOutdated(method, pkg, raw string) string {
	if method == "yarn" {
		for _, line := range strings.Split(raw, "\n") {
			var message struct {
				Type string `json:"type"`
				Data struct {
					Head []string   `json:"head"`
					Body [][]string `json:"body"`
				} `json:"data"`
			}
			if json.Unmarshal([]byte(line), &message) != nil || message.Type != "table" {
				continue
			}
			packageColumn, latestColumn := -1, -1
			for index, heading := range message.Data.Head {
				switch strings.ToLower(heading) {
				case "package":
					packageColumn = index
				case "latest":
					latestColumn = index
				}
			}
			for _, row := range message.Data.Body {
				if packageColumn >= 0 && latestColumn >= 0 && packageColumn < len(row) && latestColumn < len(row) && row[packageColumn] == pkg {
					return row[latestColumn]
				}
			}
		}
		return ""
	}
	var packages map[string]struct {
		Latest string `json:"latest"`
	}
	if json.Unmarshal([]byte(raw), &packages) != nil {
		return ""
	}
	return packages[pkg].Latest
}

func parseNodeView(method, raw string) string {
	if method == "yarn" {
		var message struct {
			Type string `json:"type"`
			Data string `json:"data"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(raw)), &message) == nil && message.Type == "inspect" {
			return message.Data
		}
		return ""
	}
	var version string
	if json.Unmarshal([]byte(strings.TrimSpace(raw)), &version) == nil {
		return version
	}
	return strings.Trim(strings.TrimSpace(raw), `"`)
}

func homebrewManagedVersion(ctx context.Context, commands ports.CommandRunner, client *http.Client, plan Plan, scheme versionScheme) (string, error) {
	// brew outdated and brew info read local metadata that is only as fresh as
	// the last brew update, so they can report a stale release as current.
	// Core formulae and casks are read from the live Homebrew API first; tap
	// packages (owner/tap/name) are not published there.
	if !strings.Contains(plan.Package, "/") {
		if latest, err := homebrewAPIVersion(ctx, client, plan.Package, plan.PackageCask); err == nil {
			if result, ok := managedResult(latest, scheme); ok {
				return result, nil
			}
			return "", fmt.Errorf("%w: Homebrew API version", errLatestUnparseable)
		}
	}
	if commands == nil {
		return "", fmt.Errorf("homebrew command runner unavailable")
	}
	selector := "--formula"
	if plan.PackageCask {
		selector = "--cask"
	}
	outdatedArgv := []string{"brew", "outdated", "--json=v2", selector, plan.Package}
	out, outdatedErr := runProbe(ctx, commands, managerProbeTimeout, false, outdatedArgv)
	if latest := parseHomebrewVersion(out, plan.Package, plan.PackageCask, true); latest != "" {
		if result, ok := managedResult(latest, scheme); ok {
			return result, nil
		}
		return "", fmt.Errorf("%w: Homebrew outdated version", errLatestUnparseable)
	}
	infoArgv := []string{"brew", "info", "--json=v2", selector, plan.Package}
	out, infoErr := runProbe(ctx, commands, managerProbeTimeout, false, infoArgv)
	if latest := parseHomebrewVersion(out, plan.Package, plan.PackageCask, false); latest != "" {
		if result, ok := managedResult(latest, scheme); ok {
			return result, nil
		}
		return "", fmt.Errorf("%w: Homebrew current version", errLatestUnparseable)
	}
	if infoErr != nil {
		return "", infoErr
	}
	if outdatedErr != nil {
		return "", outdatedErr
	}
	return "", fmt.Errorf("homebrew returned no version for %s", plan.Package)
}

func parseHomebrewVersion(raw, pkg string, cask, outdated bool) string {
	var info struct {
		Formulae []struct {
			Name           string `json:"name"`
			FullName       string `json:"full_name"`
			CurrentVersion string `json:"current_version"`
			Versions       struct {
				Stable string `json:"stable"`
			} `json:"versions"`
		} `json:"formulae"`
		Casks []struct {
			Name           json.RawMessage `json:"name"`
			Token          string          `json:"token"`
			FullToken      string          `json:"full_token"`
			CurrentVersion string          `json:"current_version"`
			Version        string          `json:"version"`
		} `json:"casks"`
	}
	if json.Unmarshal([]byte(raw), &info) != nil {
		return ""
	}
	if cask {
		for _, entry := range info.Casks {
			// info uses a display-name array; outdated historically uses the
			// package token as a string in name. Never match a display name.
			token := entry.Token
			if token == "" && outdated {
				_ = json.Unmarshal(entry.Name, &token)
			}
			// A tapped cask (owner/tap/token) is named in full only by full_token.
			if token != pkg && (entry.FullToken == "" || entry.FullToken != pkg) {
				continue
			}
			if outdated {
				return entry.CurrentVersion
			}
			return entry.Version
		}
		return ""
	}
	for _, entry := range info.Formulae {
		// brew info names a tapped formula (owner/tap/name) in full only in
		// full_name; name is the short form an unqualified package matches.
		if entry.Name != pkg && (entry.FullName == "" || entry.FullName != pkg) {
			continue
		}
		if outdated {
			return entry.CurrentVersion
		}
		return entry.Versions.Stable
	}
	return ""
}

func wingetManagedVersion(ctx context.Context, commands ports.CommandRunner, plan Plan, scheme versionScheme) (string, error) {
	if commands == nil {
		return "", fmt.Errorf("winget command runner unavailable")
	}
	// Supplying --id makes winget upgrade install the selected update. Listing
	// without a package selector is read-only; filter the exact ID in the output.
	argv := []string{"winget", "upgrade", "--disable-interactivity"}
	out, commandErr := runProbe(ctx, commands, managerProbeTimeout, false, argv)
	latest, matches := parseWingetUpgrade(out, plan.Package)
	if matches == 1 {
		if result, ok := managedResult(latest, scheme); ok {
			return result, nil
		}
		return "", fmt.Errorf("%w: Winget version for %s", errLatestUnparseable, plan.Package)
	}
	if matches > 1 {
		return "", fmt.Errorf("winget returned ambiguous rows for %s", plan.Package)
	}
	showArgv := []string{"winget", "show", "--id", plan.Package, "--exact", "--source", "winget", "--versions", "--accept-source-agreements", "--disable-interactivity"}
	showOutput, showErr := runProbe(ctx, commands, managerProbeTimeout, false, showArgv)
	if latest := latestVersionLine(showOutput); latest != "" {
		if result, ok := managedResult(latest, scheme); ok {
			return result, nil
		}
	}
	if showErr != nil {
		return "", showErr
	}
	if commandErr != nil {
		return "", commandErr
	}
	return "", fmt.Errorf("winget returned no version for %s", plan.Package)
}

func parseWingetUpgrade(raw, pkg string) (string, int) {
	latest := ""
	matches := 0
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		for index, field := range fields {
			if !strings.EqualFold(field, pkg) || index+2 >= len(fields) {
				continue
			}
			if _, ok := parseUpdateVersion(fields[index+1]); !ok {
				continue
			}
			available, ok := parseUpdateVersion(fields[index+2])
			if !ok {
				continue
			}
			matches++
			latest = available.display
		}
	}
	return latest, matches
}

func latestVersionLine(raw string) string {
	var latest updateVersion
	found := false
	for _, line := range strings.Split(raw, "\n") {
		candidate, ok := parseUpdateVersion(strings.TrimSpace(line))
		if !ok {
			continue
		}
		if !found {
			latest, found = candidate, true
			continue
		}
		if comparison, versionsComparable := compareUpdateVersions(latest, candidate); versionsComparable && comparison < 0 {
			latest = candidate
		}
	}
	if !found {
		return ""
	}
	return latest.display
}

func npmRegistryVersion(ctx context.Context, client *http.Client, pkg string, scheme versionScheme) (string, error) {
	raw, err := npmLatestTag(ctx, client, pkg)
	if err != nil {
		return "", err
	}
	if result, ok := managedResult(raw, scheme); ok {
		return result, nil
	}
	return "", fmt.Errorf("%w: npm package %s latest dist-tag %q", errLatestUnparseable, pkg, raw)
}

func pypiManagedVersion(ctx context.Context, client *http.Client, pkg string, scheme versionScheme) (string, error) {
	raw, err := pypiLatestRelease(ctx, client, pkg)
	if err != nil {
		return "", err
	}
	if result, ok := managedResult(raw, scheme); ok {
		return result, nil
	}
	return "", fmt.Errorf("%w: PyPI version for %s", errLatestUnparseable, pkg)
}

// npmLatestTag reads the release the npm registry tags latest. The dist-tags
// endpoint returns only the tag map; the full package document lists every
// published version and exceeds the metadata limit for popular harnesses
// such as Claude Code (1.5 MB).
func npmLatestTag(ctx context.Context, client *http.Client, pkg string) (string, error) {
	var distTags map[string]string
	if err := fetchRegistryJSON(ctx, client, "https://registry.npmjs.org/-/package/"+url.PathEscape(pkg)+"/dist-tags", &distTags); err != nil {
		return "", err
	}
	return distTags["latest"], nil
}

// pypiLatestRelease reads a PyPI project's current release.
func pypiLatestRelease(ctx context.Context, client *http.Client, pkg string) (string, error) {
	var metadata struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err := fetchRegistryJSON(ctx, client, "https://pypi.org/pypi/"+url.PathEscape(pkg)+"/json", &metadata); err != nil {
		return "", err
	}
	return metadata.Info.Version, nil
}

func fetchRegistryJSON(ctx context.Context, client *http.Client, endpoint string, destination any) error {
	body, err := fetchRegistry(ctx, client, endpoint, "")
	if err != nil {
		return err
	}
	return json.Unmarshal(body, destination)
}

// fetchRegistry GETs a release-metadata document, bounded to
// maxRegistryMetadataBytes. accept, when set, is sent as the Accept header.
func fetchRegistry(ctx context.Context, client *http.Client, endpoint, accept string) ([]byte, error) {
	if client == nil {
		return nil, fmt.Errorf("registry client unavailable")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		request.Header.Set("Accept", accept)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned status %d", endpoint, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxRegistryMetadataBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxRegistryMetadataBytes {
		return nil, fmt.Errorf("registry metadata exceeds %d bytes", maxRegistryMetadataBytes)
	}
	return body, nil
}

func managedResult(raw string, scheme versionScheme) (string, bool) {
	latest, ok := scheme.parse(strings.TrimSpace(raw))
	if !ok {
		return "", false
	}
	return latest.display, true
}

// homebrewAPIVersion reads the current release of a core formula or cask
// from the Homebrew JSON API that brew itself syncs from.
func homebrewAPIVersion(ctx context.Context, client *http.Client, pkg string, cask bool) (string, error) {
	var metadata struct {
		Version  string `json:"version"`
		Versions struct {
			Stable string `json:"stable"`
		} `json:"versions"`
	}
	kind := "formula"
	if cask {
		kind = "cask"
	}
	if err := fetchRegistryJSON(ctx, client, "https://formulae.brew.sh/api/"+kind+"/"+url.PathEscape(pkg)+".json", &metadata); err != nil {
		return "", err
	}
	latest := metadata.Versions.Stable
	if cask {
		latest = metadata.Version
	}
	// Cask versions may append a comma-separated download token, as in
	// 1.2.3,abcdef; only the release before it is a comparable version.
	latest, _, _ = strings.Cut(latest, ",")
	if latest == "" {
		return "", fmt.Errorf("homebrew API returned no version for %s", pkg)
	}
	return latest, nil
}
