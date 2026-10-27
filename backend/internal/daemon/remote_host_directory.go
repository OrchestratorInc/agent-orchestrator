package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	lastSent, sentAt := "", time.Time{}
	publish := func() {
		address, hash := accountTunnelAddress(bridge)
		key := address + "\x00" + hash
		// Sent again now and then: the desktop also writes the listing, so the
		// last address sent is not always the one listed.
		if address == "" || hash == "" || (key == lastSent && time.Since(sentAt) < republishEvery) {
			return
		}
		err := publishRemoteHostAddress(ctx, client, controlPlaneURL, bridge.HostID, address, hash)
		switch {
		case errors.Is(err, errAddressRefused):
			// No account lists this link, as on an AO Cloud the machine could
			// not be listed on. Asked again only later, not every tick.
		case err != nil:
			log.Warn("remote host address publish failed", "err", err)
			return
		}
		lastSent, sentAt = key, time.Now()
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

// republishEvery spaces publishes of an unchanged address.
const republishEvery = 10 * time.Minute

// errAddressRefused is AO Cloud refusing the digest: no account lists it.
var errAddressRefused = errors.New("no account lists this machine under its link")

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
	switch response.StatusCode {
	case http.StatusNoContent:
	case http.StatusUnauthorized:
		return errAddressRefused
	default:
		return fmt.Errorf("control plane returned %d", response.StatusCode)
	}
	return nil
}
