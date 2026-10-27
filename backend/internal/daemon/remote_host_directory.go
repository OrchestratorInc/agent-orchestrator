package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

// A quick-tunnel address changes on connector restart. The host publishes its
// current address outward; clients still reach the daemon directly, never
// through AO Cloud. A failed publish is retried on the next tick.
func runRemoteHostAddressPublisher(ctx context.Context, bridge *controllers.BridgeService, controlPlaneURL string, log *slog.Logger) {
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	lastSent, lastHash, waited := "", "", ""
	publish := func() {
		address, hash, fixed := accountTunnelAddress(bridge)
		key := address + "\x00" + hash
		if address == "" || hash == "" || key == lastSent {
			return
		}
		// Just linked, a machine on a quick tunnel is about to move to the
		// fixed address the link listed it at; publishing the quick one first
		// would overwrite that. It waits a tick to see whether it stays.
		if !fixed && hash != lastHash && key != waited {
			waited = key
			return
		}
		if err := publishRemoteHostAddress(ctx, client, controlPlaneURL, bridge.HostID, address, hash); err != nil {
			log.Warn("remote host address publish failed", "err", err)
			return
		}
		lastSent, lastHash = key, hash
	}
	publish()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			publish()
		}
	}
}

// accountTunnelAddress is the tunnel address to publish, the account token
// digest to publish it with, and whether the address is this machine's fixed
// one.
func accountTunnelAddress(bridge *controllers.BridgeService) (address, hash string, fixed bool) {
	state, err := mobilebridge.Load(bridge.ConfigPath)
	if err != nil || !state.Enabled || state.AccountTokenHash == "" || !bridge.LAN.Running() {
		return "", "", false
	}
	for _, endpoint := range bridge.AdvertisedEndpoints() {
		if endpoint.Kind == mobilebridge.KindTunnel && endpoint.Secure {
			return "https://" + net.JoinHostPort(endpoint.Host, fmt.Sprint(endpoint.Port)), state.AccountTokenHash, bridge.TunnelStatus().Fixed
		}
	}
	return "", "", false
}

func publishRemoteHostAddress(ctx context.Context, client *http.Client, controlPlaneURL, hostID, address, hash string) error {
	target, err := mobilebridge.ControlPlaneTarget(controlPlaneURL, "/api/cloud/v1/remote-hosts/"+url.PathEscape(hostID)+"/address")
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{"url": address})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+hash)
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("control plane returned %d", response.StatusCode)
	}
	return nil
}
