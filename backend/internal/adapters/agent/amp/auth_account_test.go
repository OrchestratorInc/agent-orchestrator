package amp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
)

func TestAmpActiveEmailFromList(t *testing.T) {
	list := "  other@example.com (user_02)\n* dev@example.com (user_01KWBSAFBZR63FSA0NQXJYVRQ8)\n"
	if got := ampActiveEmailFromList(list); got != "dev@example.com" {
		t.Fatalf("active email = %q", got)
	}
	if got := ampActiveEmailFromList("No saved accounts.\n"); got != "" {
		t.Fatalf("no accounts = %q", got)
	}
}

func TestAmpSignedInEmail(t *testing.T) {
	if got := ampSignedInEmail("Signed in as dev@example.com (free)\n"); got != "dev@example.com" {
		t.Fatalf("email = %q", got)
	}
	if got := ampSignedInEmail("Signed in as Ada\n"); got != "" {
		t.Fatalf("non-email = %q", got)
	}
}

// One stored login asks Amp for its account once; a new login asks again.
func TestAmpActiveAccountEmailIsReusedUntilTheLoginChanges(t *testing.T) {
	secrets := filepath.Join(t.TempDir(), "secrets.json")
	if err := os.WriteFile(secrets, []byte(`{"apiKey":"a"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	previous := authprobe.CmdRunner
	t.Cleanup(func() { authprobe.CmdRunner = previous })
	authprobe.CmdRunner = func(context.Context, string, ...string) ([]byte, error) {
		calls++
		return []byte("* dev@example.com (user_01)\n"), nil
	}
	binary := filepath.Join(t.TempDir(), "amp")
	for range 2 {
		if got := ampActiveAccountEmail(context.Background(), binary, secrets); got != "dev@example.com" {
			t.Fatalf("email = %q", got)
		}
	}
	if calls != 1 {
		t.Fatalf("account list ran %d times, want 1", calls)
	}
	if err := os.WriteFile(secrets, []byte(`{"apiKey":"a new login"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ampActiveAccountEmail(context.Background(), binary, secrets)
	if calls != 2 {
		t.Fatalf("account list ran %d times after a new login, want 2", calls)
	}
}
