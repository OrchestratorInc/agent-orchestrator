package mobilebridge

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const tailscaleTimeout = 3 * time.Second

type TailscaleRunner func(ctx context.Context, args ...string) ([]byte, error)

func execTailscale(ctx context.Context, args ...string) ([]byte, error) {
	return aoprocess.CommandContext(ctx, "tailscale", args...).Output()
}

type TailscaleInfo struct {
	Name         string
	CertsEnabled bool
}

func QueryTailscale(ctx context.Context) TailscaleInfo {
	return queryTailscale(ctx, execTailscale)
}

func queryTailscale(ctx context.Context, run TailscaleRunner) TailscaleInfo {
	ctx, cancel := context.WithTimeout(ctx, tailscaleTimeout)
	defer cancel()
	out, err := run(ctx, "status", "--json")
	if err != nil {
		return TailscaleInfo{}
	}
	var parsed struct {
		Self        *struct{ DNSName string } `json:"Self"`
		CertDomains []string                  `json:"CertDomains"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil || parsed.Self == nil {
		return TailscaleInfo{}
	}
	name := strings.TrimSuffix(parsed.Self.DNSName, ".")
	if name == "" {
		return TailscaleInfo{}
	}
	return TailscaleInfo{Name: name, CertsEnabled: len(parsed.CertDomains) > 0}
}
