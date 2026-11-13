package hermesacp

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
)

// hermesAgentName is the agentInfo.name Hermes' ACP server reports.
const hermesAgentName = "hermes-agent"

// minimumSteerVersion is the oldest Hermes whose `/steer` AO has checked: sent
// while a turn runs, Hermes answers it at once and hands the text to the
// running turn's next step (agent.steer). 0.22.4 and 0.22.5 have the same
// command; both were tested live.
const minimumSteerVersion = "0.22.4"

// Hermes' replies to `/steer` (acp_adapter/commands.py, _cmd_steer).
const (
	steerQueuedPrefix   = "⏩ Steer queued for the active turn:"
	steerFailedPrefix   = "⚠️ Steer failed:"
	steerNoTurnPrefix   = "No active turn — queued for the next turn."
	steerCommandPrefix  = "/steer "
	steerTextOnlyReason = "only text can steer a running Hermes turn"
)

// steerPrompt steers a running Hermes turn with its `/steer` command. Hermes
// answers the command at once with a note and delivers the text to the turn
// after its current tool batch, so the running prompt keeps the turn. AO hides
// the note: the steered message already shows it. A plain second prompt would
// instead redirect the turn, cancelling the model request in flight.
var steerPrompt = &acpdriver.SteerPrompt{
	Supported: steeringSupported,
	Prompt:    steerCommand,
	Ack:       steerAcknowledgement,
}

// steeringSupported offers Steer to Hermes from minimumSteerVersion. Builds
// without install provenance, such as Homebrew's 0.22.4, report no version in
// the handshake ("0.0.0", or text that is not a version); they are offered it
// too, and an answer to `/steer` that is not Hermes' acknowledgement counts as
// a refusal (see acp.SteerPrompt.Ack).
func steeringSupported(init acpsdk.InitializeResponse) bool {
	if init.AgentInfo == nil || init.AgentInfo.Name != hermesAgentName {
		return false
	}
	installed, ok := parseVersion(init.AgentInfo.Version)
	if !ok || installed == (version{}) {
		return true
	}
	minimum, _ := parseVersion(minimumSteerVersion)
	return !installed.less(minimum)
}

// steerCommand turns the guidance into `/steer <text>`. The command is text, so
// any other block is refused rather than dropped.
func steerCommand(prompt []acpsdk.ContentBlock) ([]acpsdk.ContentBlock, error) {
	parts := make([]string, 0, len(prompt))
	for _, block := range prompt {
		if block.Text == nil {
			return nil, errors.New(steerTextOnlyReason)
		}
		parts = append(parts, block.Text.Text)
	}
	text := strings.TrimSpace(strings.Join(parts, "\n\n"))
	if text == "" {
		return nil, errors.New("steer message text is empty")
	}
	return []acpsdk.ContentBlock{acpsdk.TextBlock(steerCommandPrefix + text)}, nil
}

// steerAcknowledgement recognises Hermes' reply to `/steer`. "No active turn"
// means the turn ended just before the command arrived: Hermes then runs the
// text as its next prompt inside the same `/steer` request. The persistent
// host keeps AO's turn open until that request answers, so the run is part of
// this turn and the steer took.
func steerAcknowledgement(text string) (bool, error) {
	trimmed := strings.TrimSpace(text)
	switch {
	case strings.HasPrefix(trimmed, steerQueuedPrefix), strings.HasPrefix(trimmed, steerNoTurnPrefix):
		return true, nil
	case strings.HasPrefix(trimmed, steerFailedPrefix):
		reason := strings.TrimSpace(strings.TrimPrefix(trimmed, steerFailedPrefix))
		if reason == "" {
			reason = "no reason given"
		}
		return true, errors.New(reason)
	default:
		return false, nil
	}
}

var versionPattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)`)

type version [3]int

func parseVersion(raw string) (version, bool) {
	match := versionPattern.FindStringSubmatch(strings.TrimPrefix(strings.TrimSpace(raw), "v"))
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
