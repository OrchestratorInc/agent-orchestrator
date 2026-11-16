-- Allow terminal failed and abandoned delivery states for reports.
-- SQLite cannot ALTER a CHECK constraint in place, so rewrite the reports schema.

-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(
    replace(
        sql,
        'CHECK (delivery_state IN (''pending'', ''claimed'', ''acknowledged''))',
        'CHECK (delivery_state IN (''pending'', ''claimed'', ''acknowledged'', ''failed'', ''abandoned''))'
    ),
    'OR (delivery_state = ''acknowledged'' AND claim_token <> '''' AND claimed_at IS NOT NULL AND acknowledged_at IS NOT NULL)',
    'OR (delivery_state = ''acknowledged'' AND claim_token <> '''' AND claimed_at IS NOT NULL AND acknowledged_at IS NOT NULL) OR (delivery_state IN (''failed'', ''abandoned'') AND claim_token = '''' AND claimed_at IS NULL AND acknowledged_at IS NULL)'
)
WHERE type = 'table' AND name = 'reports';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(
    replace(
        sql,
        'CHECK (delivery_state IN (''pending'', ''claimed'', ''acknowledged'', ''failed'', ''abandoned''))',
        'CHECK (delivery_state IN (''pending'', ''claimed'', ''acknowledged''))'
    ),
    ' OR (delivery_state IN (''failed'', ''abandoned'') AND claim_token = '''' AND claimed_at IS NULL AND acknowledged_at IS NULL)',
    ''
)
WHERE type = 'table' AND name = 'reports';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
