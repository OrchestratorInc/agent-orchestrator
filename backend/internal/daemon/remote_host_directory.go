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
	lastSent, nextAt, refusedWait := "", time.Time{}, refusedRetry
	publish := func() {
		address, hash := accountTunnelAddress(bridge)
		key := address + "\x00" + hash
		// Sent again now and then: the desktop also writes the listing, so the
		// last address sent is not always the one listed.
		if address == "" || hash == "" || (key == lastSent && time.Now().Before(nextAt)) {
			return
		}
		err := publishRemoteHostAddress(ctx, client, controlPlaneURL, bridge.HostID, address, hash, bridge.ConfigPath)
		switch {
		case errors.Is(err, errAddressRefused):
			// No account lists this link yet: a device that just took the
			// machine has not listed it, or never could. Asked again soon,
			// then less often, without a warning every tick.
			if key != lastSent {
				refusedWait = refusedRetry
			}
			lastSent, nextAt = key, time.Now().Add(refusedWait)
			refusedWait = min(2*refusedWait, maxRefusedRetry)
		case err != nil:
			log.Warn("remote host address publish failed", "err", err)
		default:
			lastSent, nextAt, refusedWait = key, time.Now().Add(republishEvery), refusedRetry
		}
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

// republishEvery spaces publishes of an unchanged address. One AO Cloud
// refused is tried again after refusedRetry, doubling to maxRefusedRetry.
const (
	republishEvery  = 10 * time.Minute
	refusedRetry    = time.Minute
	maxRefusedRetry = 30 * time.Minute
)

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

func publishRemoteHostAddress(ctx context.Context, client *http.Client, controlPlaneURL, hostID, address, hash, statePath string) error {
	target, err := mobilebridge.ControlPlaneTarget(controlPlaneURL, "/api/cloud/v1/remote-hosts/"+url.PathEscape(hostID)+"/address")
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{"url": address})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	response, err := mobilebridge.DoHostRequest(client, req, body, hash, statePath)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	switch {
	case response.StatusCode == http.StatusNoContent:
	case mobilebridge.IsHostDigestRefusal(response):
		return errAddressRefused
	default:
		return fmt.Errorf("control plane returned %d", response.StatusCode)
	}
	return nil
}
