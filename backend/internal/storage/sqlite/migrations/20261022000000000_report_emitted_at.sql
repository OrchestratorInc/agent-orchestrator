-- +goose Up
-- +goose StatementBegin
ALTER TABLE reports ADD COLUMN emitted_at TIMESTAMP;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE reports DROP COLUMN emitted_at;
-- +goose StatementEnd
