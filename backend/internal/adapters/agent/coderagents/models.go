package coderagents

import (
	"context"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/coderagents/coderapi"
)

// DiscoverModels lists the enabled model configs of the organization Coder
// Agents uses for env. binary is the already-resolved coder CLI, used only
// when the session token has to be read through `coder login token`.
func DiscoverModels(ctx context.Context, binary string, env map[string]string) ([]coderapi.ChatModel, error) {
	p := New()
	if binary = strings.TrimSpace(binary); binary != "" {
		p.resolvedBinary = binary
	}
	return p.Models(ctx, env)
}

// Models lists the enabled model configs for env's organization.
func (p *Plugin) Models(ctx context.Context, env map[string]string) ([]coderapi.ChatModel, error) {
	client, _, err := p.Connect(ctx, env)
	if err != nil {
		return nil, err
	}
	requestCtx, cancel := coderapi.WithRequestTimeout(ctx)
	defer cancel()
	org, err := coderapi.ResolveOrganization(requestCtx, client, Organization(env))
	if err != nil {
		return nil, err
	}
	models, err := client.ChatModels(requestCtx, org.ID)
	if err != nil {
		return nil, err
	}
	return coderapi.EnabledModels(models), nil
}

// DiscoveryFingerprint names the inputs a model discovery reads, without the
// session token: the deployment URL source and the organization selector.
func DiscoveryFingerprint(env map[string]string) string {
	getenv := envLookup(env)
	return "url=" + strings.TrimSpace(getenv(coderapi.EnvURL)) +
		";config=" + coderapi.ConfigDir(getenv) +
		";org=" + Organization(env)
}
