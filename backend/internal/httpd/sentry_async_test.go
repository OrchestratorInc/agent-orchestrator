package httpd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/sentryobs"

	"github.com/getsentry/sentry-go"
)

type blockedHTTPTransport struct {
	events  chan *sentry.Event
	release chan struct{}
}

func (*blockedHTTPTransport) Configure(sentry.ClientOptions)        {}
func (*blockedHTTPTransport) Flush(time.Duration) bool              { return true }
func (*blockedHTTPTransport) FlushWithContext(context.Context) bool { return true }
func (*blockedHTTPTransport) Close()                                {}
func (transport *blockedHTTPTransport) SendEvent(event *sentry.Event) {
	transport.events <- event
	<-transport.release
}

func TestRequestLoggerDoesNotWaitForSentryAndBoundsSends(t *testing.T) {
	transport := &blockedHTTPTransport{events: make(chan *sentry.Event, 4), release: make(chan struct{})}
	if err := sentryobs.Init(sentryobs.Config{DSN: "https://public@example.com/1", Transport: transport}); err != nil {
		t.Fatal(err)
	}
	sentryobs.SetPolicyCheck(func(context.Context) bool { return true })
	t.Cleanup(func() {
		sentryobs.SetPolicyCheck(nil)
		close(transport.release)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := sentryobs.Drain(ctx); err != nil {
			t.Error(err)
		}
	})
	handler := requestLogger(slog.New(slog.NewTextHandler(io.Discard, nil)), nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { envelope.WriteError(w, r, errors.New("private failure")) }))
	request := func() {
		t.Helper()
		done := make(chan struct{})
		rec := httptest.NewRecorder()
		go func() {
			defer close(done)
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/private/session", nil))
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("HTTP response blocked on Sentry")
		}
		if rec.Code != 500 {
			t.Fatalf("status = %d", rec.Code)
		}
	}
	for range 4 {
		request()
		select {
		case event := <-transport.events:
			if event.Tags["operation"] != "http_request" {
				t.Fatalf("lost HTTP metadata: %v", event.Tags)
			}
		case <-time.After(time.Second):
			t.Fatal("no admitted event")
		}
	}
	request()
	sentryobs.SetPolicyCheck(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := sentryobs.Drain(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("opt-out did not wait for admitted HTTP sends: %v", err)
	}
	select {
	case event := <-transport.events:
		t.Fatalf("saturated send was queued: %v", event)
	default:
	}
}
