package store

import (
	"context"
	"encoding/json"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// PutMCPApp participates in the provider event's transaction, after its activity.
func (s *Store) PutMCPApp(ctx context.Context, conversation, item string, data domain.MCPAppData) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return s.inTx(ctx, "store MCP app", func(q *gen.Queries) error {
		return q.UpsertMCPApp(ctx, gen.UpsertMCPAppParams{ConversationID: conversation, ProviderItemID: item, DataJson: string(encoded)})
	})
}

// MCPApp returns an app only when it belongs to the active visible branch.
func (s *Store) MCPApp(ctx context.Context, conversation, activity string) (domain.MCPAppRecord, error) {
	row, err := s.qr.SelectMCPApp(ctx, gen.SelectMCPAppParams{ConversationID: conversation, ActivityID: activity})
	if err != nil {
		return domain.MCPAppRecord{}, err
	}
	return mcpAppRecord(row.ConversationID, row.ActivityID, row.DataJson, row.DocumentJson, row.ContextText)
}

func mcpAppRecord(conversation, activity, data, document, note string) (domain.MCPAppRecord, error) {
	app := domain.MCPAppRecord{ConversationID: conversation, ActivityID: activity, Document: json.RawMessage(document), Context: note}
	err := json.Unmarshal([]byte(data), &app.Data)
	return app, err
}

// VisibleMCPApps returns the latest nonempty context notes without loading documents.
func (s *Store) VisibleMCPApps(ctx context.Context, conversation string) ([]domain.MCPAppRecord, error) {
	rows, err := s.qr.SelectVisibleMCPApps(ctx, conversation)
	if err != nil {
		return nil, err
	}
	apps := make([]domain.MCPAppRecord, 0, len(rows))
	for _, row := range rows {
		app, err := mcpAppRecord(row.ConversationID, row.ActivityID, row.DataJson, "", row.ContextText)
		if err != nil {
			return nil, err
		}
		apps = append(apps, app)
	}
	return apps, nil
}

// SetMCPAppDocument saves the first successfully captured document.
func (s *Store) SetMCPAppDocument(ctx context.Context, activity string, document json.RawMessage) error {
	return s.qw.SetMCPAppDocument(ctx, gen.SetMCPAppDocumentParams{ActivityID: activity, DocumentJson: string(document)})
}

// SetMCPAppContext replaces the context note for an app.
func (s *Store) SetMCPAppContext(ctx context.Context, activity, note string) error {
	return s.qw.SetMCPAppContext(ctx, gen.SetMCPAppContextParams{ActivityID: activity, ContextText: note})
}

// SetMCPAppDocumentForItem captures a provider item before a client opens its view.
func (s *Store) SetMCPAppDocumentForItem(ctx context.Context, conversation, item string, document json.RawMessage) error {
	return s.qw.SetMCPAppDocumentForItem(ctx, gen.SetMCPAppDocumentForItemParams{ConversationID: conversation, ProviderItemID: item, DocumentJson: string(document)})
}
