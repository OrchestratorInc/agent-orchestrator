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

var errUpdateChannelUnconfirmed = errors.New("update channel could not be confirmed")

type managedVersionResult struct {
	Latest  string
	Channel string
}

type managedVersionChecker func(context.Context, Plan, updateVersion) (managedVersionResult, error)

func newManagedVersionChecker(commands ports.CommandRunner, client *http.Client) managedVersionChecker {
	if client == nil {
		client = &http.Client{Timeout: 4 * time.Second}
	}
	return func(ctx context.Context, plan Plan, current updateVersion) (managedVersionResult, error) {
		plan.Package = packageWithoutLatest(plan.Package)
		channel, err := updateChannel(current)
		if err != nil {
			return managedVersionResult{}, err
		}
		switch plan.Method {
		case "npm", "pnpm", "yarn":
			if result, ok := checkNodeManager(ctx, commands, plan, channel); ok {
				return result, nil
			}
			return npmRegistryVersion(ctx, client, plan.Package, channel)
		case "bun":
			packageSpec := plan.Package
			if channel != "latest" {
				packageSpec += "@" + channel
			}
			output, commandErr := runManagedVersionCommand(ctx, commands, []string{"bun", "pm", "view", packageSpec, "version"})
			if result, ok := managedResult(output, channel); ok {
				return result, nil
			}
			if commandErr == nil {
				commandErr = fmt.Errorf("bun returned no version for %s", plan.Package)
			}
			result, registryErr := npmRegistryVersion(ctx, client, plan.Package, channel)
			if registryErr == nil {
				return result, nil
			}
			return managedVersionResult{}, fmt.Errorf("bun lookup failed: %w; registry fallback: %w", commandErr, registryErr)
		case "homebrew":
			return homebrewManagedVersion(ctx, commands, plan, channel)
		case "winget":
			return wingetManagedVersion(ctx, commands, plan, channel)
		case "uv", "pipx":
			return pypiManagedVersion(ctx, client, plan.Package, channel)
		default:
			return managedVersionResult{}, fmt.Errorf("unsupported version source %s", plan.Method)
		}
	}
}

func updateChannel(current updateVersion) (string, error) {
	if len(current.prerelease) == 0 {
		return "latest", nil
	}
	channel := prereleaseChannel(current.prerelease)
	if channel == "" {
		return "", fmt.Errorf("%w: installed prerelease has no named channel", errUpdateChannelUnconfirmed)
	}
	return channel, nil
}

