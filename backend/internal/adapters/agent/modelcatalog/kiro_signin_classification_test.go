package modelcatalog

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestKiroSignInGateRecognizesTheSignedOutPayload(t *testing.T) {
	previous := signInProbe
	t.Cleanup(func() { signInProbe = previous })
	signInProbe = func(context.Context, string, []string, string, map[string]string) ([]byte, error) {
		return []byte(`{"account":null}`), errors.New("exit status 1")
	}

	err := kiroSignIn.check(context.Background(), "kiro", "kiro-cli", "", nil)
	if !errors.Is(err, ports.ErrAgentModelDiscoverySignInRequired) {
		t.Fatalf("err = %v, want a recognized ErrAgentModelDiscoverySignInRequired", err)
	}
}

func TestKiroSignInGateAllowsASignedInPayload(t *testing.T) {
	previous := signInProbe
	t.Cleanup(func() { signInProbe = previous })
	signInProbe = func(context.Context, string, []string, string, map[string]string) ([]byte, error) {
		return []byte("{\"accountType\":\"IamIdentityCenter\",\"email\":\"user@example.com\"}\n\nProfile:\nKiroProfile-us-east-1\n"), nil
	}

	if err := kiroSignIn.check(context.Background(), "kiro", "kiro-cli", "", nil); err != nil {
		t.Fatalf("err = %v, want discovery allowed for a signed-in CLI", err)
	}
}
