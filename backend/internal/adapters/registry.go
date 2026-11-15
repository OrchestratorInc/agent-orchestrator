package adapters

import (
	"fmt"
	"sort"
)

type Capability string

const (
	CapabilityAgent        Capability = "agent"
	CapabilityIssueTracker Capability = "issue-tracker"
)

type Manifest struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Description  string       `json:"description"`
	Version      string       `json:"version"`
	Capabilities []Capability `json:"capabilities"`
}

type Adapter interface {
	Manifest() Manifest
}

type Registry struct {
	adapters map[string]Adapter
}

func NewRegistry() *Registry {
	return &Registry{
		adapters: make(map[string]Adapter),
	}
}

func (r *Registry) Register(adapter Adapter) error {
	manifest := adapter.Manifest()
	if manifest.ID == "" {
		return fmt.Errorf("adapter id is required")
	}
	if _, exists := r.adapters[manifest.ID]; exists {
		return fmt.Errorf("adapter %q is already registered", manifest.ID)
	}

	r.adapters[manifest.ID] = adapter
	return nil
}

func (r *Registry) Get(id string) (Adapter, bool) {
	p, ok := r.adapters[id]
	return p, ok
}

func (r *Registry) Manifests() []Manifest {
	manifests := make([]Manifest, 0, len(r.adapters))
	for _, adapter := range r.adapters {
		manifests = append(manifests, adapter.Manifest())
	}

	sort.Slice(manifests, func(i, j int) bool {
		return manifests[i].ID < manifests[j].ID
	})

	return manifests
}
