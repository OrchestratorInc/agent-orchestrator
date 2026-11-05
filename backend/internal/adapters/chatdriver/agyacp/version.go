package agyacp

import (
	"fmt"
	"regexp"
	"strconv"

	acpsdk "github.com/coder/acp-go-sdk"
)

// serverAgentName is the agentInfo.name Google's Antigravity ACP server reports.
const serverAgentName = "antigravity-acp"

// minimumServerVersion is the oldest server AO has verified end to end:
// session/request_permission with allow_once, default/auto_edit/yolo modes,
// session/load, and authenticate reusing an existing Google sign-in.
const minimumServerVersion = "1.3.0"

var versionPattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)`)

// validateInitialize admits only Google's Antigravity ACP server at a tested
// version. The server has no --version flag, so the handshake is the gate.
func validateInitialize(init acpsdk.InitializeResponse) error {
	if init.AgentInfo == nil || init.AgentInfo.Name != serverAgentName {
		name := ""
		if init.AgentInfo != nil {
			name = init.AgentInfo.Name
		}
		return fmt.Errorf("expected the %s server, got agent %q", serverAgentName, name)
	}
	installed, ok := parseVersion(init.AgentInfo.Version)
	if !ok {
		return fmt.Errorf("unrecognized %s version %q (AO requires %s or newer)",
			serverAgentName, init.AgentInfo.Version, minimumServerVersion)
	}
	minimum, _ := parseVersion(minimumServerVersion)
	if installed.less(minimum) {
		return fmt.Errorf("%s %s is older than AO's tested minimum %s",
			serverAgentName, init.AgentInfo.Version, minimumServerVersion)
	}
	return nil
}

type version [3]int

func parseVersion(raw string) (version, bool) {
	match := versionPattern.FindStringSubmatch(raw)
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
