-- +goose Up
-- +goose StatementBegin
-- A degraded handoff completed by starting the target fresh instead of
-- transferring native history; the kind records why ("identity-reset" = the
-- installed harness adapter never declared a compatible TUI+Chat identity
-- pair). Empty by default: every transition recorded before the column exists
-- was a strict transfer.
ALTER TABLE session_interface_transitions
    ADD COLUMN degraded_kind TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd
