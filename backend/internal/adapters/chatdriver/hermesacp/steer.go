package hermesacp

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
)

const hermesAgentName = "hermes-agent"

const minimumSteerVersion = "0.22.4"

const (
	steerQueuedPrefix   = "⏩ Steer queued for the active turn:"
	steerFailedPrefix   = "⚠️ Steer failed:"
	steerNoTurnPrefix   = "No active turn — queued for the next turn."
	steerCommandPrefix  = "/steer "
	steerTextOnlyReason = "only text can steer a running Hermes turn"
)

var steerPrompt = &acpdriver.SteerPrompt{
	Supported: steeringSupported,
	Prompt:    steerCommand,
	Ack:       steerAcknowledgement,
}

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
