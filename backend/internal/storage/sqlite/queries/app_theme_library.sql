-- name: GetThemeLibrary :one
SELECT library FROM app_theme_library WHERE id = 1;

-- name: SetThemeLibrary :exec
-- The row is seeded by its migration; the upsert keeps a write from being
-- lost if it is ever missing. The change trigger fires on the update.
INSERT INTO app_theme_library (id, library) VALUES (1, ?)
ON CONFLICT (id) DO UPDATE SET library = excluded.library;
