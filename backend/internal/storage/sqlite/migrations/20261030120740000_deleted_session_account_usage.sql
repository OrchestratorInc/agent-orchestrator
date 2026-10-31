-- +goose Up
-- A session's usage events hang off usage_bindings, which cascades from
-- sessions, so deleting a session would erase its spend from the agent account
-- usage view. Session deletion first folds that spend into these per-account
-- UTC-day totals, which the account usage read adds back. The key matches
-- AgentAccountUsageSince's rows.
CREATE TABLE deleted_session_account_usage (
    account_id           TEXT    NOT NULL,
    day                  TEXT    NOT NULL,
    model_id             TEXT    NOT NULL,
    input_tokens         INTEGER NOT NULL DEFAULT 0,
    cached_input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens        INTEGER NOT NULL DEFAULT 0,
    estimated_cost_nanos INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (account_id, day, model_id)
);

-- +goose Down
DROP TABLE deleted_session_account_usage;
