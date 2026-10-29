package opencode

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestOpenCodeNativeSessionConfigDir(t *testing.T) {
	p := New()
	for name, tc := range map[string]struct {
		env  map[string]string
		want string
	}{
		"explicit data dir": {map[string]string{"OPENCODE_DATA_DIR": "/custom/oc"}, "/custom/oc"},
		"xdg data home":     {map[string]string{"XDG_DATA_HOME": "/xdg"}, filepath.Join("/xdg", "opencode")},
		"home default":      {map[string]string{"HOME": "/home/me"}, filepath.Join("/home/me", ".local", "share", "opencode")},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := p.NativeSessionConfigDir(context.Background(), tc.env)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOpenCodeProbeNativeSession(t *testing.T) {
	root := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(root, "opencode.db")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE session (id text PRIMARY KEY, parent_id text, time_archived integer)`); err != nil {
		t.Fatal(err)
	}
	archived := int64(1)
	for _, row := range []struct {
		id       string
		parent   string
		archived *int64
	}{
		{"ses_live", "", nil},
		{"ses_child", "ses_live", nil},
		{"ses_archived", "", &archived},
	} {
		if _, err := db.Exec(`INSERT INTO session (id, parent_id, time_archived) VALUES (?, ?, ?)`, row.id, row.parent, row.archived); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()

	p := New()
	cases := map[string]struct {
		configDir string
		id        string
		want      ports.NativeSessionAvailability
	}{
		"live row available":      {root, "ses_live", ports.NativeSessionAvailabilityAvailable},
		"child row unavailable":   {root, "ses_child", ports.NativeSessionAvailabilityUnavailable},
		"archived unavailable":    {root, "ses_archived", ports.NativeSessionAvailabilityUnavailable},
		"missing row unavailable": {root, "ses_absent", ports.NativeSessionAvailabilityUnavailable},
		"bad id unavailable":      {root, "not-a-session", ports.NativeSessionAvailabilityUnavailable},
		"no db unavailable":       {t.TempDir(), "ses_live", ports.NativeSessionAvailabilityUnavailable},
		"empty ref unknown":       {"", "ses_live", ports.NativeSessionAvailabilityUnknown},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := p.ProbeNativeSession(context.Background(), ports.NativeSessionRef{NativeSessionID: tc.id, ConfigDir: tc.configDir})
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
