package workertransport

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

type modelCatalogControl struct {
	Control
	credential    worker.CredentialResponse
	credentialErr error
	response      any
	failureCode   string
}

func (c *modelCatalogControl) Credential(context.Context) (worker.CredentialResponse, error) {
	return c.credential, c.credentialErr
}

func (c *modelCatalogControl) CompleteTransport(_ context.Context, _ string, _ int, response any) error {
	c.response = response
	return nil
}

func (c *modelCatalogControl) FailTransport(_ context.Context, _ string, _ int, code, _ string) error {
	c.failureCode = code
	return nil
}

func TestModelCatalogPreparesCodexCredentialsBeforeFirstTurn(t *testing.T) {
	for _, credentialType := range []string{"auth_json", "api_key", "access_token"} {
		for _, inheritedHome := range []bool{false, true} {
			t.Run(credentialType+map[bool]string{false: "/data-dir", true: "/inherited-home"}[inheritedHome], func(t *testing.T) {
				root := t.TempDir()
				home := filepath.Join(root, "data", "codex")
				t.Setenv("CODEX_HOME", "")
				if inheritedHome {
					home = filepath.Join(root, "home", ".codex")
					t.Setenv("CODEX_HOME", home)
				}
				// This CLI fixture refuses to serve models before its auth is
				// materialized, reproducing a fresh container without a first turn.
				script := `#!/bin/sh
if [ "$1" = login ]; then
  read secret
  test "$secret" = catalog-test-secret || exit 1
  printf '%s' '{"test":"credential"}' > "$CODEX_HOME/auth.json"
  exit 0
fi
test -s "$CODEX_HOME/auth.json" || exit 1
read initialize
printf '%s\n' '{"id":1,"result":{}}'
read initialized
read list
printf '%s\n' '{"id":2,"result":{"data":[{"id":"entitled-model","displayName":"Entitled Model","isDefault":true}]}}'
`
				if err := os.WriteFile(filepath.Join(root, "codex"), []byte(script), 0o700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
				secret := "catalog-test-secret"
				if credentialType == "auth_json" {
					secret = `{"test":"credential"}`
				}
				control := &modelCatalogControl{credential: worker.CredentialResponse{
					Provider: "codex", CredentialType: credentialType, Secret: secret,
				}}
				s := &Supervisor{Control: control, Harness: "codex", Workspace: root,
					DataDir: filepath.Join(root, "data"), Logger: slog.Default()}
				s.handle(context.Background(), nil, &worker.TransportRequest{ID: "models", Kind: "chat.models", Attempt: 1})
				response, ok := control.response.(worker.ChatModelsResponse)
				if !ok || len(response.Models) != 1 || response.Models[0].ID != "entitled-model" {
					t.Fatalf("catalog = %+v, failure = %q; want entitled model", control.response, control.failureCode)
				}
				if _, err := os.Stat(filepath.Join(home, "auth.json")); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestModelCatalogWithoutProviderCatalogSucceedsWithoutLaunchingCodex(t *testing.T) {
	for _, harness := range []string{"claude-code", "cursor", "opencode"} {
		t.Run(harness, func(t *testing.T) {
			control := &modelCatalogControl{credentialErr: errors.New("must not request credentials for an unavailable catalog")}
			s := &Supervisor{Control: control, Harness: harness, Workspace: t.TempDir(), Logger: slog.Default()}
			s.handle(context.Background(), nil, &worker.TransportRequest{Kind: "chat.models"})
			response, ok := control.response.(worker.ChatModelsResponse)
			if !ok || response.Models == nil || len(response.Models) != 0 || control.failureCode != "" {
				t.Fatalf("catalog = %+v, failure = %q; want successful empty catalog", control.response, control.failureCode)
			}
		})
	}
}

func TestModelCatalogDoesNotStartProviderWhenCredentialsFail(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "provider-started")
	if err := os.WriteFile(filepath.Join(root, "codex"), []byte("#!/bin/sh\nprintf started > '"+marker+"'\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	control := &modelCatalogControl{credentialErr: errors.New("credential unavailable")}
	s := &Supervisor{Control: control, Harness: "codex", Workspace: root, Logger: slog.Default()}
	s.handle(context.Background(), nil, &worker.TransportRequest{Kind: "chat.models"})
	if control.response != nil || control.failureCode != "WORKER_OPERATION_FAILED" {
		t.Fatalf("response = %+v, failure = %q", control.response, control.failureCode)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("provider launched before credentials were available: %v", err)
	}
}
