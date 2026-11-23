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
	"strings"
	"sync"
)

const (
	// EnvRelayURL makes Run pass its messages to this HTTP tool server.
	EnvRelayURL = "AO_MCP_RELAY_URL"
	// EnvRelayAuthorization is the Authorization header Run sends to that server.
	EnvRelayAuthorization = "AO_MCP_RELAY_AUTHORIZATION"

	maxRelayMessage = 32 << 20
	relayErrorCode  = -32000
)

type relay struct {
	target, authorization string
	client                *http.Client

	mu               sync.Mutex
	out              io.Writer
	session, version string
}

// Run serves Orchestrator's tools, or relays to the HTTP tool server the environment names.
func Run(ctx context.Context, in io.Reader, out io.Writer, version string) error {
	if target := os.Getenv(EnvRelayURL); target != "" {
		return Relay(ctx, in, out, target, os.Getenv(EnvRelayAuthorization))
	}
	return Serve(ctx, in, out, NewDaemonClient(), version)
}

// Relay passes MCP messages from in to the HTTP tool server at target and writes its answers on out, until in ends.
func Relay(ctx context.Context, in io.Reader, out io.Writer, target, authorization string) error {
	r := &relay{target: target, authorization: authorization, client: &http.Client{}, out: out}
	reader := bufio.NewReader(in)
	var calls sync.WaitGroup
	defer calls.Wait()
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	for {
		line, err := reader.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			var msg mcpRequest
			_ = json.Unmarshal(line, &msg)
			if msg.Method == "initialize" {
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
	err := r.post(ctx, line, msg)
	if err == nil || ctx.Err() != nil || len(msg.ID) == 0 || msg.Method == "" {
		return
	}
	if body, encodeErr := json.Marshal(mcpResponse{JSONRPC: "2.0", ID: msg.ID, Error: &mcpError{Code: relayErrorCode, Message: err.Error()}}); encodeErr == nil {
		r.write(body, msg)
	}
}

func (r *relay) post(ctx context.Context, line []byte, msg mcpRequest) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.target, bytes.NewReader(line))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if r.authorization != "" {
		req.Header.Set("Authorization", r.authorization)
	}
	r.mu.Lock()
	if r.session != "" {
		req.Header.Set("Mcp-Session-Id", r.session)
	}
	if r.version != "" {
		req.Header.Set("Mcp-Protocol-Version", r.version)
	}
	r.mu.Unlock()
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("the tool server answered %s: %s", resp.Status, strings.TrimSpace(string(body)))
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
			return err
		}
		r.write(body, msg)
	case "text/event-stream":
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64<<10), maxRelayMessage)
		var data []byte
		for scanner.Scan() {
			text := scanner.Bytes()
			if len(text) == 0 {
				r.write(data, msg)
				data = nil
			} else if value, ok := bytes.CutPrefix(text, []byte("data:")); ok {
				data = append(append(data, bytes.TrimPrefix(value, []byte(" "))...), '\n')
			}
		}
		r.write(data, msg)
		return scanner.Err()
	}
	return nil
}

func (r *relay) write(body []byte, msg mcpRequest) {
	var line bytes.Buffer
	if len(bytes.TrimSpace(body)) == 0 || json.Compact(&line, body) != nil {
		return
	}
	var reply struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	line.WriteByte('\n')
	r.mu.Lock()
	defer r.mu.Unlock()
	if msg.Method == "initialize" && json.Unmarshal(body, &reply) == nil && reply.Result.ProtocolVersion != "" {
		r.version = reply.Result.ProtocolVersion
	}
	_, _ = r.out.Write(line.Bytes())
}
