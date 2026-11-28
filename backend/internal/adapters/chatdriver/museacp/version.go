package museacp

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// minimumMuseVersion is the oldest Muse Code release whose `muse acp` was
// checked: the ACP registry lists 1.4.24, and AO's lab ran 1.4.25.
const minimumMuseVersion = "1.4.24"

var versionPattern = regexp.MustCompile(`\b(\d+)\.(\d+)\.(\d+)\b`)

func versionProbe(ctx context.Context, bin string) error {
	output, err := aoprocess.CommandContext(ctx, bin, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("read Muse Code version: %w", err)
	}
	return validateVersionOutput(string(output))
}

func validateVersionOutput(output string) error {
	installed, ok := parseVersion(output)
	if !ok {
		return fmt.Errorf("unrecognized Muse Code version %q (Orchestrator requires %s or newer)",
			strings.TrimSpace(output), minimumMuseVersion)
	}
	minimum, _ := parseVersion(minimumMuseVersion)
	if installed.less(minimum) {
		return fmt.Errorf("installed Muse Code %s is older than Orchestrator's tested minimum %s",
			installed, minimumMuseVersion)
	}
	return nil
}

type version [3]int

func parseVersion(output string) (version, bool) {
	match := versionPattern.FindStringSubmatch(output)
	if len(match) != 4 {
		return version{}, false
	}
	var parsed version
	for i := range parsed {
		value, err := strconv.Atoi(match[i+1])
		if err != nil {
			return version{}, false
		}
		parsed[i] = value
	}
	return parsed, true
}

func (v version) less(other version) bool {
	for i := range v {
		if v[i] != other[i] {
			return v[i] < other[i]
		}
	}
	return false
}

func (v version) String() string {
	return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
}
