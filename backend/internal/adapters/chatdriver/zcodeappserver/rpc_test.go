package zcodeappserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAppServer stands in for `zcode app-server` over in-memory pipes, so the
// transport is tested without spawning anything.
type fakeAppServer struct {
	t        *testing.T
	requests chan frame
	toClient io.WriteCloser
}

func newFakeAppServer(t *testing.T, log *slog.Logger, onReq serverRequestHandler) (*conn, *fakeAppServer) {
	t.Helper()
	clientReads, serverWrites := io.Pipe()
	serverReads, clientWrites := io.Pipe()
	fake := &fakeAppServer{t: t, requests: make(chan frame, 64), toClient: serverWrites}
	go func() {
		br := bufio.NewReader(serverReads)
		for {
			line, err := readFrame(br)
			if err != nil {
				close(fake.requests)
				return
			}
			if len(line) == 0 {
				continue
			}
			var f frame
			if err := json.Unmarshal(line, &f); err != nil {
				continue
			}
			fake.requests <- f
		}
	}()
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	c := newConn(clientWrites, clientReads, log, onReq)
	t.Cleanup(func() {
		_ = serverWrites.Close()
		_ = c.closeWrite()
	})
	return c, fake
}

func (f *fakeAppServer) push(raw string) {
	f.t.Helper()
	if _, err := io.WriteString(f.toClient, raw+"\n"); err != nil {
		f.t.Fatalf("push frame: %v", err)
	}
}

func (f *fakeAppServer) nextRequest() frame {
	f.t.Helper()
	select {
	case got, ok := <-f.requests:
		if !ok {
			f.t.Fatal("client closed before sending the expected frame")
		}
		return got
	case <-time.After(3 * time.Second):
		f.t.Fatal("timed out waiting for a client frame")
		return frame{}
	}
}

func refuseAll(context.Context, serverRequest) (any, error) {
	return nil, errors.New("refused")
}

func TestNumericIDsCorrelate(t *testing.T) {
	c, fake := newFakeAppServer(t, nil, refuseAll)
	type result struct {
		Accepted bool `json:"accepted"`
	}
	done := make(chan error, 1)
	var out result
	go func() { done <- c.request(context.Background(), "session/send", map[string]any{"content": "x"}, &out) }()
	req := fake.nextRequest()
	if req.Method != "session/send" || req.ID == nil || rawID(*req.ID) != "1" {
		t.Fatalf("request = %+v, want numeric id 1", req)
	}
	// A response for an id we never sent is dropped; ours is delivered.
	fake.push(`{"id":99,"result":{"accepted":false}}`)
	fake.push(`{"id":1,"result":{"accepted":true}}`)
	if err := <-done; err != nil {
		t.Fatalf("request: %v", err)
	}
	if !out.Accepted {
		t.Fatal("result not decoded")
	}
}

func TestServerStringIDsRouteToAnswer(t *testing.T) {
	handled := make(chan serverRequest, 1)
	c, fake := newFakeAppServer(t, nil, func(_ context.Context, req serverRequest) (any, error) {
		handled <- req
		return map[string]any{"nativeSearchEnhancementsEnabled": true}, nil
	})
	fake.push(`{"id":"server-1","method":"session/requestRuntimePreferences","params":{"sessionId":"s"}}`)
	req := <-handled
	if req.Method != "session/requestRuntimePreferences" || rawID(req.ID) != "server-1" {
		t.Fatalf("handler got %+v", req)
	}
	reply := fake.nextRequest()
	if reply.ID == nil || string(*reply.ID) != `"server-1"` || reply.Method != "" {
		t.Fatalf("reply = %+v, want the string id echoed", reply)
	}
	_ = c
}

