package modelcatalog

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// claudeRequest builds a discovery request isolated from this machine.
//
// The configured-default pass reads ANTHROPIC_MODEL and ~/.claude/settings.json,
// so without this the developer's own configured model is appended to every
// catalog under test and the assertions drift per machine.
func claudeRequest(t *testing.T) ports.AgentModelDiscoveryRequest {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("ANTHROPIC_MODEL", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("ANTHROPIC_DEFAULT_OPUS_MODEL", "")
	t.Setenv("ANTHROPIC_DEFAULT_SONNET_MODEL", "")
	t.Setenv("ANTHROPIC_DEFAULT_HAIKU_MODEL", "")
	t.Setenv("ANTHROPIC_SMALL_FAST_MODEL", "")
	return ports.AgentModelDiscoveryRequest{
		AgentID: "claude-code", WorkingDir: t.TempDir(), Env: map[string]string{},
	}
}

// The picker must show what the configured provider actually serves. On
// Bedrock and Vertex the static aliases are simply wrong IDs.
func TestClaudeCatalogPrefersProviderModels(t *testing.T) {
	list := func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.AgentModelInfo, error) {
		return []ports.AgentModelInfo{
			{ID: "us.anthropic.claude-opus-4-5-v1:0"},
			{ID: "us.anthropic.claude-sonnet-4-5-v1:0"},
		}, nil
	}
	catalog, err := discoverClaudeCatalog(context.Background(), claudeRequest(t), list)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Source != "provider" {
		t.Fatalf("source = %q, want provider", catalog.Source)
	}
	if len(catalog.Models) != 2 {
		t.Fatalf("models = %+v, want the two provider IDs", catalog.Models)
	}
	for _, model := range catalog.Models {
		if model.ID == "sonnet" || model.ID == "opus" {
			t.Fatalf("static alias %q leaked into a provider-sourced catalog", model.ID)
		}
	}
}

// A settings.json alias default (e.g. "sonnet") does not match the concrete
// IDs provider discovery returns. Claude Code runs the family's newest model
// for it, so that model becomes the default and no alias entry is added.
func TestClaudeConfiguredAliasDefaultsToNewestFamilyModel(t *testing.T) {
	list := func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.AgentModelInfo, error) {
		return []ports.AgentModelInfo{
			{ID: "claude-sonnet-4-5-20250929", Label: "Claude Sonnet 4.5"},
			{ID: "claude-opus-5-5", Label: "Claude Opus 5.5"},
			{ID: "claude-sonnet-5-5", Label: "Claude Sonnet 5.5"},
		}, nil
	}
	request := claudeRequest(t)
	request.Env = map[string]string{"ANTHROPIC_MODEL": "sonnet"}
	catalog, err := discoverClaudeCatalog(context.Background(), request, list)
	if err != nil {
		t.Fatal(err)
	}
	assertClaudeOrder(t, catalog.Models, []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-sonnet-4-5-20250929"})
	var defaults []string
	for _, model := range catalog.Models {
		if model.IsDefault {
			defaults = append(defaults, model.ID)
		}
	}
	if len(defaults) != 1 || defaults[0] != "claude-sonnet-5-5" {
		t.Fatalf("defaults = %v, want [claude-sonnet-5-5]", defaults)
	}
}

// An alias whose family the provider does not list still surfaces, so the
// configured default stays visible with the CLI's human label.
func TestClaudeConfiguredAliasWithoutFamilyCarriesHumanLabel(t *testing.T) {
	list := func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.AgentModelInfo, error) {
		return []ports.AgentModelInfo{{ID: "claude-opus-4-5-20251101", Label: "Claude Opus 4.5"}}, nil
	}
	request := claudeRequest(t)
	request.Env = map[string]string{"ANTHROPIC_MODEL": "sonnet"}
	catalog, err := discoverClaudeCatalog(context.Background(), request, list)
	if err != nil {
		t.Fatal(err)
	}
	last := catalog.Models[len(catalog.Models)-1]
	if last.ID != "sonnet" || last.Label != "Sonnet" || !last.IsDefault {
		t.Fatalf("last = %+v, want default {ID: sonnet, Label: Sonnet}", last)
	}
}

