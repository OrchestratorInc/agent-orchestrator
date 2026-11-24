// Package agentversion reads the version an agent harness reports, from its
// --version output or its ACP initialize response, and compares it with the
// minimum AO has tested. Comparison is Go's own semver implementation
// (golang.org/x/mod/semver); this package only finds the major.minor.patch
// core in the text a harness prints. Pre-release and build suffixes are
// ignored, so 1.2.3-rc1 counts as 1.2.3.
package agentversion

import (
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/mod/semver"
)

// Version is a canonical semantic version, such as "v1.2.3". The zero value
// is not a valid version; use Find or Parse.
type Version string

// findPattern matches a major.minor.patch anywhere in free text, such as
// "kimchi version 1.2.3" or "omp/15.0.0".
var findPattern = regexp.MustCompile(`\b(\d+)\.(\d+)\.(\d+)\b`)

// parsePattern matches a version string: an optional "v", major.minor.patch,
// then the end or a suffix that does not continue the number (such as
// "-rc1", "+build" or " (abc)").
var parsePattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:$|[^\d.])`)

// Find returns the first major.minor.patch anywhere in text, as printed by a
// command's --version.
func Find(text string) (Version, bool) {
	return fromMatch(findPattern.FindStringSubmatch(text))
}

// Parse reads a version string such as "1.2.3", "v1.2.3" or "1.2.3-beta.1",
// as an agent reports in its ACP agentInfo. Surrounding space is ignored.
func Parse(s string) (Version, bool) {
	return fromMatch(parsePattern.FindStringSubmatch(strings.TrimSpace(s)))
}

// MustParse parses a version constant and panics if it is invalid.
func MustParse(s string) Version {
	v, ok := Parse(s)
	if !ok {
		panic("agentversion: invalid version constant " + strconv.Quote(s))
	}
	return v
}

func fromMatch(match []string) (Version, bool) {
	if len(match) < 4 {
		return "", false
	}
	parts := make([]string, 3)
	for i := range parts {
		n, err := strconv.Atoi(match[i+1])
		if err != nil {
			return "", false
		}
		parts[i] = strconv.Itoa(n)
	}
	v := Version("v" + strings.Join(parts, "."))
	if !semver.IsValid(string(v)) {
		return "", false
	}
	return v, true
}

// Less reports whether v is older than other.
func (v Version) Less(other Version) bool {
	return semver.Compare(string(v), string(other)) < 0
}

// String returns the version without its "v", such as "1.2.3".
func (v Version) String() string {
	return strings.TrimPrefix(string(v), "v")
}
