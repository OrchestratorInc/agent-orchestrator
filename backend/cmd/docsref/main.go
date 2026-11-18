// docsref exports shipped agent capabilities without probing the local machine.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	agents "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	chatdrivers "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type harnessRow struct {
	ID       string `json:"id"`
	Terminal bool   `json:"terminal"`
	Chat     bool   `json:"chat"`
	Reviewer bool   `json:"reviewer"`
}

func writeReferences(w io.Writer) error {
	workers, err := agents.Build()
	if err != nil {
		return err
	}
	chat := chatdrivers.Build(nil, nil)
	reviews, err := reviewer.NewResolver()
	if err != nil {
		return err
	}
	for _, harness := range chat.Harnesses() {
		if _, ok := workers.Get(string(harness)); !ok {
			return fmt.Errorf("chat harness %q has no worker adapter", harness)
		}
	}
	for _, adapter := range reviewer.Constructors() {
		if _, ok := workers.Get(string(adapter.Harness())); !ok {
			return fmt.Errorf("reviewer harness %q has no worker adapter", adapter.Harness())
		}
	}
	manifests := workers.Manifests() // Sorted by ID, independent of map order.
	rows := make([]harnessRow, 0, len(manifests))
	for _, manifest := range manifests {
		h := domain.AgentHarness(manifest.ID)
		if !h.IsKnown() {
			return fmt.Errorf("worker harness %q is unknown", h)
		}
		_, hasReviewer := reviews.Reviewer(domain.ReviewerHarness(h))
		rows = append(rows, harnessRow{
			ID: string(h), Terminal: !h.ChatOnly(),
			Chat: chat.SupportsChat(h), Reviewer: hasReviewer,
		})
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(rows)
}

func main() {
	if err := writeReferences(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "docsref:", err)
		os.Exit(1)
	}
}
