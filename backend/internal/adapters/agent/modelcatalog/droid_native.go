package modelcatalog

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ErrNativeCatalogUnsupported allows legacy discovery only for an older CLI.
var ErrNativeCatalogUnsupported = errors.New("native model catalog unsupported")

const droidNativeTimeout = 15 * time.Second
const droidNativeOutputLimit = 4 << 20

type droidNativeModel struct {
	ID            string   `json:"id"`
	DisplayName   string   `json:"displayName"`
	Provider      string   `json:"modelProvider"`
	Efforts       []string `json:"supportedReasoningEfforts"`
	DefaultEffort string   `json:"defaultReasoningEffort"`
	Disabled      bool     `json:"disabled"`
}

// DiscoverDroidCatalog uses the official SDK's sessionless droid.list_models
// request: https://docs.factory.com/sdk/python#discover-available-models.
func DiscoverDroidCatalog(ctx context.Context, request ports.AgentModelDiscoveryRequest, dataDir string) (ports.AgentModelCatalog, error) {
	catalog := Base(request.AgentID)
	catalog.Source = "native"
	probeCtx, cancel := context.WithTimeout(ctx, droidNativeTimeout)
	defer cancel()
	if strings.TrimSpace(dataDir) == "" {
		return catalog, errors.New("droid catalog discovery requires AO data directory")
	}
	dir := filepath.Join(dataDir, "model-discovery")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return catalog, err
	}
	scratch, err := os.MkdirTemp(dir, "droid-catalog-")
	if err != nil {
		return catalog, err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	settings := filepath.Join(scratch, "settings.json")
	if err := os.WriteFile(settings, []byte(`{"hooksDisabled":true}`), 0o600); err != nil {
		return catalog, err
	}
	cmd := modelCommand(probeCtx, request.Binary, []string{"--settings", settings, "exec", "--input-format", "stream-jsonrpc", "--output-format", "stream-jsonrpc"}, request.WorkingDir, request.Env)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return catalog, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return catalog, err
	}
	stderr := &droidNativeStderr{}
	cmd.Stderr = stderr
	if err = cmd.Start(); err != nil {
		return catalog, fmt.Errorf("droid native model discovery: %w", err)
	}
	defer func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	stopReading := context.AfterFunc(probeCtx, func() { _ = stdout.Close() })
	defer stopReading()
	envelope := map[string]any{
		"jsonrpc": "2.0", "factoryApiVersion": "1.0.0", "factoryProtocolVersion": "1.221.0",
		"type": "request", "id": "ao-droid-models", "method": "droid.list_models", "params": map[string]any{},
	}
	if err = json.NewEncoder(stdin).Encode(envelope); err != nil {
		return catalog, fmt.Errorf("droid model catalog request: %w", err)
	}
	scanner := bufio.NewScanner(io.LimitReader(stdout, droidNativeOutputLimit+1))
	scanner.Buffer(make([]byte, 4096), droidNativeOutputLimit)
	for scanner.Scan() {
		var response struct {
			Type   string `json:"type"`
			ID     string `json:"id"`
			Method string `json:"method"`
			Result struct {
				Models []droidNativeModel `json:"models"`
			} `json:"result"`
			Error *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if err = json.Unmarshal(scanner.Bytes(), &response); err != nil {
			return catalog, errors.New("droid model catalog returned malformed JSON")
		}
		if response.Type == "request" {
			return catalog, errors.New("droid model catalog requires interaction")
		}
		if response.ID != "ao-droid-models" {
			continue
		}
		if response.Error != nil {
			if response.Error.Code == -32601 {
				return catalog, ErrNativeCatalogUnsupported
			}
			return catalog, fmt.Errorf("droid native model catalog RPC error %d", response.Error.Code)
		}
		if err := probeCtx.Err(); err != nil {
			return catalog, err
		}
		if response.Result.Models == nil {
			return catalog, errors.New("droid model catalog omitted models")
		}
		seen := make(map[string]bool)
		for _, model := range response.Result.Models {
			id := strings.TrimSpace(model.ID)
			if id == "" || model.Disabled || seen[id] {
				continue
			}
			seen[id] = true
			label := strings.TrimSpace(model.DisplayName)
			if label == "" {
				label = id
			}
			catalog.Models = append(catalog.Models, ports.AgentModelInfo{ID: id, Label: label, Provider: model.Provider, Efforts: model.Efforts, DefaultEffort: model.DefaultEffort})
		}

		catalog.FetchedAt = time.Now().UTC()
		return catalog, nil
	}
	if probeCtx.Err() != nil {
		return catalog, probeCtx.Err()
	}
	if err = scanner.Err(); err != nil {
		return catalog, fmt.Errorf("droid model catalog output: %w", err)
	}
	_ = stdin.Close()
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	detail := strings.ToLower(stderr.text())
	if (strings.Contains(detail, "unknown option") || strings.Contains(detail, "unknown argument") || strings.Contains(detail, "unrecognized option")) && (strings.Contains(detail, "stream-jsonrpc") || strings.Contains(detail, "input-format") || strings.Contains(detail, "output-format")) {
		return catalog, ErrNativeCatalogUnsupported
	}
	return catalog, errors.New("droid exited without a model catalog")
}

// Read only after cmd.Wait has joined the stderr writer. Keep private details bounded.
type droidNativeStderr struct {
	data []byte
}

func (b *droidNativeStderr) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 8192 - len(b.data)
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}
func (b *droidNativeStderr) text() string { return string(b.data) }