// A configured custom alias or pinned snapshot AO does not know keeps its raw
// id as the label — we never invent a name for it.
func TestClaudeConfiguredUnknownDefaultKeepsRawLabel(t *testing.T) {
	list := func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.AgentModelInfo, error) {
		return []ports.AgentModelInfo{{ID: "claude-sonnet-4-5-20250929", Label: "Claude Sonnet 4.5"}}, nil
	}
	request := claudeRequest(t)
	request.Env = map[string]string{"ANTHROPIC_MODEL": "my-custom-pin"}
	catalog, err := discoverClaudeCatalog(context.Background(), request, list)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range catalog.Models {
		if model.ID == "my-custom-pin" {
			if model.Label != "my-custom-pin" || !model.IsDefault {
				t.Fatalf("custom pin = %+v, want raw label and default", model)
			}
			return
		}
	}
	t.Fatal("configured custom pin was not carried into the catalog")
}

// Discovery must never empty the picker. Every way of failing to reach the
// provider falls back to the aliases that shipped before.
func TestClaudeCatalogReturnsStaticFallbackWithProviderError(t *testing.T) {
	tests := []struct {
		name string
		list ClaudeModelListFunc
	}{
		{name: "no lister wired", list: nil},
		{
			name: "provider unreachable",
			list: func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.AgentModelInfo, error) {
				return nil, errors.New("could not reach Anthropic")
			},
		},
		{
			name: "credential rejected",
			list: func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.AgentModelInfo, error) {
				return nil, errors.New("provider rejected the credential")
			},
		},
		{
			name: "chain-sourced credential, nothing readable",
			list: func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.AgentModelInfo, error) {
				return nil, errors.New("no credential could be resolved")
			},
		},
		{
			name: "provider returned an empty list",
			list: func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.AgentModelInfo, error) {
				return []ports.AgentModelInfo{}, nil
			},
		},
		{
			name: "provider returned only blanks",
			list: func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.AgentModelInfo, error) {
				return []ports.AgentModelInfo{{ID: ""}, {ID: "   "}}, nil
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			catalog, err := discoverClaudeCatalog(context.Background(), claudeRequest(t), tc.list)
			if len(catalog.Models) == 0 {
				t.Fatal("the picker must never be emptied by a discovery failure")
			}
			if catalog.Source != "catalog" {
				t.Fatalf("source = %q, want the static catalog", catalog.Source)
			}
			ids := map[string]bool{}
			for _, model := range catalog.Models {
				ids[model.ID] = true
			}
			if !ids["sonnet"] || !ids["opus"] {
				t.Fatalf("fallback lost the static aliases: %+v", catalog.Models)
			}
			if tc.list != nil && err == nil {
				t.Fatal("provider discovery failure was suppressed")
			}
		})
	}
}

func TestClaudeGatewayCatalogFailureReturnsConfiguredModelsWithoutDiscoverySuccess(t *testing.T) {
	request := claudeRequest(t)
	request.Env = map[string]string{
		"ANTHROPIC_BASE_URL":             "https://gateway.example",
		"ANTHROPIC_MODEL":                "gateway-primary",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   "gateway-opus",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "gateway-shared",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":  "gateway-shared",
		"ANTHROPIC_SMALL_FAST_MODEL":     "gateway-fast",
	}
	listCalls := 0
	list := func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.AgentModelInfo, error) {
		listCalls++
		return nil, errors.New("gateway does not implement /v1/models")
	}

	catalog, err := discoverClaudeCatalog(context.Background(), request, list)
	if err == nil {
		t.Fatal("gateway listing failure was reported as successful discovery")
	}
	if listCalls != 1 {
		t.Fatalf("provider listing calls = %d, want exactly one", listCalls)
	}
	if catalog.Source == "provider" {
		t.Fatalf("source = %q, want unverified fallback provenance", catalog.Source)
	}
	if catalog.CustomModelEntry != ports.CustomModelEntryDirect || !catalog.AllowCustom {
		t.Fatalf("custom entry = (%q, %v), want direct enabled", catalog.CustomModelEntry, catalog.AllowCustom)
	}
	wantPrefix := []string{"gateway-primary", "gateway-opus", "gateway-shared", "gateway-fast"}
	if len(catalog.Models) < len(wantPrefix) {
		t.Fatalf("models = %#v, want configured gateway models first", catalog.Models)
	}
	for i, want := range wantPrefix {
		if got := catalog.Models[i].ID; got != want {
			t.Fatalf("models[%d] = %q, want %q; catalog = %#v", i, got, want, catalog.Models)
		}
	}
	counts := make(map[string]int, len(catalog.Models))
	for _, item := range catalog.Models {
		counts[item.ID]++
	}
	if counts["gateway-shared"] != 1 {
		t.Fatalf("gateway-shared count = %d, want one deduplicated target", counts["gateway-shared"])
	}
	if counts["sonnet"] != 1 || counts["opus"] != 1 || counts["haiku"] != 1 {
		t.Fatalf("models = %#v, want static aliases after configured gateway targets", catalog.Models)
	}
}

