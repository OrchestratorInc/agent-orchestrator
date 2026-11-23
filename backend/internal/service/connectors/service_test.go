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
	svc := New(dir, "http://127.0.0.1:3001")
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
	again, err := New(dir, "http://127.0.0.1:3001").Get(context.Background(), "docs-api")
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
			_, err := New(t.TempDir(), "http://127.0.0.1:3001").Create(context.Background(), tt.in)
			if errCode(err) != tt.code {
				t.Fatalf("err = %v (%s), want %s", err, errCode(err), tt.code)
			}
		})
	}
}

func TestLoopbackHTTPAndDuplicateKeys(t *testing.T) {
	svc := New(t.TempDir(), "http://127.0.0.1:3001")
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
	svc := New(dir, "http://127.0.0.1:3001")
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
	svc := New(t.TempDir(), "http://127.0.0.1:3001")
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

func TestAnEditKeepsTheHiddenQueryAndDropsAHeaderThatIsNoLongerTheSignIn(t *testing.T) {
	svc := New(t.TempDir(), "http://127.0.0.1:1")
	ctx := context.Background()
	created, err := svc.Create(ctx, Input{
		Name: "docs", Transport: TransportHTTP, URL: "https://docs.example.com/mcp?api_key=secret", Auth: AuthHeader,
		Headers: map[string]string{"Authorization": "Bearer a", "X-Org": "acme"},
	})
	if err != nil {
		t.Fatal(err)
	}
	shown := MaskedURL(created.URL)
	if shown != "https://docs.example.com/mcp?api_key=***" {
		t.Fatalf("shown address = %q", shown)
	}
	if _, err := svc.Update(ctx, created.ID, Input{Name: "docs", Transport: TransportHTTP, URL: shown, Auth: AuthHeader, Headers: map[string]string{"Authorization": "", "X-Org": ""}, ProjectID: "p1"}); err != nil {
		t.Fatal(err)
	}
	records, _ := readFile(svc.path)
	if records[0].URL != "https://docs.example.com/mcp?api_key=secret" || records[0].Headers["Authorization"] != "Bearer a" || records[0].Headers["X-Org"] != "acme" || records[0].ProjectID != "p1" {
		t.Fatalf("after an edit of the project only = %+v", records[0])
	}
	if _, err := svc.Update(ctx, created.ID, Input{Name: "docs", Transport: TransportHTTP, URL: shown, Auth: AuthNone}); err != nil {
		t.Fatal(err)
	}
	records, _ = readFile(svc.path)
	if len(records[0].Headers) != 0 || records[0].Auth != AuthNone {
		t.Fatalf("after a change to no sign-in = %+v", records[0])
	}
	if _, err := svc.Update(ctx, created.ID, Input{Name: "docs", Transport: TransportHTTP, URL: "https://docs.example.com/mcp?other=1", Auth: AuthNone}); err != nil {
		t.Fatal(err)
	}
	if records, _ = readFile(svc.path); records[0].URL != "https://docs.example.com/mcp?other=1" {
		t.Fatalf("a new address was not saved: %+v", records[0])
	}
}

func TestAnEditOfTheAddressKeepsOrRemovesItsHiddenValuesByName(t *testing.T) {
	svc := New(t.TempDir(), "http://127.0.0.1:1")
	ctx := context.Background()
	created, err := svc.Create(ctx, Input{Name: "docs", Transport: TransportHTTP, URL: "https://docs.example.com/mcp?api_key=secret&team=7"})
	if err != nil {
		t.Fatal(err)
	}
	edit := func(address string) (string, error) {
		_, err := svc.Update(ctx, created.ID, Input{Name: "docs", Transport: TransportHTTP, URL: address})
		records, _ := readFile(svc.path)
		return records[0].URL, err
	}
	if got, err := edit("https://docs.example.com/mcp/v2?api_key=***&team=***"); err != nil || got != "https://docs.example.com/mcp/v2?api_key=secret&team=7" {
		t.Fatalf("a new path = %q, %v", got, err)
	}
	if got, err := edit("https://docs.example.com/mcp/v2?team=8&api_key=***"); err != nil || got != "https://docs.example.com/mcp/v2?team=8&api_key=secret" {
		t.Fatalf("one new value = %q, %v", got, err)
	}
	for _, address := range []string{"https://other.example.com/mcp?api_key=***", "https://docs.example.com/mcp?token=***"} {
		if got, err := edit(address); errCode(err) != "INVALID_CONNECTOR_URL" || !strings.Contains(got, "api_key=secret") {
			t.Fatalf("a hidden value for %s = %q, %v", address, got, err)
		}
	}
	if got, err := edit("https://docs.example.com/mcp/v2"); err != nil || got != "https://docs.example.com/mcp/v2" {
		t.Fatalf("the query removed = %q, %v", got, err)
	}
	if _, err := svc.Create(ctx, Input{Name: "typed", Transport: TransportHTTP, URL: "https://docs.example.com/mcp?api_key=***"}); errCode(err) != "INVALID_CONNECTOR_URL" {
		t.Fatalf("a new connector with a hidden value = %v", err)
	}
}

func TestTheKeyOfAConnectorDoesNotChange(t *testing.T) {
	svc := New(t.TempDir(), "http://127.0.0.1:1")
	ctx := context.Background()
	created, err := svc.Create(ctx, httpInput("Linear"))
	if err != nil {
		t.Fatal(err)
	}
	renamed := httpInput("Linear Work")
	if got, err := svc.Update(ctx, created.ID, renamed); err != nil || got.Key != "linear" || got.Name != "Linear Work" {
		t.Fatalf("a new name = %+v, %v", got, err)
	}
	renamed.Key = "linear-work"
	if _, err := svc.Update(ctx, created.ID, renamed); errCode(err) != "CONNECTOR_KEY_FIXED" {
		t.Fatalf("a new key = %v", err)
	}
}

func TestAResultIsNotSavedOnATargetThatWasNotProbed(t *testing.T) {
	svc := New(t.TempDir(), "http://127.0.0.1:1")
	ctx := context.Background()
	created, err := svc.Create(ctx, httpInput("docs"))
	if err != nil {
		t.Fatal(err)
	}
	probed, _ := svc.snapshot(created.ID)
	moved := httpInput("docs")
	moved.URL = "https://new.example.com/mcp"
	if _, err := svc.Update(ctx, created.ID, moved); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.setStatusOf(created.ID, &probed, StatusConnected, "", "", []string{"old-tool"}); err != nil || got.Status != StatusUnchecked || len(got.Tools) != 0 {
		t.Fatalf("a result for the old address = %+v, %v", got, err)
	}
	current, _ := svc.snapshot(created.ID)
	if got, err := svc.setStatusOf(created.ID, &current, StatusConnected, "", "", []string{"new-tool"}); err != nil || got.Status != StatusConnected {
		t.Fatalf("a result for the current address = %+v, %v", got, err)
	}
}
