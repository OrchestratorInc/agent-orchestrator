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
	"golang.org/x/oauth2"

	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	probeTimeout        = 20 * time.Second
	commandProbeTimeout = 90 * time.Second
)

// Test connects to a connector, lists its tools and saves the result as its status.
func (s *Service) Test(ctx context.Context, ref string) (Connector, error) {
	rec, err := s.snapshot(ref)
	if err != nil {
		return Connector{}, err
	}
	budget := probeTimeout
	if rec.Transport == TransportStdio {
		budget = commandProbeTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	tools, step, err := s.probe(ctx, &rec)
	switch {
	case err == nil:
		return s.setStatusOf(rec.ID, &rec, StatusConnected, "", "", tools)
	case step == "sign-in":
		return s.setStatusOf(rec.ID, &rec, StatusNeedsSignIn, step, safeError(err, rec.URL), nil)
	default:
		return s.setStatusOf(rec.ID, &rec, StatusUnavailable, step, safeError(err, rec.URL), nil)
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
			HTTPClient:           &http.Client{Transport: &probeTransport{service: s, rec: rec, base: base, refused: &refused}, CheckRedirect: sameHostRedirect},
			MaxRetries:           -1,
			DisableStandaloneSSE: true,
		}
	} else {
		step = "start"
		cmd := process.CommandContext(ctx, rec.Command, rec.Args...)
		process.ConfigureTreeCancellation(cmd)
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
		var renewal *oauth2.RetrieveError
		if errors.As(err, &renewal) && renewal.ErrorCode == "invalid_grant" {
			t.refused.Store(true)
		}
		return nil, err
	}
	var resp *http.Response
	var err error
	if req.GetBody == nil && req.Body != nil && req.Body != http.NoBody {
		resp, err = t.base.RoundTrip(req)
	} else {
		resp, err = t.service.send(t.base, t.rec, req, req.GetBody)
	}
	if err == nil && asksForSignIn(resp.StatusCode, resp.Header) {
		t.refused.Store(true)
	}
	return resp, err
}
