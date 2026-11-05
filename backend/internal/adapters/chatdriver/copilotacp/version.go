package copilotacp

import (
	"fmt"
	"strconv"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"
)

// Copilot CLI identifies itself as "Copilot" in the ACP handshake. 1.0.93 is
// the oldest build AO has verified end to end over ACP.
const (
	copilotAgentName      = "Copilot"
	minimumCopilotVersion = "1.0.93"
)

// validateInitialize admits only GitHub Copilot CLI at or above the verified
// version. It reads the version from the handshake rather than running
// `copilot --version`, which would start a second CLI process per probe.
func validateInitialize(init acpsdk.InitializeResponse) error {
	if init.AgentInfo == nil {
		return fmt.Errorf("ACP agent did not identify itself; AO requires GitHub Copilot CLI %s or newer",
			minimumCopilotVersion)
	}
	if init.AgentInfo.Name != copilotAgentName {
		return fmt.Errorf("ACP agent %q is not GitHub Copilot CLI", init.AgentInfo.Name)
	}
	installed, ok := parseVersion(init.AgentInfo.Version)
	if !ok {
		return fmt.Errorf("unrecognized GitHub Copilot CLI version %q (AO requires %s or newer)",
			init.AgentInfo.Version, minimumCopilotVersion)
	}
	minimum, _ := parseVersion(minimumCopilotVersion)
	if installed.less(minimum) {
		return fmt.Errorf("GitHub Copilot CLI %s is older than AO's tested minimum %s",
			init.AgentInfo.Version, minimumCopilotVersion)
	}
	return nil
}

type version [3]int

// parseVersion reads major.minor.patch, ignoring a pre-release or build suffix.
func parseVersion(value string) (version, bool) {
	core := strings.TrimPrefix(strings.TrimSpace(value), "v")
	if i := strings.IndexAny(core, "-+ "); i >= 0 {
		core = core[:i]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return version{}, false
	}
	var out version
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return version{}, false
		}
		out[i] = n
	}
	return out, true
}

func (v version) less(other version) bool {
	for i := range v {
		if v[i] != other[i] {
			return v[i] < other[i]
		}
	}
	return false
}
