package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	relayDrain = 5 * time.Second
	// EnvRelayURL makes Run pass its messages to this HTTP tool server.
	EnvRelayURL = "AO_MCP_RELAY_URL"
	// EnvRelayAuthorization is the Authorization header Run sends to that server.
	EnvRelayAuthorization = "AO_MCP_RELAY_AUTHORIZATION"
	// EnvLaunchURL makes Run ask the daemon at this address for a command and run it.
	EnvLaunchURL = "AO_MCP_LAUNCH_URL"
	// EnvConnectorToken is the session's connector token, used when no Authorization header is given.
	EnvConnectorToken = config.EnvConnectorToken

	maxRelayMessage = 32 << 20
	relayErrorCode  = -32000
)

type relay struct {
	target, authorization string
	client                *http.Client

	mu               sync.Mutex
	out              io.Writer
	session, version string
	start            []byte
	restarting       sync.Mutex
}

// Run serves Orchestrator's tools, or relays to the HTTP tool server the environment names.
func Run(ctx context.Context, in io.Reader, out io.Writer, version string) error {
	authorization := os.Getenv(EnvRelayAuthorization)
	if token := os.Getenv(EnvConnectorToken); authorization == "" && token != "" {
		authorization = "Bearer " + token
	}
	if target := os.Getenv(EnvLaunchURL); target != "" {
		return Launch(ctx, in, out, os.Stderr, target, authorization)
	}
	if target := os.Getenv(EnvRelayURL); target != "" {
		return Relay(ctx, in, out, target, authorization)
	}
	return Serve(ctx, in, out, NewDaemonClient(), version)
}

// Launch asks the daemon at target for the command of a connector and runs it on the given streams.
func Launch(ctx context.Context, in io.Reader, out, errOut io.Writer, target, authorization string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return err
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the connector did not start: %s", strings.TrimSpace(string(body)))
	}
	var spec struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}
	if err := json.Unmarshal(body, &spec); err != nil || spec.Command == "" {
		return errors.New("the connector did not start: the daemon gave no command")
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmd := process.CommandContext(ctx, spec.Command, spec.Args...)
	process.ConfigureTreeCancellation(cmd)
	for _, pair := range os.Environ() {
		name, _, _ := strings.Cut(pair, "=")
		if name != EnvLaunchURL && name != EnvRelayURL && name != EnvRelayAuthorization && name != EnvConnectorToken {
			cmd.Env = append(cmd.Env, pair)
		}
	}
	for name, value := range spec.Env {
		cmd.Env = append(cmd.Env, name+"="+value)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, errOut
	return cmd.Run()
}

// Relay passes MCP messages from in to the HTTP tool server at target and writes its answers on out, until in ends.
func Relay(ctx context.Context, in io.Reader, out io.Writer, target, authorization string) error {
	r := &relay{target: target, authorization: authorization, client: &http.Client{}, out: out}
	reader := bufio.NewReader(in)
	var calls sync.WaitGroup
	ctx, stop := context.WithCancel(ctx)
	defer func() {
		drained := make(chan struct{})
		go func() {
			calls.Wait()
			close(drained)
		}()
		select {
		case <-drained:
		case <-time.After(relayDrain):
		}
		stop()
		<-drained
		r.end()
	}()
	for {
		line, err := reader.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			var msg mcpRequest
			_ = json.Unmarshal(line, &msg)
			if msg.Method == "initialize" {
				r.mu.Lock()
				r.start = bytes.Clone(line)
				r.mu.Unlock()
			}
			if msg.Method == "initialize" || len(msg.ID) == 0 {
				r.pass(ctx, line, msg)
			} else {
				calls.Go(func() { r.pass(ctx, line, msg) })
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (r *relay) pass(ctx context.Context, line []byte, msg mcpRequest) {
	answered, err := r.post(ctx, line, msg)
	if err == nil || answered || ctx.Err() != nil || len(msg.ID) == 0 || msg.Method == "" {
		return
	}
	if body, encodeErr := json.Marshal(mcpResponse{JSONRPC: "2.0", ID: msg.ID, Error: &mcpError{Code: relayErrorCode, Message: err.Error()}}); encodeErr == nil {
		r.write(body, msg)
	}
}

func (r *relay) send(ctx context.Context, line []byte) (resp *http.Response, session string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.target, bytes.NewReader(line))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if r.authorization != "" {
		req.Header.Set("Authorization", r.authorization)
	}
	r.mu.Lock()
	session = r.session
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	if r.version != "" {
		req.Header.Set("Mcp-Protocol-Version", r.version)
	}
	r.mu.Unlock()
	resp, err = r.client.Do(req)
	return resp, session, err
}

func (r *relay) end() {
	r.mu.Lock()
	session, version := r.session, r.version
	r.mu.Unlock()
	if session == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, r.target, http.NoBody)
	if err != nil {
		return
	}
	req.Header.Set("Mcp-Session-Id", session)
	if version != "" {
		req.Header.Set("Mcp-Protocol-Version", version)
	}
	if r.authorization != "" {
		req.Header.Set("Authorization", r.authorization)
	}
	if resp, err := r.client.Do(req); err == nil {
		_ = resp.Body.Close()
	}
}

func (r *relay) restart(ctx context.Context, lost string) (err error) {
	r.restarting.Lock()
	defer r.restarting.Unlock()
	defer func() {
		r.mu.Lock()
		if err != nil && r.session == "" {
			r.session = lost
		}
		r.mu.Unlock()
	}()
	r.mu.Lock()
	start, current := r.start, r.session
	if current == lost {
		r.session = ""
	}
	r.mu.Unlock()
	if current != lost {
		return nil
	}
	if start == nil {
		return errors.New("the tool server dropped the session")
	}
	for _, line := range [][]byte{start, []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)} {
		resp, _, err := r.send(ctx, line)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxRelayMessage))
		_ = resp.Body.Close()
		if resp.StatusCode >= http.StatusBadRequest {
			return fmt.Errorf("the tool server answered %s", resp.Status)
		}
		if id := resp.Header.Get("Mcp-Session-Id"); id != "" {
			r.mu.Lock()
			r.session = id
			r.mu.Unlock()
		}
	}
	return nil
}

