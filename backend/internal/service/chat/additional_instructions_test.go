package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type instructionsStore struct {
	Store
	text string
	err  error
}

func (s *instructionsStore) GetSessionAdditionalInstructions(context.Context, domain.SessionID) (string, error) {
	return s.text, s.err
}
func (s *instructionsStore) SetSessionAdditionalInstructions(_ context.Context, _ domain.SessionID, text string) error {
	s.text = text
	return s.err
}

func TestAdditionalInstructionsDelivery(t *testing.T) {
	ctx := context.Background()
	store := &instructionsStore{}
	c := &Controller{store: store}
	original := ports.ChatUserMessage{Text: "hello"}
	got, err := c.withAdditionalInstructions(ctx, original)
	if err != nil || got.Text != original.Text {
		t.Fatalf("empty policy changed delivery: %q, %v", got.Text, err)
	}
	store.text = "中文规则"
	got, err = c.withAdditionalInstructions(ctx, original)
	if err != nil || got.Text != "hello\n\n<ao-user-additional-instructions>\n中文规则\n</ao-user-additional-instructions>" {
		t.Fatalf("delivery = %q, %v", got.Text, err)
	}
	if original.Text != "hello" {
		t.Fatal("original queue payload mutated")
	}
	store.err = errors.New("read failed")
	if _, err := c.withAdditionalInstructions(ctx, original); !errors.Is(err, store.err) {
		t.Fatalf("read error = %v", err)
	}
}

func TestAdditionalInstructionsValidation(t *testing.T) {
	ctx := context.Background()
	store := &instructionsStore{}
	s := &Service{store: store}
	valid := strings.Repeat("中", MaxAdditionalInstructions)
	if err := s.SetAdditionalInstructions(ctx, "session", valid); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{valid + "中", string([]byte{0xff})} {
		if err := s.SetAdditionalInstructions(ctx, "session", invalid); err == nil {
			t.Fatal("invalid instructions accepted")
		}
		if store.text != valid {
			t.Fatal("invalid write changed saved instructions")
		}
	}
	if err := s.SetAdditionalInstructions(ctx, "session", ""); err != nil || store.text != "" {
		t.Fatalf("clear: %v", err)
	}
}

type instructionsDriverRegistry struct{ ports.ChatDriverRegistry }

func (instructionsDriverRegistry) Driver(domain.AgentHarness) (ports.ChatDriver, error) {
	return nil, nil
}

// TestSideLaunchConfigRecovery covers legacy empty snapshots and rejects damaged snapshots.
func TestSideLaunchConfigRecovery(t *testing.T) {
	svc := &Service{drivers: instructionsDriverRegistry{}}
	side := domain.SideConversation{}
	if _, _, err := svc.sideLaunchConfig(side); err != nil {
		t.Fatalf("legacy empty snapshot: %v", err)
	}
	side.LaunchConfig = []byte(`{"WorkspacePath":"frozen-workspace","AdditionalInstructions":"frozen policy"}`)
	cfg, _, err := svc.sideLaunchConfig(side)
	if err != nil || cfg.WorkspacePath != "frozen-workspace" {
		t.Fatalf("frozen snapshot = %+v, %v", cfg, err)
	}
	side.LaunchConfig = []byte("{")
	if _, _, err := svc.sideLaunchConfig(side); err == nil {
		t.Fatal("damaged nonempty snapshot accepted")
	}
}
