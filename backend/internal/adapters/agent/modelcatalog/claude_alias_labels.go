package modelcatalog

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// claudeAliasFamilies are the Claude Code model aliases that name a family
// rather than a version ("opus" runs the newest Opus Claude Code knows).
var claudeAliasFamilies = []string{"fable", "opus", "sonnet", "haiku"}

// claudeAliasPattern matches a bare family alias, optionally with a context
// suffix such as "opus[1m]".
var claudeAliasPattern = regexp.MustCompile(`^(fable|opus|sonnet|haiku)(\[[^\]]+\])?$`)

// claudeDisplayVersionPattern reads "5.5" out of a provider display name such
// as "Claude Opus 5.5".
var claudeDisplayVersionPattern = regexp.MustCompile(`\b(\d{1,2}(?:\.\d{1,2})?)\b`)

// LabelClaudeAliasVersions adds the resolved version to Claude Code family
// aliases ("Opus" becomes "Opus 5.5") using the newest model of that family in
// reference, which must be a catalog the provider itself reported. Aliases
// whose family is absent from reference, or whose label already names a
// version, keep their label: a guessed version is worse than none.
func LabelClaudeAliasVersions(models, reference []ports.AgentModelInfo) []ports.AgentModelInfo {
	if len(models) == 0 || len(reference) == 0 {
		return models
	}
	newest := newestClaudeFamilyVersions(reference)
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
			// A configured alias arrives labeled with its own ID. Keep its
			// context variant ("opus[1m]") apart from the family's plain
			// model, or the picker lists "Opus 5.5" twice.
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

// newestClaudeFamilyVersions is the newest version of each family among the
// concrete models in reference. Aliases are never evidence of a version.
func newestClaudeFamilyVersions(reference []ports.AgentModelInfo) map[string][]int {
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
	return newest
}

// ClaudeAliasFamily returns the family a Claude Code alias names ("opus" for
// "opus[1m]"), or "" for anything else, including "default", whose model depends
// on the account's plan rather than on a family.
func ClaudeAliasFamily(alias string) string {
	match := claudeAliasPattern.FindStringSubmatch(strings.ToLower(strings.TrimSpace(alias)))
	if match == nil {
		return ""
	}
	return match[1]
}

// ClaudeAliasSubstitution reports whether served is a real substitution for
// alias, and if so what alias should have run ("Opus 5.5", or just "Opus" when
// reference does not list the family). It uses the same reading of the provider
// catalog as LabelClaudeAliasVersions, so it agrees with what the picker promised.
//
// Normal resolution is not a substitution: the newest model of the family, under
// any spelling, including a dated snapshot ID.
//
// Only provable differences count, because the caller tells the user a model was
// swapped: another family, or an older version than reference lists. Everything
// else is reported as no substitution: an alias with no family, a served ID with
// no readable family, a family reference does not list, and a version newer than
// reference lists, which means the catalog is behind rather than the model wrong.
func ClaudeAliasSubstitution(alias, served string, reference []ports.AgentModelInfo) (string, bool) {
	family := ClaudeAliasFamily(alias)
	if family == "" {
		return "", false
	}
	servedFamily, servedVersion := claudeFamilyVersion(ports.AgentModelInfo{ID: served})
	if servedFamily == "" {
		return "", false
	}
	expected := strings.ToUpper(family[:1]) + family[1:]
	newest, listed := newestClaudeFamilyVersions(reference)[family]
	if listed {
		expected += " " + formatVersion(newest)
	}
	if servedFamily != family {
		return expected, true
	}
	if !listed || compareVersions(servedVersion, newest) >= 0 {
		return "", false
	}
	return expected, true
}

// claudeFamilyVersion reads the family and major.minor version of a concrete
// provider model, preferring its display name and falling back to the ID
// ("claude-opus-5-5-20260901"). Snapshot dates are never versions.
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
