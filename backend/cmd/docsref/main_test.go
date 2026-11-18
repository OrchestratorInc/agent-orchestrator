package main

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	agents "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	chatdrivers "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestReferencesCoverRegistries(t *testing.T) {
	var output bytes.Buffer
	if err := writeReferences(&output); err != nil {
		t.Fatal(err)
	}
	var rows []harnessRow
	if err := json.Unmarshal(output.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	workers, err := agents.Build()
	if err != nil {
		t.Fatal(err)
	}
	chat := chatdrivers.Build(nil, nil)
	reviews, err := reviewer.NewResolver()
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for i, row := range rows {
		h := domain.AgentHarness(row.ID)
		if _, ok := workers.Get(row.ID); !ok || !h.IsKnown() {
			t.Errorf("row names unknown worker %q", row.ID)
		}
		if seen[row.ID] || (i > 0 && rows[i-1].ID >= row.ID) {
			t.Errorf("duplicate or unsorted row %q", row.ID)
		}
		seen[row.ID] = true
		_, hasReviewer := reviews.Reviewer(domain.ReviewerHarness(h))
		if row.Terminal != !h.ChatOnly() || row.Chat != chat.SupportsChat(h) || row.Reviewer != hasReviewer {
			t.Errorf("capabilities differ from registries: %+v", row)
		}
	}
	for _, adapter := range agents.Constructors() {
		if !seen[adapter.Manifest().ID] {
			t.Errorf("registered worker %q has no row", adapter.Manifest().ID)
		}
	}
	for _, h := range chat.Harnesses() {
		if !seen[string(h)] {
			t.Errorf("registered chat harness %q has no row", h)
		}
	}
	for _, adapter := range reviewer.Constructors() {
		if !seen[string(adapter.Harness())] {
			t.Errorf("registered reviewer %q has no row", adapter.Harness())
		}
	}
	t.Logf("%d worker rows cover all worker, Chat and reviewer registrations", len(rows))
}

func TestReferencesDeterministicWithoutInstalledAgents(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dataDir)
	t.Setenv("PATH", t.TempDir())
	var first, second bytes.Buffer
	if err := writeReferences(&first); err != nil {
		t.Fatal(err)
	}
	if err := writeReferences(&second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("two exports differ")
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("export touched app data: %v, %v", entries, err)
	}
	t.Log("two exports are byte-identical with empty PATH and temporary app data")
}
