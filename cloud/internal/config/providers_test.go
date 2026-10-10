package config

import (
	"testing"
)

func TestResolveAvailableProvidersDefaultsToSingle(t *testing.T) {
	t.Setenv("AO_CLOUD_SANDBOX_PROVIDERS", "")
	got, err := resolveAvailableProviders("freestyle", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "freestyle" {
		t.Fatalf("got %v, want [freestyle]", got)
	}
}

func TestResolveAvailableProvidersParsesListAndKeepsDefaultFirst(t *testing.T) {
	t.Setenv("AO_CLOUD_SANDBOX_PROVIDERS", "coder, freestyle")
	got, err := resolveAvailableProviders("freestyle", false)
	if err != nil {
		t.Fatal(err)
	}
	// The default is always first, and each provider appears once.
	if len(got) != 2 || got[0] != "freestyle" || got[1] != "coder" {
		t.Fatalf("got %v, want [freestyle coder]", got)
	}
}

func TestResolveAvailableProvidersDeduplicatesAndTrims(t *testing.T) {
	t.Setenv("AO_CLOUD_SANDBOX_PROVIDERS", " CODER , coder , freestyle ")
	got, err := resolveAvailableProviders("freestyle", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "freestyle" || got[1] != "coder" {
		t.Fatalf("got %v, want [freestyle coder]", got)
	}
}

func TestResolveAvailableProvidersRejectsUnknown(t *testing.T) {
	t.Setenv("AO_CLOUD_SANDBOX_PROVIDERS", "coder, bogus")
	if _, err := resolveAvailableProviders("freestyle", false); err == nil {
		t.Fatal("expected an error for an unknown provider")
	}
}

func TestResolveAvailableProvidersRejectsNonHostedProviderWhenHosted(t *testing.T) {
	t.Setenv("AO_CLOUD_SANDBOX_PROVIDERS", "coder, docker")
	if _, err := resolveAvailableProviders("coder", true); err == nil {
		t.Fatal("expected an error: docker is not permitted in hosted environments")
	}
}

func TestResolveAvailableProvidersAllowsCoderAndFreestyleDefaultWhenHosted(t *testing.T) {
	t.Setenv("AO_CLOUD_SANDBOX_PROVIDERS", "coder")
	got, err := resolveAvailableProviders("freestyle", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "freestyle" || got[1] != "coder" {
		t.Fatalf("got %v, want [freestyle coder]", got)
	}
}

func TestResolveAvailableProvidersAllowsFreestyleWhenHosted(t *testing.T) {
	t.Setenv("AO_CLOUD_SANDBOX_PROVIDERS", "coder,freestyle")
	got, err := resolveAvailableProviders("coder", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "coder" || got[1] != "freestyle" {
		t.Fatalf("got %v, want [coder freestyle]", got)
	}
}

func TestResolveAvailableProvidersRejectsRetiredProviders(t *testing.T) {
	for _, retired := range []string{"nodeops", "ecs", "daytona", "lambda-microvms"} {
		t.Setenv("AO_CLOUD_SANDBOX_PROVIDERS", "coder,"+retired)
		if _, err := resolveAvailableProviders("coder", false); err == nil {
			t.Fatalf("expected an error for retired provider %q", retired)
		}
	}
}

func TestDefaultSandboxProviderIsCoderWhenHosted(t *testing.T) {
	if got := defaultSandboxProvider(true); got != "coder" {
		t.Fatalf("hosted default = %q, want coder", got)
	}
	if got := defaultSandboxProvider(false); got != "docker" {
		t.Fatalf("local default = %q, want docker", got)
	}
}

func TestProvidersRequireWorkerHome(t *testing.T) {
	if providersRequireWorkerHome(nil) {
		t.Fatal("no provider does not require a worker home")
	}
	for _, provider := range []string{"docker", "coder", "freestyle"} {
		if !providersRequireWorkerHome([]string{provider}) {
			t.Fatalf("%s requires a worker home", provider)
		}
	}
}
