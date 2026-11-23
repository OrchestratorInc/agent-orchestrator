package connectors

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const probeTimeout = 20 * time.Second

// Test connects to a connector, lists its tools and saves the result as its status.
func (s *Service) Test(ctx context.Context, ref string) (Connector, error) {
	rec, err := s.snapshot(ref)
	if err != nil {
		return Connector{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	tools, step, err := s.probe(ctx, &rec)
	switch {
	case err == nil:
		return s.setStatus(rec.ID, StatusConnected, "", "", tools)
	case step == "sign-in":
		return s.setStatus(rec.ID, StatusNeedsSignIn, step, safeError(err, rec.URL), nil)
	default:
		return s.setStatus(rec.ID, StatusUnavailable, step, safeError(err, rec.URL), nil)
	}
}

func (s *Service) probe(ctx context.Context, rec *record) (tools []string, step string, err error) {
	var transport mcp.Transport
	var refused atomic.Bool
	step = "reach"
	if rec.Transport == TransportHTTP {
		base := s.http.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		transport = &mcp.StreamableClientTransport{
			Endpoint:             rec.URL,
			HTTPClient:           &http.Client{Transport: &probeTransport{service: s, rec: rec, base: base, refused: &refused}},
			MaxRetries:           -1,
			DisableStandaloneSSE: true,
		}
	} else {
		step = "start"
		cmd := process.CommandContext(ctx, rec.Command, rec.Args...)
		cmd.Env = os.Environ()
		for name, value := range rec.Env {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
		transport = &mcp.CommandTransport{Command: cmd}
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "orchestrator", Version: "1"}, nil).Connect(ctx, transport, nil)
	if err != nil {
		return nil, failedStep(step, err, refused.Load()), err
	}
	defer func() { _ = session.Close() }()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, failedStep("list tools", err, refused.Load()), err
	}
	tools = make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		tools = append(tools, tool.Name)
	}
	return tools, "", nil
}

func failedStep(step string, err error, refused bool) string {
	var dial *net.OpError
	var missing *exec.Error
	switch {
	case refused:
		return "sign-in"
	case errors.As(err, &dial), errors.As(err, &missing), errors.Is(err, os.ErrNotExist):
		return step
	case step == "list tools":
		return step
	default:
		return "initialize"
	}
}

type probeTransport struct {
	service *Service
	rec     *record
	base    http.RoundTripper
	refused *atomic.Bool
}

func (t *probeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if err := t.service.authorize(req, t.rec); err != nil {
		t.refused.Store(true)
		return nil, err
	}
	resp, err := t.base.RoundTrip(req)
	if err == nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
		t.refused.Store(true)
	}
	return resp, err
}
