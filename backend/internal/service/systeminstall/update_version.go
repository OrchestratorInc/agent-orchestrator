package systeminstall

import (
	"regexp"
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

func findUpdateVersion(text string) (updateVersion, bool) {
	var version updateVersion
	found := false
	for _, span := range updateVersionPattern.FindAllStringIndex(text, -1) {
		// Reject partial matches inside a longer version or identifier. A tag
		// prefix such as rust-v is allowed, but malformed cores are never sliced.
		if span[0] > 0 && versionTokenByte(text[span[0]-1], false) || span[1] < len(text) && versionTokenByte(text[span[1]], true) {
			continue
		}
		candidate, ok := parseUpdateVersion(text[span[0]:span[1]])
		if !ok {
			return updateVersion{}, false
		}
		if found && candidate.display != version.display {
			// Runtime warnings may precede the CLI version on either stream.
			// Without an unambiguous version, the advisory must remain unknown.
			return updateVersion{}, false
		}
		version, found = candidate, true
	}
	return version, found
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
