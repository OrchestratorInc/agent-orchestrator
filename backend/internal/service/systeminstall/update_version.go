package systeminstall

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var (
	exactUpdateVersionPattern = regexp.MustCompile(`^v?([0-9]+(?:\.[0-9]+){1,3})(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)
	updateVersionPattern      = regexp.MustCompile(`v?[0-9]+(?:\.[0-9]+)+(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?`)
)

type updateVersion struct {
	display    string
	core       [4]uint64
	parts      int
	prerelease []string
	build      string
}

// versionScheme says how a harness formats the suffix after its numeric core.
type versionScheme int

const (
	// versionSemver reads a hyphen suffix as a semver prerelease.
	versionSemver versionScheme = iota
	// versionBuildSuffix reads a hyphen suffix as a build identifier, such as
	// Cursor's 2026.10.01-e373342, Amp's 0.0.1791388870-g4d32fb, or Muse's
	// 1.4.3-R5018.1. These vendors publish no prereleases on the channel AO
	// checks and order releases by the numeric core alone.
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

func findUpdateVersion(text string) (updateVersion, bool) {
	return versionSemver.find(text)
}

func (scheme versionScheme) find(text string) (updateVersion, bool) {
	var version updateVersion
	found := false
	for _, span := range updateVersionPattern.FindAllStringIndex(text, -1) {
		// Reject partial matches inside a longer version or identifier. A tag
		// prefix such as rust-v is allowed, but malformed cores are never sliced.
		// Some CLIs print a sentence-ending period (Copilot: "CLI 1.0.91.").
		// Only allow one period followed by whitespace/end, never .foo or .. .
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
			// Runtime warnings may precede the CLI version on either stream.
			// Without an unambiguous version, the advisory must remain unknown.
			// One release printed with and without its build, as in Muse's
			// "1.4.3 (1.4.3-R5018.1)", keeps the form that names the build.
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

// compare orders two releases. Under versionBuildSuffix, equal cores with
// different builds have no order: a same-day Cursor rebuild or a new Muse
// build number is neither provably newer nor provably current.
func (scheme versionScheme) compare(installed, latest updateVersion) (int, bool) {
	comparison, canCompare := compareUpdateVersions(installed, latest)
	if canCompare && comparison == 0 && scheme == versionBuildSuffix && unorderedBuilds(installed, latest) {
		return 0, false
	}
	return comparison, canCompare
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
	parsed := updateVersion{parts: len(parts), build: match[3]}
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
