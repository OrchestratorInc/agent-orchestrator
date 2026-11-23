package sqlite

import (
	"slices"
	"testing"
	"time"
)

// The theme library tells open windows about a change through change_log, and
// only about a change to the themes themselves. It lives in its own table so
// an ordinary settings read does not carry megabytes of embedded images.
func TestCustomThemesChangeIsLoggedOnce(t *testing.T) {
	db := openMigratedTestDB(t)
	logged := func() int {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM change_log WHERE event_type = 'session_updated'
 AND json_extract(payload, '$.kind') = 'custom_theme'`).Scan(&n); err != nil {
			t.Fatalf("count change_log: %v", err)
		}
		return n
	}

	var stored string
	if err := db.QueryRow(`SELECT library FROM app_theme_library WHERE id = 1`).Scan(&stored); err != nil || stored != "{}" {
		t.Fatalf("library = %q, %v; want the empty one", stored, err)
	}

	// Themes belong to the app, not a project: a user with only standalone
	// sessions (no project row at all) must hear about the change too.
	if _, err := db.Exec(`UPDATE app_theme_library SET library = '{"themes":[{"id":"sumi"}]}' WHERE id = 1`); err != nil {
		t.Fatalf("update themes: %v", err)
	}
	if got := logged(); got != 1 {
		t.Fatalf("logged %d rows after a theme change, want 1", got)
	}
	var project, session any
	if err := db.QueryRow(`SELECT project_id, session_id FROM change_log WHERE json_extract(payload, '$.kind') = 'custom_theme'`).Scan(&project, &session); err != nil || project != nil || session != nil {
		t.Fatalf("log row project=%v session=%v err=%v; want neither", project, session, err)
	}

	// The same value again is not a change, and neither is a settings write.
	if _, err := db.Exec(`UPDATE app_theme_library SET library = '{"themes":[{"id":"sumi"}]}' WHERE id = 1`); err != nil {
		t.Fatalf("rewrite same themes: %v", err)
	}
	if _, err := db.Exec(`UPDATE app_settings SET updated_at = ? WHERE id = 1`, time.Now().UTC()); err != nil {
		t.Fatalf("settings update: %v", err)
	}
	if got := logged(); got != 1 {
		t.Fatalf("logged %d rows after no-op writes, want still 1", got)
	}

	// The upsert the store uses updates the one row and is logged.
	if _, err := db.Exec(`INSERT INTO app_theme_library (id, library) VALUES (1, '{}') ON CONFLICT (id) DO UPDATE SET library = excluded.library`); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got := logged(); got != 2 {
		t.Fatalf("logged %d rows after the upsert, want 2", got)
	}
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM app_theme_library`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("rows = %d, %v; want the single library row", rows, err)
	}

	// The settings row no longer carries the library.
	if columns := tableColumns(t, db, "app_settings"); slices.Contains(columns, "custom_themes") {
		t.Fatalf("app_settings must not hold the theme library: %v", columns)
	}

	if _, err := db.Exec(`UPDATE app_theme_library SET library = 'not json' WHERE id = 1`); err == nil {
		t.Fatal("the library must hold JSON")
	}
}