func (r *relay) post(ctx context.Context, line []byte, msg mcpRequest) (answered bool, err error) {
	resp, session, err := r.send(ctx, line)
	if err != nil {
		return false, err
	}
	if resp.StatusCode == http.StatusNotFound && session != "" && msg.Method != "initialize" && r.restart(ctx, session) == nil {
		_ = resp.Body.Close()
		if resp, _, err = r.send(ctx, line); err != nil {
			return false, err
		}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return false, fmt.Errorf("the tool server answered %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if id := resp.Header.Get("Mcp-Session-Id"); id != "" {
		r.mu.Lock()
		r.session = id
		r.mu.Unlock()
	}
	switch mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mediaType {
	case "application/json":
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxRelayMessage))
		if err != nil {
			return false, err
		}
		return r.write(body, msg), nil
	case "text/event-stream":
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64<<10), maxRelayMessage)
		var data []byte
		for scanner.Scan() {
			text := scanner.Bytes()
			if len(text) == 0 {
				answered = r.write(data, msg) || answered
				data = nil
			} else if value, ok := bytes.CutPrefix(text, []byte("data:")); ok {
				data = append(append(data, bytes.TrimPrefix(value, []byte(" "))...), '\n')
			}
		}
		answered = r.write(data, msg) || answered
		return answered, scanner.Err()
	}
	return false, nil
}

func (r *relay) write(body []byte, msg mcpRequest) (answered bool) {
	var line bytes.Buffer
	if len(bytes.TrimSpace(body)) == 0 || json.Compact(&line, body) != nil {
		return false
	}
	var reply struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	_ = json.Unmarshal(body, &reply)
	line.WriteByte('\n')
	r.mu.Lock()
	defer r.mu.Unlock()
	if msg.Method == "initialize" && reply.Result.ProtocolVersion != "" {
		r.version = reply.Result.ProtocolVersion
	}
	_, _ = r.out.Write(line.Bytes())
	return len(msg.ID) > 0 && reply.Method == "" && bytes.Equal(bytes.TrimSpace(reply.ID), bytes.TrimSpace(msg.ID))
}
