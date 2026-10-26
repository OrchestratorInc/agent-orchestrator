package config

import (
	"strings"
	"testing"
)

func TestHostTunnelsLoadAllOrNothing(t *testing.T) {
	setArtifactTestEnv(t)
	cfg, err := Load()
	if err != nil || cfg.HostTunnelsEnabled() {
		t.Fatalf("default = %v, %v", cfg.HostTunnelsEnabled(), err)
	}
	t.Setenv("AO_CLOUD_HOST_TUNNEL_ACCOUNT_ID", "acct")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "set together") {
		t.Fatalf("partial config error = %v", err)
	}
	t.Setenv("AO_CLOUD_HOST_TUNNEL_ZONE_ID", "zone")
	t.Setenv("AO_CLOUD_HOST_TUNNEL_API_TOKEN", "token")
	t.Setenv("AO_CLOUD_HOST_TUNNEL_ZONE", "https://useao.dev")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "zone apex") {
		t.Fatalf("bad zone error = %v", err)
	}
	t.Setenv("AO_CLOUD_HOST_TUNNEL_ZONE", "UseAO.dev")
	cfg, err = Load()
	if err != nil || !cfg.HostTunnelsEnabled() || cfg.HostTunnelZone != "useao.dev" {
		t.Fatalf("config = %+v, %v", cfg, err)
	}
}
