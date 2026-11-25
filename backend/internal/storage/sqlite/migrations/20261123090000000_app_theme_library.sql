-- +goose Up
-- Custom themes for the desktop app: theme files the user imports or their
-- agents make through `ao theme`, listed in the theme picker, plus the latest
-- theme switch an agent asked for. One row holding a JSON object owned by
-- service/apptheme: {"themes": [...], "switch": {...}}; '{}' means none.
--
-- Its own table, not a column of app_settings: a library can hold megabytes
-- of embedded images, and every ordinary settings read selects that whole row.
CREATE TABLE app_theme_library (
    id      INTEGER PRIMARY KEY CHECK (id = 1),
    library TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(library))
);

INSERT INTO app_theme_library (id) VALUES (1);

-- +goose StatementBegin
-- Open windows apply a change at once. The row names no project or session:
-- themes belong to the app, and the desktop stream is not filtered by either,
-- so a user with only standalone sessions hears about it too.
CREATE TRIGGER app_theme_library_cdc
AFTER UPDATE OF library ON app_theme_library
WHEN NEW.library IS NOT OLD.library
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NULL, NULL, 'session_updated', json_object('kind', 'custom_theme'), CURRENT_TIMESTAMP);
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS app_theme_library_cdc;
DROP TABLE IF EXISTS app_theme_library;
