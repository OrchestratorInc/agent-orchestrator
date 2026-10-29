-- +goose Up
-- App documents and model context are sidecars to recorded tool activities.
-- The activity's existing CDC event announces the app; these larger payloads
-- are read on demand and never copied into the event stream.
CREATE TABLE conversation_mcp_apps (
    activity_id TEXT PRIMARY KEY REFERENCES conversation_activities(id) ON DELETE CASCADE,
    data_json TEXT NOT NULL,
    document_json TEXT NOT NULL DEFAULT '',
    context_text TEXT NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE conversation_mcp_apps;
