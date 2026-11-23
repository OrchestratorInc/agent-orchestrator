package connectors

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func httpInput(name string) Input {
	return Input{Name: name, Transport: TransportHTTP, URL: "https://mcp.example.com/mcp", Auth: AuthOAuth}
}

func errCode(err error) string {
	var api *apierr.Error
	if errors.As(err, &api) {
		return api.Code
	}
	return ""
}

func TestCreateSavesAPrivateFileAndHidesValues(t *testing.T) {
	dir := t.TempDir()
	svc := New(dir)
	created, err := svc.Create(context.Background(), Input{
		Name: "Docs API", Transport: TransportHTTP, URL: "https://docs.example.com/mcp#frag",
		Auth: AuthHeader, Headers: map[string]string{"Authorization": "Bearer secret-value"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Key != "docs-api" || created.URL != "https://docs.example.com/mcp" || !created.Enabled || created.Status != StatusUnchecked {
		t.Fatalf("created = %+v", created)
	}
	if len(created.HeaderKeys) != 1 || created.HeaderKeys[0] != "Authorization" {
		t.Fatalf("header keys = %v", created.HeaderKeys)
	}
	info, err := os.Stat(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, want 0600", info.Mode().Perm())
	}
	again, err := New(dir).Get(context.Background(), "docs-api")
	if err != nil || again.ID != created.ID {
		t.Fatalf("reload = %+v, %v", again, err)
	}
}

func TestCreateRefusesBadDefinitions(t *testing.T) {
	tests := []struct {
		name string
		in   Input
		code string
	}{
		{"reserved key", Input{Key: "ao", Transport: TransportHTTP, URL: "https://x.example/mcp"}, "INVALID_CONNECTOR_KEY"},
		{"plain http", Input{Name: "x", Transport: TransportHTTP, URL: "http://x.example/mcp"}, "INVALID_CONNECTOR_URL"},
		{"login in url", Input{Name: "x", Transport: TransportHTTP, URL: "https://u:p@x.example/mcp"}, "INVALID_CONNECTOR_URL"},
		{"header without value", Input{Name: "x", Transport: TransportHTTP, URL: "https://x.example/mcp", Auth: AuthHeader}, "INVALID_CONNECTOR_HEADERS"},
		{"header with newline", Input{Name: "x", Transport: TransportHTTP, URL: "https://x.example/mcp", Auth: AuthHeader, Headers: map[string]string{"X-Key": "a\nb"}}, "INVALID_CONNECTOR_VALUES"},
		{"command with url", Input{Name: "x", Transport: TransportStdio, Command: "npx", URL: "https://x.example/mcp"}, "INVALID_CONNECTOR"},
		{"command missing", Input{Name: "x", Transport: TransportStdio}, "INVALID_CONNECTOR_COMMAND"},
		{"bad env name", Input{Name: "x", Transport: TransportStdio, Command: "npx", Env: map[string]string{"1BAD": "v"}}, "INVALID_CONNECTOR_VALUES"},
		{"no transport", Input{Name: "x"}, "INVALID_CONNECTOR_TRANSPORT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(t.TempDir()).Create(context.Background(), tt.in)
			if errCode(err) != tt.code {
				t.Fatalf("err = %v (%s), want %s", err, errCode(err), tt.code)
			}
		})
	}
}

func TestLoopbackHTTPAndDuplicateKeys(t *testing.T) {
	svc := New(t.TempDir())
	ctx := context.Background()
	if _, err := svc.Create(ctx, Input{Name: "local", Transport: TransportHTTP, URL: "http://127.0.0.1:8000/mcp"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, Input{Key: "local", Name: "other", Transport: TransportStdio, Command: "npx"}); errCode(err) != "CONNECTOR_KEY_EXISTS" {
		t.Fatalf("err = %v", err)
	}
}

func TestUpdateKeepsEmptyValuesAndResetsStatusOnANewTarget(t *testing.T) {
	dir := t.TempDir()
	svc := New(dir)
	ctx := context.Background()
	created, err := svc.Create(ctx, Input{Name: "db", Transport: TransportStdio, Command: "uvx", Args: []string{"db-mcp"}, Env: map[string]string{"DATABASE_URI": "postgres://secret", "MODE": "ro"}})
	if err != nil {
		t.Fatal(err)
	}
	svc.records[0].Status = StatusConnected
	off := false
	updated, err := svc.Update(ctx, created.ID, Input{Name: "db", Transport: TransportStdio, Command: "uvx", Args: []string{"db-mcp"}, Env: map[string]string{"DATABASE_URI": "", "MODE": "ro"}, Enabled: &off})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != StatusConnected || updated.Enabled || len(updated.EnvKeys) != 2 {
		t.Fatalf("updated = %+v", updated)
	}
	raw, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil || !strings.Contains(string(raw), "postgres://secret") {
		t.Fatalf("the kept value is missing from the file: %v", err)
	}
	moved, err := svc.Update(ctx, created.Key, Input{Name: "db", Transport: TransportStdio, Command: "uvx", Args: []string{"other"}})
	if err != nil || moved.Status != StatusUnchecked {
		t.Fatalf("moved = %+v, %v", moved, err)
	}
}

func TestEnableDeleteAndUnknown(t *testing.T) {
	svc := New(t.TempDir())
	ctx := context.Background()
	created, err := svc.Create(ctx, httpInput("Linear"))
	if err != nil {
		t.Fatal(err)
	}
	if off, err := svc.SetEnabled(ctx, "linear", false); err != nil || off.Enabled {
		t.Fatalf("off = %+v, %v", off, err)
	}
	if err := svc.Delete(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, created.ID); errCode(err) != "CONNECTOR_NOT_FOUND" {
		t.Fatalf("err = %v", err)
	}
	if list, err := svc.List(ctx); err != nil || len(list) != 0 {
		t.Fatalf("list = %v, %v", list, err)
	}
}
