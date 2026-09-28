package fx

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAuthStatusParsesDocumentedFXFields(t *testing.T) {
	tests := []struct {
		name string
		json string
		want ports.AgentAuthStatus
	}{
		{name: "missing", json: `{"auth":"missing","auth_expired":false}`, want: ports.AgentAuthStatusUnauthorized},
		{name: "expired named source", json: `{"auth":"vercel","auth_expired":true}`, want: ports.AgentAuthStatusUnauthorized},
		{name: "named source is configured", json: `{"auth":"vercel","auth_expired":false}`, want: ports.AgentAuthStatusConfigured},
		{name: "another named source is configured", json: `{"auth":"api-key"}`, want: ports.AgentAuthStatusConfigured},
		{name: "unknown shape", json: `{"version":"0.0.9"}`, want: ports.AgentAuthStatusUnknown},
		{name: "malformed", json: `{"auth":`, want: ports.AgentAuthStatusUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := authStatusFromJSON([]byte(tc.json))
			if got != tc.want {
				t.Fatalf("status = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAuthStatusParsesFXModelResponses(t *testing.T) {
	tests := []struct {
		name string
		json string
		want ports.AgentAuthStatus
	}{
		{name: "models returned", json: `{"kind":"models","count":4,"ids":["gpt-6-luna","gpt-5.6-terra"]}`, want: ports.AgentAuthStatusAuthorized},
		{name: "empty model catalog is still authenticated", json: `{"kind":"models","count":0,"ids":[]}`, want: ports.AgentAuthStatusAuthorized},
		{name: "authentication rejected code", json: `{"kind":"models","error":"could not list models: AuthenticationRejected","code":"AuthenticationRejected"}`, want: ports.AgentAuthStatusUnauthorized},
		{name: "authentication rejected error", json: `{"kind":"models","error":"could not list models: AuthenticationRejected"}`, want: ports.AgentAuthStatusUnauthorized},
		{name: "unknown shape", json: `{"version":"0.0.9"}`, want: ports.AgentAuthStatusUnknown},
		{name: "malformed", json: `{"auth":`, want: ports.AgentAuthStatusUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := authStatusFromModelsJSON([]byte(tc.json))
			if got != tc.want {
				t.Fatalf("status = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAuthStatusStopsWhenCredentialsAreMissing(t *testing.T) {
	var gotName string
	var gotArgs []string
	plugin := &Plugin{
		resolvedBinary: "/opt/fx/bin/fx",
		statusRunner: func(_ context.Context, name string, args ...string) ([]byte, error) {
			gotName = name
			gotArgs = append([]string(nil), args...)
			return []byte(`{"auth":"missing"}`), nil
		},
	}

	status, err := plugin.AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status != ports.AgentAuthStatusUnauthorized {
		t.Fatalf("status = %q, want unauthorized", status)
	}
	if gotName != "/opt/fx/bin/fx" || !reflect.DeepEqual(gotArgs, []string{"status", "--json"}) {
		t.Fatalf("command = %q %#v, want /opt/fx/bin/fx [status --json]", gotName, gotArgs)
	}
}

func TestAuthStatusAuthorizesSuccessfulModelListing(t *testing.T) {
	var calls [][]string
	plugin := &Plugin{
		resolvedBinary: "fx",
		statusRunner: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			calls = append(calls, append([]string(nil), args...))
			if reflect.DeepEqual(args, []string{"status", "--json"}) {
				return []byte(`{"auth":"vercel","auth_expired":false}`), nil
			}
			return []byte(`{"kind":"models","count":4,"shown_count":4,"ids":["gpt-6-luna","gpt-5.6-terra","gpt-5.6-luna","gpt-5.5"]}`), nil
		},
	}

	status, err := plugin.AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = %q, want authorized", status)
	}
	wantCalls := [][]string{{"status", "--json"}, {"models", "--json"}}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("commands = %#v, want %#v", calls, wantCalls)
	}
}

func TestAuthStatusKeepsConfiguredWhenModelVerificationIsInconclusive(t *testing.T) {
	plugin := &Plugin{
		resolvedBinary: "fx",
		statusRunner: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if reflect.DeepEqual(args, []string{"status", "--json"}) {
				return []byte(`{"auth":"vercel","auth_expired":false}`), nil
			}
			return []byte("fx failed"), errors.New("exit status 1")
		},
	}

	status, err := plugin.AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status != ports.AgentAuthStatusConfigured {
		t.Fatalf("status = %q, want configured", status)
	}
}

func TestAuthStatusRejectsCredentialRejectedByModelListing(t *testing.T) {
	plugin := &Plugin{
		resolvedBinary: "fx",
		statusRunner: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if reflect.DeepEqual(args, []string{"status", "--json"}) {
				return []byte(`{"auth":"vercel","auth_expired":false}`), nil
			}
			return []byte(`{"kind":"models","error":"could not list models: AuthenticationRejected","code":"AuthenticationRejected"}`), errors.New("exit status 1")
		},
	}

	status, err := plugin.AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status != ports.AgentAuthStatusUnauthorized {
		t.Fatalf("status = %q, want unauthorized", status)
	}
}

func TestAuthStatusPropagatesCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	plugin := &Plugin{
		resolvedBinary: "fx",
		statusRunner: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			called = true
			return nil, nil
		},
	}

	status, err := plugin.AuthStatus(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if status != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = %q, want unknown", status)
	}
	if called {
		t.Fatal("status command ran after caller cancellation")
	}
}

func TestAuthStatusTimeoutDegradesToUnknown(t *testing.T) {
	plugin := &Plugin{
		resolvedBinary: "fx",
		statusTimeout:  10 * time.Millisecond,
		statusRunner: func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	status, err := plugin.AuthStatus(context.Background())
	if err != nil {
		t.Fatalf("error = %v, want nil for adapter timeout", err)
	}
	if status != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = %q, want unknown", status)
	}
}

func TestAuthStatusUnknownOnCommandFailure(t *testing.T) {
	plugin := &Plugin{
		resolvedBinary: "fx",
		statusRunner: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("fx failed"), errors.New("exit status 1")
		},
	}

	status, err := plugin.AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = %q, want unknown", status)
	}
}
