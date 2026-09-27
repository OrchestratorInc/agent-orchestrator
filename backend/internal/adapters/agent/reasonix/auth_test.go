package reasonix

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAuthenticationEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       ports.AgentAuthStatus
	}{
		{"default configured", `{"providers":[{"name":"deepseek","is_default":true,"key_present":true}]}`, ports.AgentAuthStatusConfigured},
		{"other configured", `{"providers":[{"name":"other","is_default":false,"key_present":true},{"is_default":true,"key_present":false}]}`, ports.AgentAuthStatusUnknown},
		{"anonymous provider", `{"providers":[{"is_default":true,"key_present":false}]}`, ports.AgentAuthStatusUnknown},
		{"missing key field", `{"providers":[{"is_default":true}]}`, ports.AgentAuthStatusUnknown},
		{"unknown schema", `{"authenticated":true}`, ports.AgentAuthStatusUnknown},
		{"malformed", `not json`, ports.AgentAuthStatusUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := compatiblePlugin()
			baseProbe := p.probe
			p.probe = func(ctx context.Context, binary string, args ...string) ([]byte, error) {
				if args[0] == "doctor" {
					return []byte(tc.body), nil
				}
				return baseProbe(ctx, binary, args...)
			}
			got, _ := p.AuthStatus(t.Context())
			if got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
	p := compatiblePlugin()
	p.probe = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("secret credential") }
	got, err := p.AuthStatus(t.Context())
	if got != ports.AgentAuthStatusUnknown || err == nil || strings.Contains(err.Error(), "secret credential") {
		t.Fatalf("status=%s err=%v", got, err)
	}
}
