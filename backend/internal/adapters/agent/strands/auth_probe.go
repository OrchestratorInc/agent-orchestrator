package strands

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

//go:embed assets/ao-auth.mjs
var nativeAuthenticationProbe string

// probeNativeAuthentication uses the same read-only authenticated catalog
// discovery as native setup, without loading an agent or generating a response.
// Inconclusive and unsupported providers never count as authorized.
func probeNativeAuthentication(ctx context.Context, binary string) (ports.AgentAuthStatus, error) {
	node, err := exec.LookPath("node")
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	probe, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := aoprocess.CommandContext(probe, node, "--input-type=module", "--eval", nativeAuthenticationProbe, binary)
	// Native errors may contain credentials or provider details. Never include
	// stdout/stderr in an error returned to callers.
	command.Stderr = io.Discard
	output, err := command.Output()
	if err != nil || len(output) > 512 {
		return ports.AgentAuthStatusUnknown, errors.New("strands: native authentication probe unavailable")
	}
	return nativeAuthenticationStatus(output)
}

func nativeAuthenticationStatus(output []byte) (ports.AgentAuthStatus, error) {
	var result struct {
		Status ports.AgentAuthStatus `json:"status"`
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return ports.AgentAuthStatusUnknown, errors.New("strands: invalid native authentication result")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return ports.AgentAuthStatusUnknown, errors.New("strands: invalid native authentication result")
	}
	switch result.Status {
	case ports.AgentAuthStatusAuthorized, ports.AgentAuthStatusUnauthorized, ports.AgentAuthStatusConfigured, ports.AgentAuthStatusUnknown:
		return result.Status, nil
	default:
		return ports.AgentAuthStatusUnknown, errors.New("strands: invalid native authentication status")
	}
}
