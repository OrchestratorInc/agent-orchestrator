package stakpak

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// This deliberately gated native negative control is not full Chat conformance.
// Stock 0.3.88 advertises loadSession but accepts a nonexistent UUID without
// loading history. Keep the harness unregistered until this and the independent
// authenticated history/replay, instructions and process ownership gates pass.
func TestLiveACPMissingHistoryMustNotSucceed(t *testing.T) {
	binary := os.Getenv("AO_LIVE_STAKPAK_ACP_BINARY")
	if binary == "" {
		t.Skip("set AO_LIVE_STAKPAK_ACP_BINARY to an absolute released native executable")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("native executable must be absolute")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	home, workspace := t.TempDir(), t.TempDir()
	cmd := exec.CommandContext(ctx, binary, "acp")
	cmd.Dir = workspace
	// Never inherit provider credentials or mutate the invoking native profile.
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home,
		"XDG_CONFIG_HOME=" + home, "APPDATA=" + home, "LOCALAPPDATA=" + home,
		"PATH=" + os.Getenv("PATH")}
	cmd.Stderr = io.Discard // Native diagnostics can contain private configuration.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	// A native descendant may retain the pipe after the parent exits.
	stopClose := context.AfterFunc(ctx, func() { _ = stdout.Close() })
	defer stopClose()
	defer stdout.Close()
	encoder, decoder := json.NewEncoder(stdin), json.NewDecoder(stdout)
	call := func(id int, method string, params any) json.RawMessage {
		t.Helper()
		if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
			t.Fatal(err)
		}
		for {
			var response struct {
				ID     int             `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if err := decoder.Decode(&response); err != nil {
				t.Fatalf("%s response: %v", method, err)
			}
			if response.ID != id {
				continue
			}
			if len(response.Error) != 0 && string(response.Error) != "null" {
				if method == "initialize" {
					t.Fatal("native initialize failed")
				}
				return nil
			}
			if len(response.Result) == 0 {
				t.Fatal("native response has neither result nor error")
			}
			return response.Result
		}
	}
	result := call(1, "initialize", map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{},
		"clientInfo": map[string]string{"name": "ao-stakpak-negative-control", "version": "1"}})
	var initialized struct {
		AgentCapabilities struct {
			LoadSession bool `json:"loadSession"`
		} `json:"agentCapabilities"`
	}
	if err := json.Unmarshal(result, &initialized); err != nil {
		t.Fatal(err)
	}
	if !initialized.AgentCapabilities.LoadSession {
		t.Fatal("native does not advertise required session/load")
	}
	result = call(2, "session/load", map[string]any{
		"sessionId": "1c5e6ab9-45af-4af4-9519-182093e3625d", "cwd": workspace, "mcpServers": []any{},
	})
	if result != nil {
		t.Fatal("native accepted missing history in an empty isolated profile; exact restore is unsafe")
	}
	// An auth error also refuses admission; it does not prove authenticated load.
	t.Log("missing-history success refused; authenticated exact load/replay remains independently required")
}
