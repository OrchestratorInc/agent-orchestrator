package modelcatalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func droidNativeFixture(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture")
	}
	binary := filepath.Join(t.TempDir(), "droid")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"+body), 0700); err != nil {
		t.Fatal(err)
	}
	return binary
}

func TestDroidNativeCatalogPreservesSessionlessMetadata(t *testing.T) {
	cwd, dataDir := t.TempDir(), t.TempDir()
	capture := filepath.Join(cwd, "capture")
	binary := droidNativeFixture(t, `printf '%s\n' "$PWD" "$SCOPE" "$@" > "$CAPTURE"
cat "$2" >> "$CAPTURE"
stat -f '%Lp' "$2" >> "$CAPTURE" 2>/dev/null || stat -c '%a' "$2" >> "$CAPTURE"
IFS= read -r request
printf '%s\n' "$request" >> "$CAPTURE"
printf '%s\n' '{"type":"response","id":"ao-droid-models","result":{"models":[{"id":"latest","displayName":"Latest Official","modelProvider":"anthropic","supportedReasoningEfforts":["off","high"],"defaultReasoningEffort":"high"},{"id":"blocked","disabled":true},{"id":"custom","displayName":"Custom"},{"id":"no-effort","supportedReasoningEfforts":[]},{"id":"latest","displayName":"Duplicate"}]}}'
exec sleep 30
`)
	start := time.Now()
	got, err := DiscoverDroidCatalog(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "droid", Binary: binary, WorkingDir: cwd, Env: map[string]string{"CAPTURE": capture, "SCOPE": "account-project"}}, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("discovery waited for process shutdown")
	}
	ids := []string{}
	for _, m := range got.Models {
		ids = append(ids, m.ID)
		if m.IsDefault {
			t.Fatal("invented default model")
		}
	}
	if !reflect.DeepEqual(ids, []string{"latest", "custom", "no-effort"}) {
		t.Fatalf("models: %#v", got.Models)
	}
	if got.Source != "native" || got.Models[0].Provider != "anthropic" || got.Models[0].DefaultEffort != "high" || !reflect.DeepEqual(got.Models[0].Efforts, []string{"off", "high"}) || got.Models[1].Efforts != nil || got.Models[2].Efforts == nil {
		t.Fatalf("metadata: %#v", got)
	}
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	invocation := string(raw)
	for _, want := range []string{cwd, "account-project", "--settings", filepath.Join(dataDir, "model-discovery"), "stream-jsonrpc", `{"hooksDisabled":true}`, "600", `"method":"droid.list_models"`, `"params":{}`} {
		if !strings.Contains(invocation, want) {
			t.Fatalf("missing %q in %s", want, invocation)
		}
	}
	for _, bad := range []string{"session/new", "authenticate", `"method":"initialize"`, `"method":"prompt"`} {
		if strings.Contains(invocation, bad) {
			t.Fatalf("unexpected interaction %q", bad)
		}
	}
	files, err := os.ReadDir(filepath.Join(dataDir, "model-discovery"))
	if err != nil || len(files) != 0 {
		t.Fatalf("runtime overlay leaked: %v %v", files, err)
	}
}

func TestDroidNativeCatalogFailuresAndEmpty(t *testing.T) {
	tests := []struct {
		name, body         string
		unsupported, empty bool
	}{
		{"missing-method", `printf '%s\n' '{"id":"ao-droid-models","error":{"code":-32601,"message":"secret"}}'`, true, false},
		{"unsupported-flags", `echo 'unknown option --input-format stream-jsonrpc' >&2`, true, false},
		{"rpc-error", `printf '%s\n' '{"id":"ao-droid-models","error":{"code":-32000,"message":"secret"}}'`, false, false},
		{"malformed", `printf '%s\n' '{broken'`, false, false},
		{"omitted-models", `printf '%s\n' '{"id":"ao-droid-models","result":{}}'`, false, false},
		{"interaction", `printf '%s\n' '{"type":"request","method":"authenticate"}'`, false, false},
		{"all-disabled", `printf '%s\n' '{"id":"ao-droid-models","result":{"models":[{"id":"blocked","disabled":true}]}}'`, false, true},
		{"empty", `printf '%s\n' '{"id":"ao-droid-models","result":{"models":[]}}'`, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binary := droidNativeFixture(t, "IFS= read -r request\n"+tt.body+"\n")
			got, err := DiscoverDroidCatalog(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "droid", Binary: binary}, t.TempDir())
			if tt.empty {
				if err != nil || len(got.Models) != 0 || got.Source != "native" {
					t.Fatalf("empty native: %#v %v", got, err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected failure")
			}
			if errors.Is(err, ErrNativeCatalogUnsupported) != tt.unsupported {
				t.Fatalf("unsupported=%v: %v", tt.unsupported, err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("private RPC detail exposed: %v", err)
			}
		})
	}
}

func TestDroidNativeCatalogCancellation(t *testing.T) {
	binary := droidNativeFixture(t, "IFS= read -r request\nexec sleep 30\n")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := DiscoverDroidCatalog(ctx, ports.AgentModelDiscoveryRequest{AgentID: "droid", Binary: binary}, t.TempDir())
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("cancellation: %v after %v", err, time.Since(start))
	}
}

func TestDroidNativeCatalogBoundsOutput(t *testing.T) {
	binary := droidNativeFixture(t, `IFS= read -r request
exec awk 'BEGIN { for (i=0; i<5000000; i++) printf "x" }'
`)
	_, err := DiscoverDroidCatalog(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "droid", Binary: binary}, t.TempDir())
	if err == nil || errors.Is(err, ErrNativeCatalogUnsupported) {
		t.Fatalf("oversized output: %v", err)
	}
}
