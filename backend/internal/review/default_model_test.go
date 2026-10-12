package review

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fixedModelCatalog struct {
	catalog ports.AgentModelCatalog
	err     error
}

func (c fixedModelCatalog) Models(context.Context, string, string, bool) (ports.AgentModelCatalog, error) {
	return c.catalog, c.err
}

func TestRetiredDefaultModelDropsOnlyModelsTheCatalogNoLongerLists(t *testing.T) {
	listed := ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "opus"}}}
	saved := domain.HarnessDefault{Model: "retired", Effort: "high"}
	for _, tt := range []struct {
		name    string
		catalog ModelCatalog
		pref    domain.HarnessDefault
		config  domain.AgentConfig
		want    bool
	}{
		{"retired saved model", fixedModelCatalog{catalog: listed}, saved, domain.AgentConfig{}, true},
		{"listed saved model", fixedModelCatalog{catalog: listed}, domain.HarnessDefault{Model: "opus"}, domain.AgentConfig{}, false},
		{"reviewer config chose a model", fixedModelCatalog{catalog: listed}, saved, domain.AgentConfig{Model: "opus"}, false},
		{"catalog unavailable", fixedModelCatalog{err: errors.New("offline")}, saved, domain.AgentConfig{}, false},
		{"catalog stale", fixedModelCatalog{catalog: ports.AgentModelCatalog{Stale: true, Models: listed.Models}}, saved, domain.AgentConfig{}, false},
		{"no catalog", nil, saved, domain.AgentConfig{}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := &Engine{models: tt.catalog}
			if got := e.retiredDefaultModel(context.Background(), "claude-code", "p", tt.pref, tt.config); got != tt.want {
				t.Fatalf("retiredDefaultModel = %t, want %t", got, tt.want)
			}
		})
	}
}
