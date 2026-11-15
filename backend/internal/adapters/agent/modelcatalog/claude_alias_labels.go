package modelcatalog

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var claudeAliasFamilies = []string{"fable", "opus", "sonnet", "haiku"}

var claudeAliasPattern = regexp.MustCompile(`^(fable|opus|sonnet|haiku)(\[[^\]]+\])?$`)

var claudeDisplayVersionPattern = regexp.MustCompile(`\b(\d{1,2}(?:\.\d{1,2})?)\b`)

func LabelClaudeAliasVersions(models, reference []ports.AgentModelInfo) []ports.AgentModelInfo {
	if len(models) == 0 || len(reference) == 0 {
		return models
	}
	newest := make(map[string][]int, len(claudeAliasFamilies))
	for _, item := range reference {
		if claudeAliasPattern.MatchString(strings.ToLower(strings.TrimSpace(item.ID))) {
			continue
		}
		family, version := claudeFamilyVersion(item)
		if family == "" {
			continue
		}
		if current, ok := newest[family]; !ok || compareVersions(version, current) > 0 {
			newest[family] = version
		}
	}
	if len(newest) == 0 {
		return models
	}
	out := make([]ports.AgentModelInfo, len(models))
	copy(out, models)
	for i, item := range out {
		match := claudeAliasPattern.FindStringSubmatch(strings.ToLower(strings.TrimSpace(item.ID)))
		if match == nil {
			continue
		}
		version, ok := newest[match[1]]
		if !ok || claudeDisplayVersionPattern.MatchString(item.Label) {
			continue
		}
		label := strings.TrimSpace(item.Label)
		if label == "" || strings.EqualFold(label, item.ID) {
			label = strings.ToUpper(match[1][:1]) + match[1][1:]
			if window := strings.Trim(match[2], "[]"); window != "" {
				label += " (" + strings.ToUpper(window) + " context)"
			}
		}
		versionText := formatVersion(version)
		if base, suffix, found := strings.Cut(label, " ("); found {
			out[i].Label = base + " " + versionText + " (" + suffix
		} else {
			out[i].Label = label + " " + versionText
		}
	}
	return out
}

func claudeFamilyVersion(item ports.AgentModelInfo) (string, []int) {
	label := strings.ToLower(item.Label)
	id := strings.ToLower(item.ID)
	for _, family := range claudeAliasFamilies {
		if !strings.Contains(label, family) && !strings.Contains(id, family) {
			continue
		}
		if _, afterFamily, found := strings.Cut(label, family); found && label != id {
			if match := claudeDisplayVersionPattern.FindString(family + afterFamily); match != "" {
				return family, parseVersion(strings.Split(match, "."))
			}
		}
		if _, rest, ok := strings.Cut(id, family); ok {
			parts := make([]string, 0, 2)
			for _, part := range strings.FieldsFunc(rest, func(r rune) bool { return r < '0' || r > '9' }) {
				if len(part) > 2 || len(parts) == 2 {
					break
				}
				parts = append(parts, part)
			}
			if len(parts) > 0 {
				return family, parseVersion(parts)
			}
		}
		return "", nil
	}
	return "", nil
}

func parseVersion(parts []string) []int {
	version := make([]int, 0, len(parts))
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			break
		}
		version = append(version, n)
	}
	return version
}

func formatVersion(version []int) string {
	parts := make([]string, len(version))
	for i, n := range version {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ".")
}
