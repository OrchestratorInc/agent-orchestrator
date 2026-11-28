package kimchiacp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
)

// Kimchi steers a running turn with its own vendor method rather than ACP's
// _session/steering. It queues the text into pi's AgentSession.steer(), which
// delivers it after the running tool calls and before the next model call, and
// it never starts a turn. Shipped in Kimchi v1.0.8 (getkimchi/kimchi#1029).
const (
	kimchiMetaKey        = "kimchi.dev"
	kimchiSteeringMethod = "_kimchi.dev/steering"
	// minimumSteerImagesVersion is the first Kimchi whose steering reads
	// `attachments`. Earlier ones queue the text and drop the images, and the
	// handshake does not say which a build does.
	minimumSteerImagesVersion = "1.2.0"
)

var steerRequest = &acpdriver.SteerRequest{
	Supported:   steeringAdvertised,
	Method:      kimchiSteeringMethod,
	Params:      steeringParams,
	TakesImages: steeringTakesImages,
	Outcome:     steeringOutcome,
}

// steeringTakesImages reads Kimchi's version from the handshake. A version it
// cannot read is treated as too old: refusing an image is recoverable, a
// silently dropped one is not.
func steeringTakesImages(init acpsdk.InitializeResponse) bool {
	if init.AgentInfo == nil {
		return false
	}
	installed, ok := parseSemver(init.AgentInfo.Version)
	if !ok {
		return false
	}
	minimum, _ := parseSemver(minimumSteerImagesVersion)
	return !installed.less(minimum)
}

// steeringAdvertised reads `agentCapabilities._meta["kimchi.dev"].steering`.
func steeringAdvertised(init acpsdk.InitializeResponse) bool {
	vendor, ok := init.AgentCapabilities.Meta[kimchiMetaKey].(map[string]any)
	if !ok {
		return false
	}
	supported, _ := vendor["steering"].(bool)
	return supported
}

// steeringParams splits the ACP prompt into Kimchi's shape: one text string
// plus image attachments. Kimchi has no field for anything else, so other
// blocks are refused rather than dropped.
func steeringParams(sessionID string, prompt []acpsdk.ContentBlock) (any, error) {
	texts := make([]string, 0, len(prompt))
	attachments := make([]acpsdk.ContentBlock, 0)
	for _, block := range prompt {
		switch {
		case block.Text != nil:
			texts = append(texts, block.Text.Text)
		case block.Image != nil:
			attachments = append(attachments, block)
		default:
			return nil, errors.New("kimchi steering takes only text and images")
		}
	}
	text := strings.Join(texts, "\n\n")
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("kimchi steering needs text")
	}
	params := map[string]any{"sessionId": sessionID, "prompt": text}
	if len(attachments) > 0 {
		params["attachments"] = attachments
	}
	return params, nil
}

func steeringOutcome(raw json.RawMessage) (acpdriver.SteerOutcome, error) {
	var response struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return 0, fmt.Errorf("decode kimchi steering response: %w", err)
	}
	switch response.Status {
	case "injected":
		return acpdriver.SteerInjected, nil
	case "promptRequired":
		return acpdriver.SteerNoRunningTurn, nil
	default:
		return 0, fmt.Errorf("kimchi steering returned unknown status %q", response.Status)
	}
}