func TestClaudeProviderModelsAreDeduped(t *testing.T) {
	list := func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.AgentModelInfo, error) {
		return []ports.AgentModelInfo{
			{ID: "claude-opus-4-5-20251101"},
			{ID: "claude-opus-4-5-20251101"},
			{ID: " claude-haiku-4-5 "},
		}, nil
	}
	catalog, err := discoverClaudeCatalog(context.Background(), claudeRequest(t), list)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 2 {
		t.Fatalf("models = %+v, want duplicates collapsed and whitespace trimmed", catalog.Models)
	}
}

func TestProviderModelsPreserveProviderLabelsAndFallBackToRawIDs(t *testing.T) {
	list := func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.AgentModelInfo, error) {
		return []ports.AgentModelInfo{
			{ID: "claude-opus-5", Label: "Claude Opus 5", Efforts: []string{"low", "max"}},
			{ID: "claude-sonnet-4-5-20250929", Label: "Claude Sonnet 4.5"},
			// No display name: the raw provider ID is the honest fallback.
			{ID: "claude-haiku-4-5-20251001"},
		}, nil
	}
	catalog, err := discoverClaudeCatalog(context.Background(), claudeRequest(t), list)
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{}
	for _, model := range catalog.Models {
		labels[model.ID] = model.Label
	}
	if labels["claude-opus-5"] != "Claude Opus 5" {
		t.Fatalf("label = %q, want provider label", labels["claude-opus-5"])
	}
	if labels["claude-sonnet-4-5-20250929"] != "Claude Sonnet 4.5" {
		t.Fatalf("label = %q, want provider label", labels["claude-sonnet-4-5-20250929"])
	}
	if labels["claude-haiku-4-5-20251001"] != "claude-haiku-4-5-20251001" {
		t.Fatalf("fallback label = %q, want raw model ID", labels["claude-haiku-4-5-20251001"])
	}
}

// Efforts must survive normalization attached to their own model.
func TestProviderEffortsSurviveNormalization(t *testing.T) {
	list := func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.AgentModelInfo, error) {
		return []ports.AgentModelInfo{
			{ID: "claude-opus-5", Efforts: []string{"low", "medium", "high", "xhigh", "max"}},
			{ID: "claude-opus-4-6", Efforts: []string{"low", "medium", "high", "max"}},
			{ID: "claude-sonnet-4-5-20250929"},
		}, nil
	}
	catalog, err := discoverClaudeCatalog(context.Background(), claudeRequest(t), list)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, model := range catalog.Models {
		got[model.ID] = len(model.Efforts)
	}
	if got["claude-opus-5"] != 5 || got["claude-opus-4-6"] != 4 {
		t.Fatalf("effort counts = %v, want per-model levels preserved", got)
	}
	if got["claude-sonnet-4-5-20250929"] != 0 {
		t.Fatalf("a model with no efforts must carry none, got %d", got["claude-sonnet-4-5-20250929"])
	}
}

