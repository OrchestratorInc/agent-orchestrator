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

// cliServerAgentName is the agentInfo.name of the ACP server built into the
// agy CLI, started as `agy acp`.
const cliServerAgentName = "Antigravity ACP Server"

// minimumCLIACPVersion is the first agy CLI whose `agy acp` AO launches. Its
// `agy acp` steers a running turn when another prompt arrives from 1.5.0, but
// before 1.5.4 a turn hangs when a tool call needs a second approval, and a
// resumed session drops back to the default mode (1.5.4 changelog). AO
// launches older CLIs' separate server instead.
const minimumCLIACPVersion = "1.5.4"

var versionPattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)`)

// validateInitialize admits only Google's Antigravity ACP servers at a tested
// version: the separate server, which has no --version flag, or `agy acp`.
func validateInitialize(init acpsdk.InitializeResponse) error {
	if isCLIServer(init) {
		installed, ok := parseVersion(init.AgentInfo.Version)
		if !ok || installed.less(mustVersion(minimumCLIACPVersion)) {
			return fmt.Errorf("agy acp %q is older than AO's tested minimum %s",
				init.AgentInfo.Version, minimumCLIACPVersion)
		}
		return nil
	}
	if init.AgentInfo == nil || init.AgentInfo.Name != serverAgentName {
		name := ""
		if init.AgentInfo != nil {
			name = init.AgentInfo.Name
		}
		return fmt.Errorf("expected the %s server or agy acp, got agent %q", serverAgentName, name)
	}
	installed, ok := parseVersion(init.AgentInfo.Version)
	if !ok {
		return fmt.Errorf("unrecognized %s version %q (Orchestrator requires %s or newer)",
			serverAgentName, init.AgentInfo.Version, minimumServerVersion)
	}
	minimum, _ := parseVersion(minimumServerVersion)
	if installed.less(minimum) {
		return fmt.Errorf("%s %s is older than Orchestrator's tested minimum %s",
			serverAgentName, init.AgentInfo.Version, minimumServerVersion)
	}
	return nil
}

// isCLIServer reports whether the handshake came from `agy acp`.
func isCLIServer(init acpsdk.InitializeResponse) bool {
	return init.AgentInfo != nil && init.AgentInfo.Name == cliServerAgentName
}

// cliSteers reports whether the handshake came from an `agy acp` that steers.
func cliSteers(init acpsdk.InitializeResponse) bool {
	if !isCLIServer(init) {
		return false
	}
	installed, ok := parseVersion(init.AgentInfo.Version)
	return ok && !installed.less(mustVersion(minimumCLIACPVersion))
}

type version [3]int

func mustVersion(raw string) version {
	parsed, ok := parseVersion(raw)
	if !ok {
		panic("agyacp: invalid version constant " + raw)
	}
	return parsed
}

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
