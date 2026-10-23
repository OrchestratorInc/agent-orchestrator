package worker

import (
	"context"
	"net"
	"net/http"
	"time"
)

// PokeCheckpoint signals the worker's checkpoint bridge (a unix socket at
// AO_CHECKPOINT_SOCKET) that a turn ended, so it captures a durable-restore
// checkpoint. It is fire-and-forget: any failure (no socket, worker down) is
// ignored so a turn end is never delayed or broken by capture.
func PokeCheckpoint(ctx context.Context, socket string) {
	if socket == "" {
		return
	}
	httpClient := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			DialContext: func(dialCtx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(dialCtx, "unix", socket)
			},
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/checkpoint", nil)
	if err != nil {
		return
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}