// The exact list from the first-party picker that regressed: only the 4.5
// models carry a snapshot date, and the undated newer models used to sink
// below them. Version decides first, release date breaks version ties.
func TestClaudeCatalogOrdersNewestFirstAcrossDatedAndUndatedIDs(t *testing.T) {
	list := func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.AgentModelInfo, error) {
		return []ports.AgentModelInfo{
			{ID: "claude-opus-4-5-20251101", Label: "Claude Opus 4.5"},
			{ID: "claude-haiku-4-5-20251001", Label: "Claude Haiku 4.5"},
			{ID: "claude-sonnet-4-5-20250929", Label: "Claude Sonnet 4.5"},
			{ID: "claude-fable-5", Label: "Claude Fable 5"},
			{ID: "claude-fable-5-1", Label: "Claude Fable 5.1"},
			{ID: "claude-opus-4-6", Label: "Claude Opus 4.6"},
			{ID: "claude-opus-4-7", Label: "Claude Opus 4.7"},
			{ID: "claude-opus-4-8", Label: "Claude Opus 4.8"},
		}, nil
	}
	catalog, err := discoverClaudeCatalog(context.Background(), claudeRequest(t), list)
	if err != nil {
		t.Fatal(err)
	}
	assertClaudeOrder(t, catalog.Models, []string{
		"claude-fable-5-1",
		"claude-fable-5",
		"claude-opus-4-8",
		"claude-opus-4-7",
		"claude-opus-4-6",
		"claude-opus-4-5-20251101",
		"claude-haiku-4-5-20251001",
		"claude-sonnet-4-5-20250929",
	})
}

// The provider-reported release time breaks a version tie even when no ID
// carries a snapshot date.
func TestClaudeReleasedAtBreaksVersionTies(t *testing.T) {
	at := func(value string) *time.Time {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			t.Fatal(err)
		}
		return &parsed
	}
	models := []ports.AgentModelInfo{
		{ID: "claude-sonnet-5", Label: "Claude Sonnet 5", ReleasedAt: at("2026-01-10T00:00:00Z")},
		{ID: "claude-opus-5", Label: "Claude Opus 5", ReleasedAt: at("2026-03-02T00:00:00Z")},
		{ID: "claude-haiku-5", Label: "Claude Haiku 5"},
	}
	assertClaudeOrder(t, SortClaudeNewestFirst(models), []string{"claude-opus-5", "claude-sonnet-5", "claude-haiku-5"})
}