func TestProtocolRejectionsAreLoggedAtErrorAndDoNotKillTheLoop(t *testing.T) {
	var buf strings.Builder
	var mu sync.Mutex
	log := slog.New(slog.NewTextHandler(lockedWriter{&mu, &buf}, &slog.HandlerOptions{Level: slog.LevelError}))
	c, fake := newFakeAppServer(t, log, refuseAll)

	fake.push(`{"id":"invalid-message","error":{"code":-32600,"message":"Invalid message","data":{"issues":[{"path":["params","model"],"message":"Unrecognized key"}]}}}`)
	fake.push(`{"id":"parse-error","error":{"code":-32700,"message":"Parse error"}}`)

	// The loop is alive: a request still round-trips.
	done := make(chan error, 1)
	go func() { done <- c.request(context.Background(), "session/stop", nil, nil) }()
	req := fake.nextRequest()
	fake.push(`{"id":` + string(*req.ID) + `,"result":{}}`)
	if err := <-done; err != nil {
		t.Fatalf("request after rejections: %v", err)
	}

	mu.Lock()
	logged := buf.String()
	mu.Unlock()
	if !strings.Contains(logged, "level=ERROR") || !strings.Contains(logged, "invalid-message") || !strings.Contains(logged, "Unrecognized key") {
		t.Fatalf("rejections not logged at Error with data: %q", logged)
	}
	if !strings.Contains(logged, "parse-error") {
		t.Fatalf("parse-error not logged: %q", logged)
	}
}

type lockedWriter struct {
	mu  *sync.Mutex
	buf *strings.Builder
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func TestTwoAnswersParkedOnOneHandlerEachWriteTheirOwnReply(t *testing.T) {
	release := make(chan struct{})
	c, fake := newFakeAppServer(t, nil, func(ctx context.Context, req serverRequest) (any, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return map[string]any{"decision": "allow"}, nil
	})
	fake.push(`{"id":"server-3","method":"interaction/requestPermission","params":{"requestId":"r1"}}`)
	fake.push(`{"id":"server-4","method":"interaction/requestPermission","params":{"requestId":"r1"}}`)
	time.Sleep(20 * time.Millisecond)
	close(release)

	ids := map[string]string{}
	for i := 0; i < 2; i++ {
		reply := fake.nextRequest()
		if reply.Error != nil || reply.ID == nil {
			t.Fatalf("reply = %+v", reply)
		}
		ids[rawID(*reply.ID)] = string(reply.Result)
	}
	if ids["server-3"] != `{"decision":"allow"}` || ids["server-4"] != `{"decision":"allow"}` {
		t.Fatalf("replies = %v, want one per frame id", ids)
	}
	_ = c
}

func TestHandlerRPCErrorKeepsItsCode(t *testing.T) {
	c, fake := newFakeAppServer(t, nil, func(context.Context, serverRequest) (any, error) {
		return nil, &rpcError{Code: -32601, Message: "Method not found"}
	})
	fake.push(`{"id":"server-5","method":"anything","params":{}}`)
	reply := fake.nextRequest()
	if reply.Error == nil || reply.Error.Code != -32601 {
		t.Fatalf("reply = %+v, want the handler's -32601", reply)
	}
	_ = c
}

func TestLargeFrameParses(t *testing.T) {
	c, fake := newFakeAppServer(t, nil, refuseAll)
	done := make(chan error, 1)
	var out struct {
		Response string `json:"response"`
	}
	go func() { done <- c.request(context.Background(), "session/read", nil, &out) }()
	fake.nextRequest()
	big := strings.Repeat("x", 2<<20)
	fake.push(`{"id":1,"result":{"response":"` + big + `"}}`)
	if err := <-done; err != nil {
		t.Fatalf("request: %v", err)
	}
	if len(out.Response) != len(big) {
		t.Fatalf("response truncated to %d bytes", len(out.Response))
	}
}

func TestProcessExitUnblocksPendingRequest(t *testing.T) {
	c, fake := newFakeAppServer(t, nil, refuseAll)
	done := make(chan error, 1)
	go func() { done <- c.request(context.Background(), "session/send", nil, nil) }()
	fake.nextRequest()
	if err := fake.toClient.Close(); err != nil {
		t.Fatalf("close server side: %v", err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrConnClosed) {
			t.Fatalf("err = %v, want ErrConnClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request did not fail after the process exited")
	}
	select {
	case <-c.wait():
	case <-time.After(time.Second):
		t.Fatal("done never closed")
	}
}
