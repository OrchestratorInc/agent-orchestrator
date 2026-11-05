package codexappserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestInitializeConnectionPreservesContextFailure(t *testing.T) {
	for _, tc := range []struct {
		name    string
		context func() (context.Context, context.CancelFunc)
		cancel  bool
		want    error
	}{
		{
			name: "deadline",
			context: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 100*time.Millisecond)
			},
			want: context.DeadlineExceeded,
		},
		{
			name: "cancellation",
			context: func() (context.Context, context.CancelFunc) {
				return context.WithCancel(context.Background())
			},
			cancel: true,
			want:   context.Canceled,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connection, server := newFakeAppServer(t, rejectAllServerRequests)
			ctx, cancel := tc.context()
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- initializeConnection(ctx, connection) }()

			request := server.nextRequest()
			if request.Method != "initialize" {
				t.Fatalf("method = %q, want initialize", request.Method)
			}
			// Withhold the provider response, reproducing a slow startup handshake.
			if tc.cancel {
				cancel()
			}
			select {
			case err := <-done:
				if !errors.Is(err, tc.want) {
					t.Fatalf("initialize error = %v, want %v", err, tc.want)
				}
				if errors.Is(err, ports.ErrChatDriverIncompatible) {
					t.Fatalf("initialize error = %v; a context failure is not protocol incompatibility", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("initialize did not finish after its context ended")
			}
			select {
			case frame := <-server.requests:
				t.Fatalf("sent %q after the initialize failed", frame.Method)
			default:
			}
		})
	}
}

func TestInitializeConnectionRejectsIncompatibleProtocol(t *testing.T) {
	connection, server := newFakeAppServer(t, rejectAllServerRequests)
	done := make(chan error, 1)
	go func() { done <- initializeConnection(context.Background(), connection) }()

	request := server.nextRequest()
	server.push(`{"id":` + string(*request.ID) + `,"error":{"code":-32601,"message":"method not found"}}`)
	select {
	case err := <-done:
		if !errors.Is(err, ports.ErrChatDriverIncompatible) {
			t.Fatalf("initialize error = %v, want ErrChatDriverIncompatible", err)
		}
		var providerError *rpcError
		if !errors.As(err, &providerError) || providerError.Code != -32601 {
			t.Fatalf("initialize error = %v, want provider method-not-found error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("initialize did not finish after the protocol error")
	}
}

func TestInitializeConnectionCompletesHandshake(t *testing.T) {
	connection, server := newFakeAppServer(t, rejectAllServerRequests)
	done := make(chan error, 1)
	go func() { done <- initializeConnection(context.Background(), connection) }()

	request := server.nextRequest()
	if request.Method != "initialize" || request.ID == nil {
		t.Fatalf("request = %+v, want initialize request with an id", request)
	}
	server.push(`{"id":` + string(*request.ID) + `,"result":{}}`)
	initialized := server.nextRequest()
	if initialized.Method != "initialized" || initialized.ID != nil {
		t.Fatalf("notification = %+v, want initialized notification", initialized)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("initialize: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("initialize did not finish after the handshake")
	}
}