// Alias IDs carry no version, so the label supplies it; aliases with neither
// keep their order after every versioned model.
func TestClaudeFallbackAliasesUseLabelVersions(t *testing.T) {
	catalog, err := discoverClaudeCatalog(context.Background(), claudeRequest(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	assertClaudeOrder(t, catalog.Models, []string{"fable", "haiku", "opus", "opus[1m]", "sonnet"})
}

func TestClaudeUnversionedAliasesFollowVersionedModels(t *testing.T) {
	models := []ports.AgentModelInfo{
		{ID: "opus[1m]", Label: "Opus (1M context)"},
		{ID: "claude-opus-5-20260101", Label: "Claude Opus 5"},
		{ID: "opus", Label: "Opus"},
	}
	assertClaudeOrder(t, SortClaudeNewestFirst(models), []string{"claude-opus-5-20260101", "opus[1m]", "opus"})
}

// Legacy IDs put the version before the family.
func TestClaudeLegacyIDsSortByVersion(t *testing.T) {
	models := []ports.AgentModelInfo{
		{ID: "claude-3-5-sonnet-20241022", Label: "Claude 3.5 Sonnet"},
		{ID: "claude-sonnet-4-20250514", Label: "Claude Sonnet 4"},
		{ID: "claude-3-7-sonnet-20250219", Label: "Claude 3.7 Sonnet"},
	}
	assertClaudeOrder(t, SortClaudeNewestFirst(models), []string{
		"claude-sonnet-4-20250514", "claude-3-7-sonnet-20250219", "claude-3-5-sonnet-20241022",
	})
}

func TestClaudeSameVersionSnapshotsSortNewestFirst(t *testing.T) {
	models := []ports.AgentModelInfo{
		{ID: "claude-opus-4-5-20251101", Label: "Claude Opus 4.5"},
		{ID: "claude-opus-4-5-20251201", Label: "Claude Opus 4.5"},
		{ID: "claude-opus-4-5-20250901", Label: "Claude Opus 4.5"},
	}
	assertClaudeOrder(t, SortClaudeNewestFirst(models), []string{
		"claude-opus-4-5-20251201", "claude-opus-4-5-20251101", "claude-opus-4-5-20250901",
	})
}

// Bedrock and Vertex spellings parse the same version and date.
func TestClaudeProviderSpellingsSortByVersion(t *testing.T) {
	models := []ports.AgentModelInfo{
		{ID: "us.anthropic.claude-opus-4-5-20251101-v1:0", Label: "Claude Opus 4.5"},
		{ID: "claude-opus-4-8@20260801", Label: "Claude Opus 4.8"},
		{ID: "us.anthropic.claude-haiku-4-5-20251001-v1:0", Label: "Claude Haiku 4.5"},
	}
	assertClaudeOrder(t, SortClaudeNewestFirst(models), []string{
		"claude-opus-4-8@20260801", "us.anthropic.claude-opus-4-5-20251101-v1:0", "us.anthropic.claude-haiku-4-5-20251001-v1:0",
	})
}

// Opaque gateway IDs fall back to the version in the label.
func TestClaudeOpaqueIDsUseLabelVersion(t *testing.T) {
	models := []ports.AgentModelInfo{
		{ID: "gateway-model-a", Label: "Claude Sonnet 4.5"},
		{ID: "gateway-model-b", Label: "Claude Opus 5"},
		{ID: "gateway-model-c", Label: "House model"},
	}
	assertClaudeOrder(t, SortClaudeNewestFirst(models), []string{"gateway-model-b", "gateway-model-a", "gateway-model-c"})
}

func TestClaudeRequestScrubsAmbientGatewayConfiguration(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "https://gw.example")
	t.Setenv("ANTHROPIC_DEFAULT_OPUS_MODEL", "gw-opus")
	t.Setenv("ANTHROPIC_DEFAULT_SONNET_MODEL", "gw-sonnet")
	t.Setenv("ANTHROPIC_DEFAULT_HAIKU_MODEL", "gw-haiku")
	t.Setenv("ANTHROPIC_SMALL_FAST_MODEL", "gw-fast")

	catalog, err := discoverClaudeCatalog(context.Background(), claudeRequest(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	assertClaudeOrder(t, catalog.Models, []string{"fable", "haiku", "opus", "opus[1m]", "sonnet"})
}

// The configured alias is unversioned, so it keeps its place among aliases.
func TestClaudeConfiguredAliasKeepsAliasOrder(t *testing.T) {
	request := claudeRequest(t)
	t.Setenv("ANTHROPIC_MODEL", "haiku")
	catalog, err := discoverClaudeCatalog(context.Background(), request, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertClaudeOrder(t, catalog.Models, []string{"fable", "haiku", "opus", "opus[1m]", "sonnet"})
}

// Unknown families still sort by version; models with no version stay last.
func TestClaudeUnknownFamilySortsByVersion(t *testing.T) {
	list := func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.AgentModelInfo, error) {
		return []ports.AgentModelInfo{
			{ID: "internal-preview", Label: "Internal Preview"},
			{ID: "claude-sonnet-5", Label: "Claude Sonnet 5"},
			{ID: "claude-opus-5-1", Label: "Claude Opus 5.1"},
		}, nil
	}
	catalog, err := discoverClaudeCatalog(context.Background(), claudeRequest(t), list)
	if err != nil {
		t.Fatal(err)
	}
	assertClaudeOrder(t, catalog.Models, []string{"claude-opus-5-1", "claude-sonnet-5", "internal-preview"})
}

func assertClaudeOrder(t *testing.T, models []ports.AgentModelInfo, want []string) {
	t.Helper()
	got := make([]string, 0, len(models))
	for _, model := range models {
		got = append(got, model.ID)
	}
	if len(got) != len(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// The picker order is persisted inside the cached catalog, so it has to be part
// of what the fingerprint covers: a daemon upgrade that changes the ordering
// rule must invalidate the catalogs an older build wrote. Non-Claude agents
// keep their fingerprint unchanged so the upgrade does not rediscover the world.
func TestClaudeDiscoveryFingerprintCoversTheOrderRevision(t *testing.T) {
	dir := t.TempDir()
	claude := discoveryConfigInputs(context.Background(), "claude-code", dir, nil)
	if !strings.Contains(claude, "order=") {
		t.Fatalf("claude discovery inputs = %q, want the order fingerprint folded in", claude)
	}
	if other := discoveryConfigInputs(context.Background(), "codex", dir, nil); strings.Contains(other, "order=") {
		t.Fatalf("codex discovery inputs = %q, want no Claude order revision", other)
	}
}
