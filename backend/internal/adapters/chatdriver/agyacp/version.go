package agyacp

import (
	"fmt"
	"regexp"
	"strconv"

	acpsdk "github.com/coder/acp-go-sdk"
)

const serverAgentName = "antigravity-acp"

const minimumServerVersion = "1.3.0"

const cliServerAgentName = "Antigravity ACP Server"

const minimumCLIACPVersion = "1.5.4"

var versionPattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)`)

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

func isCLIServer(init acpsdk.InitializeResponse) bool {
	return init.AgentInfo != nil && init.AgentInfo.Name == cliServerAgentName
}

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
