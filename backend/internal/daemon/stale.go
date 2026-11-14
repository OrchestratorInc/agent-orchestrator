package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/daemonmeta"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

const staleProbeTimeout = 2 * time.Second

func runFileOwnerServing(client *http.Client, host string, info *runfile.Info) bool {
	if info == nil || info.Port <= 0 {
		return false
	}

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s:%d/healthz", host, info.Port), http.NoBody)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}

	var body struct {
		Service string `json:"service"`
		PID     int    `json:"pid"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false
	}
	return body.Service == daemonmeta.ServiceName && body.PID == info.PID
}
