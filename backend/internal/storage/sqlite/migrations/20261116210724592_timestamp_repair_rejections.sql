-- +goose Up
CREATE TABLE timestamp_repair_rejections (
    table_name TEXT NOT NULL,
    row_id INTEGER NOT NULL,
    pending INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (table_name, row_id)
);

-- +goose Down
-- Installed triggers must remain usable by older binaries.
SELECT 1;
