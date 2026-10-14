package modelcatalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestPoolCatalogUsesConfiguredNativeChoices(t *testing.T) {
	for _, catalog := range []ports.AgentModelCatalog{Base("pool"), Manual("pool")} {
		if catalog.SelectionMode != ports.ModelSelectionCatalog || catalog.CustomModelEntry != ports.CustomModelEntryConfigured || catalog.AllowCustom || len(catalog.Models) != 0 {
			t.Fatalf("unqualified Pool catalog = %#v", catalog)
		}
	}
	if Base("pool").Source != "cli" {
		t.Fatal("Pool must schedule native catalog discovery")
	}
	discoverer := Discoverer{ACPOptions: map[string]ACPOptionListFunc{
		"pool": func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.ChatConfigOption, error) {
			return []ports.ChatConfigOption{{
				ID: "model", Category: "model", Type: ports.ChatConfigOptionSelect,
				Current: ports.ChatConfigOptionValue{Select: "provider-current"},
				Choices: []ports.ChatConfigOptionChoice{
					{Value: "provider-current", Name: "Current"},
					{Value: "provider-other", Name: "Other"},
				},
			}}, nil
		},
	}}
	got, err := discoverer.Discover(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "pool"})
	if err != nil {
		t.Fatal(err)
	}
	want := []ports.AgentModelInfo{{ID: "provider-current", Label: "Current", IsDefault: true}, {ID: "provider-other", Label: "Other"}}
	if !reflect.DeepEqual(got.Models, want) || got.Source != "acp" || got.AllowCustom {
		t.Fatalf("Pool catalog = %#v, want native IDs and current selection %#v", got, want)
	}
}

func TestPoolCatalogPreservesDiscoveryFailure(t *testing.T) {
	failure := errors.New("provider model catalog unavailable")
	discoverer := Discoverer{ACPOptions: map[string]ACPOptionListFunc{
		"pool": func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.ChatConfigOption, error) {
			return nil, failure
		},
	}}
	_, err := discoverer.Discover(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "pool"})
	if !errors.Is(err, failure) {
		t.Fatalf("Pool discovery error = %v, want original failure", err)
	}
}

func TestPoolCatalogFingerprintTracksProviderModelAndSavedSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	for _, key := range []string{"POOLSIDE_API_KEY", "POOLSIDE_STANDALONE_BASE_URL", "POOLSIDE_STANDALONE_MODEL", "POOLSIDE_STANDALONE_CONTEXT_LENGTH"} {
		t.Setenv(key, "")
	}
	fingerprint := func(env map[string]string) string {
		return discoveryConfigInputs(context.Background(), "pool", "", env)
	}
	base := fingerprint(nil)
	for key, value := range map[string]string{
		"POOLSIDE_API_KEY":             "private-test-key",
		"POOLSIDE_STANDALONE_BASE_URL": "https://private-test-provider.example/v1",
		"POOLSIDE_STANDALONE_MODEL":    "private-test-model",
	} {
		got := fingerprint(map[string]string{key: value})
		if got == base || strings.Contains(got, "private-test") {
			t.Fatalf("changed %s must invalidate without exposing its value: %q", key, got)
		}
	}
	configDir := filepath.Join(home, "config", "poolside")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"settings.yaml", "credentials.json"} {
		if err := os.WriteFile(filepath.Join(configDir, name), []byte("private-test-config"), 0600); err != nil {
			t.Fatal(err)
		}
		got := fingerprint(nil)
		if got == base || strings.Contains(got, "private-test") {
			t.Fatalf("changed %s must invalidate without exposing contents: %q", name, got)
		}
		base = got
	}
	t.Setenv("POOLSIDE_API_KEY", "private-test-inherited")
	if fingerprint(map[string]string{"POOLSIDE_API_KEY": ""}) != base {
		t.Fatal("explicit empty key must mask the inherited key in the fingerprint")
	}
}
