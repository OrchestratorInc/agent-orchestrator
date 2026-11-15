package systeminstall

import (
	"context"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hermes"
)

var (
	exactUpdateVersionPattern = regexp.MustCompile(`^v?([0-9]+(?:\.[0-9]+){1,3})(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)
	updateVersionPattern      = regexp.MustCompile(`v?[0-9]+(?:\.[0-9]+)+(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?`)
)

type updateVersion struct {
	display    string
	core       [4]uint64
	prerelease []string
	build      string
}

type versionScheme int

const (
	versionSemver versionScheme = iota
	versionBuildSuffix
)

func versionSchemeFor(target Target) versionScheme {
	switch target {
	case TargetAmp, TargetCursor, TargetMuse:
		return versionBuildSuffix
	default:
		return versionSemver
	}
}

var hermesVersionToken = regexp.MustCompile(`^Hermes Agent (v\d[^ \r\n]*)`)

func installedVersionText(target Target, resolvedPath, output string) string {
	if target != TargetHermes {
		return output
	}
	if resolved, err := filepath.EvalSymlinks(resolvedPath); err == nil {
		parts := strings.Split(filepath.ToSlash(resolved), "/")
		for i := 0; i+2 < len(parts); i++ {
			if parts[i] == "Cellar" && parts[i+1] == "hermes-agent" {
				return parts[i+2]
			}
		}
	}
	if tag := hermes.SourceReleaseTag(context.Background(), resolvedPath, output); tag != "" {
		return tag
	}
	if m := hermesVersionToken.FindStringSubmatch(output); m != nil {
		return m[1]
	}
	return output
}

func (scheme versionScheme) find(text string) (updateVersion, bool) {
	var version updateVersion
	found := false
	for _, span := range updateVersionPattern.FindAllStringIndex(text, -1) {
		sentenceEnd := span[1] < len(text) && text[span[1]] == '.' &&
			(span[1]+1 == len(text) || strings.ContainsRune(" \t\r\n", rune(text[span[1]+1])))
		if span[0] > 0 && versionTokenByte(text[span[0]-1], false) || span[1] < len(text) && versionTokenByte(text[span[1]], true) && !sentenceEnd {
			continue
		}
		candidate, ok := scheme.parse(text[span[0]:span[1]])
		if !ok {
			return updateVersion{}, false
		}
		if found && candidate.display != version.display {
			if !sameRelease(version, candidate) || version.build != "" && candidate.build != "" {
				return updateVersion{}, false
			}
			if candidate.build == "" {
				continue
			}
		}
		version, found = candidate, true
	}
	return version, found
}

func (scheme versionScheme) parse(text string) (updateVersion, bool) {
	version, ok := parseUpdateVersion(text)
	if !ok || scheme != versionBuildSuffix || len(version.prerelease) == 0 {
		return version, ok
	}
	build := strings.Join(version.prerelease, ".")
	if version.build != "" {
		build += "." + version.build
	}
	version.build, version.prerelease = build, nil
	return version, true
}

func (scheme versionScheme) compare(installed, latest updateVersion) (int, bool) {
	comparison, canCompare := compareUpdateVersions(installed, latest)
	if canCompare && comparison == 0 && scheme == versionBuildSuffix && unorderedBuilds(installed, latest) {
		return 0, false
	}
	return comparison, canCompare
}

func versionAtMost(scheme versionScheme, left, right string) bool {
	l, leftOK := scheme.parse(left)
	r, rightOK := scheme.parse(right)
	if !leftOK || !rightOK {
		return false
	}
	comparison, canCompare := scheme.compare(l, r)
	return canCompare && comparison <= 0
}

func unorderedBuilds(installed, latest updateVersion) bool {
	return installed.build != "" && latest.build != "" && installed.build != latest.build
}

func sameRelease(a, b updateVersion) bool {
	return a.core == b.core && slices.Equal(a.prerelease, b.prerelease)
}

func versionTokenByte(char byte, includeHyphen bool) bool {
	return char >= '0' && char <= '9' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char == '.' || char == '+' || includeHyphen && char == '-'
}

func parseUpdateVersion(text string) (updateVersion, bool) {
	match := exactUpdateVersionPattern.FindStringSubmatch(strings.TrimSpace(text))
	return updateVersionFromMatch(match)
}

func updateVersionFromMatch(match []string) (updateVersion, bool) {
	if len(match) != 4 {
		return updateVersion{}, false
	}
	parts := strings.Split(match[1], ".")
	if len(parts) < 2 || len(parts) > 4 {
		return updateVersion{}, false
	}
	parsed := updateVersion{build: match[3]}
	for index, part := range parts {
		value, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return updateVersion{}, false
		}
		parsed.core[index] = value
	}
	if match[2] != "" {
		parsed.prerelease = strings.Split(match[2], ".")
	}
	parsed.display = match[1]
	if match[2] != "" {
		parsed.display += "-" + match[2]
	}
	if match[3] != "" {
		parsed.display += "+" + match[3]
	}
	return parsed, true
}

func compareUpdateVersions(installed, latest updateVersion) (int, bool) {
	if len(installed.prerelease) != 0 && len(latest.prerelease) != 0 && prereleaseChannel(installed.prerelease) != prereleaseChannel(latest.prerelease) {
		return 0, false
	}
	for index := range installed.core {
		if installed.core[index] < latest.core[index] {
			return -1, true
		}
		if installed.core[index] > latest.core[index] {
			return 1, true
		}
	}
	if len(installed.prerelease) == 0 && len(latest.prerelease) == 0 {
		return 0, true
	}
	if len(installed.prerelease) == 0 {
		return 1, true
	}
	if len(latest.prerelease) == 0 {
		return -1, true
	}
	for index := 0; index < len(installed.prerelease) || index < len(latest.prerelease); index++ {
		if index == len(installed.prerelease) {
			return -1, true
		}
		if index == len(latest.prerelease) {
			return 1, true
		}
		left, leftNumeric := numericIdentifier(installed.prerelease[index])
		right, rightNumeric := numericIdentifier(latest.prerelease[index])
		switch {
		case leftNumeric && rightNumeric:
			if left < right {
				return -1, true
			}
			if left > right {
				return 1, true
			}
		case leftNumeric:
			return -1, true
		case rightNumeric:
			return 1, true
		case installed.prerelease[index] < latest.prerelease[index]:
			return -1, true
		case installed.prerelease[index] > latest.prerelease[index]:
			return 1, true
		}
	}
	return 0, true
}

func prereleaseChannel(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	if _, numeric := numericIdentifier(parts[0]); numeric {
		return ""
	}
	return strings.ToLower(parts[0])
}

func numericIdentifier(value string) (uint64, bool) {
	if value == "" {
		return 0, false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	return parsed, err == nil
}
