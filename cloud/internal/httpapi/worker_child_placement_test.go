package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

// placementChildStore is a stubChildStore whose parent orchestrator runs on a
// bring-your-own Coder connection.
type placementChildStore struct {
	stubChildStore
	placement domain.OrchestratorPlacement
}

func (s *placementChildStore) OrchestratorPlacement(context.Context, string, string) (domain.OrchestratorPlacement, error) {
	return s.placement, nil
}

func byoCoderConnections(t *testing.T) []domain.ProviderConnection {
	t.Helper()
	orgCoder, err := domain.EncodeOrgCoderConfig(domain.OrgCoderConfig{
		BaseURL: byocBaseURL, Owner: byocOwner, TemplateID: byocTemplate,
		AgentName: byocAgentName, DurableRoot: byocDurableDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	return []domain.ProviderConnection{
		{ID: byocConnID, Provider: sandbox.ProviderCoder, Label: "default", Config: orgCoder, ValidationState: "valid"},
	}
}

// A child of an orchestrator on a bring-your-own Coder connection runs on that
// same connection with the project's dev-kit config, never on the managed
// deployment default. This is the production bug where BYO children ran on the
// managed Coder.
func TestCreateWorkerChildInheritsParentBYOCoderConnection(t *testing.T) {
	t.Parallel()
	projectConfig, err := domain.MergeProjectCoderConfig(nil, domain.ProjectCoderConfig{
		Size: "large", WorkspaceNamePrefix: "acme",
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &placementChildStore{
		stubChildStore: stubChildStore{credentialAvailable: true},
		placement: domain.OrchestratorPlacement{
			Provider:         sandbox.ProviderCoder,
			ConnectionID:     byocConnID,
			ProjectConfig:    projectConfig,
			CoderConnections: byoCoderConnections(t),
		},
	}
	srv := newChildServer(store, bothProviderProvisioning(sandbox.ProviderCoder), sandbox.ProviderCoder)

	rec := httptest.NewRecorder()
	srv.createWorkerChild(rec, childRequest(t, []string{"worker:orchestrate"}))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	if store.captured.SandboxConnectionID != byocConnID {
		t.Fatalf("SandboxConnectionID = %q, want the parent's %q", store.captured.SandboxConnectionID, byocConnID)
	}
	profile, err := sandbox.DecodeCoderSessionProfile(store.captured.ResourceProfile)
	if err != nil {
		t.Fatalf("decode stamped profile: %v", err)
	}
	if profile.BaseURL != byocBaseURL || profile.Owner != byocOwner || profile.TemplateID != byocTemplate {
		t.Fatalf("child profile did not use the org's Coder: %+v", profile)
	}
	if profile.WorkspaceNamePrefix != "acme" {
		t.Fatalf("child workspace prefix = %q, want the project's acme; profile %s", profile.WorkspaceNamePrefix, store.captured.ResourceProfile)
	}
}

// A child of an ao-worker orchestrator is checked out in the project's working
// folder too, with {repo} resolved from the orchestrator project's repository,
// on a bring-your-own connection and on the managed deployment alike.
func TestCreateWorkerChildInheritsTheWorkingFolderCheckout(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		connectionID string
		connections  func(*testing.T) []domain.ProviderConnection
	}{
		{"bring-your-own", byocConnID, byoCoderConnections},
		{"managed", "", func(*testing.T) []domain.ProviderConnection { return nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &placementChildStore{
				stubChildStore: stubChildStore{credentialAvailable: true},
				placement: domain.OrchestratorPlacement{
					Provider: sandbox.ProviderCoder, ConnectionID: tc.connectionID,
					ProjectConfig:    []byte(`{"workingDirectory":"~/work/{repo}","repos":[{"url":"https://github.com/acme/billing"}]}`),
					RepositoryURL:    "https://github.com/acme/alice",
					CoderConnections: tc.connections(t),
				},
			}
			provisioning := bothProviderProvisioning(sandbox.ProviderCoder)
			provisioning.Coder.WorkerWorkingFolder = true
			srv := newChildServer(store, provisioning, sandbox.ProviderCoder)
			rec := httptest.NewRecorder()
			srv.createWorkerChild(rec, childRequest(t, []string{"worker:orchestrate"}))
			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
			}
			profile, err := sandbox.DecodeCoderSessionProfile(store.captured.ResourceProfile)
			if err != nil {
				t.Fatalf("decode stamped profile: %v", err)
			}
			if profile.CheckoutDirectory != "~/work/alice" || len(profile.CheckoutSiblings) != 1 || profile.CheckoutSiblings[0] != "billing" {
				t.Fatalf("child checkout = %q %q; profile %s", profile.CheckoutDirectory, profile.CheckoutSiblings, store.captured.ResourceProfile)
			}
		})
	}
}

// A managed-Coder parent in an organization without a bring-your-own connection
// keeps its children on the managed deployment.
func TestCreateWorkerChildOnManagedCoderStaysManaged(t *testing.T) {
	t.Parallel()
	store := &placementChildStore{
		stubChildStore: stubChildStore{credentialAvailable: true},
		placement:      domain.OrchestratorPlacement{Provider: sandbox.ProviderCoder},
	}
	srv := newChildServer(store, bothProviderProvisioning(sandbox.ProviderCoder), sandbox.ProviderCoder)

	rec := httptest.NewRecorder()
	srv.createWorkerChild(rec, childRequest(t, []string{"worker:orchestrate"}))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	if store.captured.SandboxConnectionID != "" {
		t.Fatalf("SandboxConnectionID = %q, want none", store.captured.SandboxConnectionID)
	}
	profile, err := sandbox.DecodeCoderSessionProfile(store.captured.ResourceProfile)
	if err != nil {
		t.Fatalf("decode stamped profile: %v", err)
	}
	if profile.BaseURL != "https://coder.example.com" {
		t.Fatalf("child base URL = %q, want the managed deployment", profile.BaseURL)
	}
}

// A managed-Coder parent in an organization that has (or later gains) a
// bring-your-own connection still keeps its children on the managed
// deployment: a child runs where its orchestrator runs. A scratch project's
// orchestrator lands here when the connection has no default template.
func TestCreateWorkerChildOfManagedParentInBYOOrgStaysManaged(t *testing.T) {
	t.Parallel()
	store := &placementChildStore{
		stubChildStore: stubChildStore{credentialAvailable: true},
		placement: domain.OrchestratorPlacement{
			Provider:         sandbox.ProviderCoder,
			ProjectConfig:    []byte(`{"source":"scratch"}`),
			CoderConnections: byoCoderConnections(t),
		},
	}
	srv := newChildServer(store, bothProviderProvisioning(sandbox.ProviderCoder), sandbox.ProviderCoder)

	rec := httptest.NewRecorder()
	srv.createWorkerChild(rec, childRequest(t, []string{"worker:orchestrate"}))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	if store.captured.SandboxConnectionID != "" {
		t.Fatalf("SandboxConnectionID = %q, want none (the parent is on the managed Coder)", store.captured.SandboxConnectionID)
	}
	profile, err := sandbox.DecodeCoderSessionProfile(store.captured.ResourceProfile)
	if err != nil {
		t.Fatalf("decode stamped profile: %v", err)
	}
	if profile.BaseURL != "https://coder.example.com" {
		t.Fatalf("child base URL = %q, want the managed deployment", profile.BaseURL)
	}
}
