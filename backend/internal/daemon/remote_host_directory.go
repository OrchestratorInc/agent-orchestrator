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
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

// A quick-tunnel address changes on connector restart. The host publishes its
// current address outward; clients still reach the daemon directly, never
// through AO Cloud. A failed publish is retried on the next tick.
func runRemoteHostAddressPublisher(ctx context.Context, bridge *controllers.BridgeService, controlPlaneURL string, log *slog.Logger) {
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	lastSent := ""
	publish := func() {
		address, hash := accountTunnelAddress(bridge)
		key := address + "\x00" + hash
		if address == "" || hash == "" || key == lastSent {
			return
		}
		if err := publishRemoteHostAddress(ctx, client, controlPlaneURL, bridge.HostID, address, hash); err != nil {
			log.Warn("remote host address publish failed", "err", err)
			return
		}
		lastSent = key
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

func accountTunnelAddress(bridge *controllers.BridgeService) (string, string) {
	state, err := mobilebridge.Load(bridge.ConfigPath)
	if err != nil || !state.Enabled || state.AccountTokenHash == "" || !bridge.LAN.Running() {
		return "", ""
	}
	for _, endpoint := range bridge.AdvertisedEndpoints() {
		if endpoint.Kind == mobilebridge.KindTunnel && endpoint.Secure {
			return "https://" + net.JoinHostPort(endpoint.Host, fmt.Sprint(endpoint.Port)), state.AccountTokenHash
		}
	}
	return "", ""
}

func publishRemoteHostAddress(ctx context.Context, client *http.Client, controlPlaneURL, hostID, address, hash string) error {
	base, err := url.Parse(controlPlaneURL)
	if err != nil {
		return fmt.Errorf("invalid control-plane URL")
	}
	localHTTP := base.Scheme == "http" && (base.Hostname() == "localhost" || base.Hostname() == "127.0.0.1")
	if base.Scheme != "https" && !localHTTP {
		return fmt.Errorf("invalid control-plane URL")
	}
	body, _ := json.Marshal(map[string]string{"url": address})
	target := strings.TrimRight(controlPlaneURL, "/") + "/api/cloud/v1/remote-hosts/" + url.PathEscape(hostID) + "/address"
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