func checkNodeManager(ctx context.Context, commands ports.CommandRunner, plan Plan, channel string) (managedVersionResult, bool) {
	if commands == nil {
		return managedVersionResult{}, false
	}
	var outdated, view []string
	switch plan.Method {
	case "npm":
		outdated = []string{"npm", "outdated", "-g", "--json"}
		if plan.PackagePrefix != "" {
			outdated = append(outdated, "--prefix", plan.PackagePrefix)
		}
		outdated = append(outdated, plan.Package)
		view = []string{"npm", "view", plan.Package + "@" + channel, "version", "--json"}
	case "pnpm":
		outdated = []string{"pnpm", "outdated", "--global", "--format", "json", plan.Package}
		view = []string{"pnpm", "view", plan.Package + "@" + channel, "version", "--json"}
	case "yarn":
		outdated = []string{"yarn", "global", "outdated", "--json", plan.Package}
		view = []string{"yarn", "info", plan.Package + "@" + channel, "version", "--json"}
	}
	out, _ := runManagedVersionCommand(ctx, commands, outdated)
	if latest := parseNodeOutdated(plan.Method, plan.Package, out); latest != "" {
		if result, ok := managedResult(latest, channel); ok {
			return result, true
		}
	}
	out, _ = runManagedVersionCommand(ctx, commands, view)
	if latest := parseNodeView(plan.Method, out); latest != "" {
		if result, ok := managedResult(latest, channel); ok {
			return result, true
		}
	}
	return managedVersionResult{}, false
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

func homebrewManagedVersion(ctx context.Context, commands ports.CommandRunner, plan Plan, channel string) (managedVersionResult, error) {
	if commands == nil {
		return managedVersionResult{}, fmt.Errorf("homebrew command runner unavailable")
	}
	selector := "--formula"
	if plan.PackageCask {
		selector = "--cask"
	}
	outdatedArgv := []string{"brew", "outdated", "--json=v2", selector, plan.Package}
	out, outdatedErr := runManagedVersionCommand(ctx, commands, outdatedArgv)
	if latest := parseHomebrewVersion(out, plan.Package, plan.PackageCask, true); latest != "" {
		if result, ok := managedResult(latest, channel); ok {
			return result, nil
		}
		return managedVersionResult{}, fmt.Errorf("%w: Homebrew outdated version", errUpdateChannelUnconfirmed)
	}
	infoArgv := []string{"brew", "info", "--json=v2", selector, plan.Package}
	out, infoErr := runManagedVersionCommand(ctx, commands, infoArgv)
	if latest := parseHomebrewVersion(out, plan.Package, plan.PackageCask, false); latest != "" {
		if result, ok := managedResult(latest, channel); ok {
			return result, nil
		}
		return managedVersionResult{}, fmt.Errorf("%w: Homebrew current version", errUpdateChannelUnconfirmed)
	}
	if infoErr != nil {
		return managedVersionResult{}, infoErr
	}
	if outdatedErr != nil {
		return managedVersionResult{}, outdatedErr
	}
	return managedVersionResult{}, fmt.Errorf("homebrew returned no version for %s", plan.Package)
}

func parseHomebrewVersion(raw, pkg string, cask, outdated bool) string {
	var info struct {
		Formulae []struct {
			Name           string `json:"name"`
			CurrentVersion string `json:"current_version"`
			Versions       struct {
				Stable string `json:"stable"`
			} `json:"versions"`
		} `json:"formulae"`
		Casks []struct {
			Name           json.RawMessage `json:"name"`
			Token          string          `json:"token"`
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
			if token != pkg {
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
		if entry.Name != pkg {
			continue
		}
		if outdated {
			return entry.CurrentVersion
		}
		return entry.Versions.Stable
	}
	return ""
}

func wingetManagedVersion(ctx context.Context, commands ports.CommandRunner, plan Plan, channel string) (managedVersionResult, error) {
	if commands == nil {
		return managedVersionResult{}, fmt.Errorf("winget command runner unavailable")
	}
	// Supplying --id makes winget upgrade install the selected update. Listing
	// without a package selector is read-only; filter the exact ID in the output.
	argv := []string{"winget", "upgrade", "--disable-interactivity"}
	out, commandErr := runManagedVersionCommand(ctx, commands, argv)
	latest, matches := parseWingetUpgrade(out, plan.Package)
	if matches == 1 {
		if result, ok := managedResult(latest, channel); ok {
			return result, nil
		}
		return managedVersionResult{}, fmt.Errorf("%w: Winget version for %s", errUpdateChannelUnconfirmed, plan.Package)
	}
	if matches > 1 {
		return managedVersionResult{}, fmt.Errorf("winget returned ambiguous rows for %s", plan.Package)
	}
	showArgv := []string{"winget", "show", "--id", plan.Package, "--exact", "--source", "winget", "--versions", "--accept-source-agreements", "--disable-interactivity"}
	showOutput, showErr := runManagedVersionCommand(ctx, commands, showArgv)
	if latest := latestVersionLine(showOutput); latest != "" {
		if result, ok := managedResult(latest, channel); ok {
			return result, nil
		}
	}
	if showErr != nil {
		return managedVersionResult{}, showErr
	}
	if commandErr != nil {
		return managedVersionResult{}, commandErr
	}
	return managedVersionResult{}, fmt.Errorf("winget returned no version for %s", plan.Package)
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

func npmRegistryVersion(ctx context.Context, client *http.Client, pkg, channel string) (managedVersionResult, error) {
	var metadata struct {
		DistTags map[string]string `json:"dist-tags"`
	}
	if err := fetchRegistryJSON(ctx, client, "https://registry.npmjs.org/"+url.PathEscape(pkg), &metadata); err != nil {
		return managedVersionResult{}, err
	}
	latest := metadata.DistTags[channel]
	if latest == "" && channel != "latest" {
		// The prerelease identifier need not be the dist-tag: e.g. next may
		// publish beta versions. Only accept an unambiguous matching family.
		for tag, version := range metadata.DistTags {
			if tag == "latest" {
				continue
			}
			candidate, ok := managedResult(version, channel)
			if !ok {
				continue
			}
			if latest != "" && latest != candidate.Latest {
				return managedVersionResult{}, fmt.Errorf("%w: multiple dist-tags match %s", errUpdateChannelUnconfirmed, channel)
			}
			latest = candidate.Latest
		}
	}
	if latest == "" {
		return managedVersionResult{}, fmt.Errorf("%w: npm package %s has no %s dist-tag", errUpdateChannelUnconfirmed, pkg, channel)
	}
	if result, ok := managedResult(latest, channel); ok {
		return result, nil
	}
	return managedVersionResult{}, fmt.Errorf("%w: npm %s dist-tag is incompatible with installed version", errUpdateChannelUnconfirmed, channel)
}

func pypiManagedVersion(ctx context.Context, client *http.Client, pkg, channel string) (managedVersionResult, error) {
	var metadata struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err := fetchRegistryJSON(ctx, client, "https://pypi.org/pypi/"+url.PathEscape(pkg)+"/json", &metadata); err != nil {
		return managedVersionResult{}, err
	}
	if result, ok := managedResult(metadata.Info.Version, channel); ok {
		return result, nil
	}
	return managedVersionResult{}, fmt.Errorf("%w: PyPI version is incompatible with installed channel", errUpdateChannelUnconfirmed)
}

func fetchRegistryJSON(ctx context.Context, client *http.Client, endpoint string, destination any) error {
	if client == nil {
		return fmt.Errorf("registry client unavailable")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("registry status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxRegistryMetadataBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxRegistryMetadataBytes {
		return fmt.Errorf("registry metadata exceeds %d bytes", maxRegistryMetadataBytes)
	}
	return json.Unmarshal(body, destination)
}

func managedResult(raw, channel string) (managedVersionResult, bool) {
	latest, ok := parseUpdateVersion(strings.TrimSpace(raw))
	if !ok {
		return managedVersionResult{}, false
	}
	if channel == "latest" {
		if len(latest.prerelease) != 0 {
			return managedVersionResult{}, false
		}
	} else if prereleaseChannel(latest.prerelease) != channel {
		return managedVersionResult{}, false
	}
	return managedVersionResult{Latest: latest.display, Channel: channel}, true
}

func runManagedVersionCommand(ctx context.Context, commands ports.CommandRunner, argv []string) (string, error) {
	if commands == nil {
		return "", fmt.Errorf("manager command runner unavailable")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output := &capturedOutput{max: maxOutputBytes}
	// Manager warnings are not part of the JSON response.
	err := commands.Run(probeCtx, argv, output, io.Discard)
	return output.String(), err
}
