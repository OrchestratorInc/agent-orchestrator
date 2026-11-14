package usage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestDefaultSourceRootsResolvesOpenCodeAndKiloData(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("OPENCODE_DATA_DIR", "")
	t.Setenv("KILO_DATA_DIR", "")
	got, err := DefaultSourceRoots(context.Background(), "")
	mustNoError(t, err)
	if want := filepath.Join(home, ".local", "share", "opencode"); got.OpenCodeData != want {
		t.Fatalf("OpenCode data root = %q, want %q", got.OpenCodeData, want)
	}
	if want := filepath.Join(home, ".local", "share", "kilo"); got.KiloData != want {
		t.Fatalf("Kilo data root = %q, want %q", got.KiloData, want)
	}

	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	got, err = DefaultSourceRoots(context.Background(), "")
	mustNoError(t, err)
	if got.OpenCodeData != filepath.Join(dataHome, "opencode") || got.KiloData != filepath.Join(dataHome, "kilo") {
		t.Fatalf("roots = %q, %q, want XDG_DATA_HOME %q", got.OpenCodeData, got.KiloData, dataHome)
	}

	openCodeDir, kiloDir := t.TempDir(), t.TempDir()
	t.Setenv("OPENCODE_DATA_DIR", openCodeDir)
	t.Setenv("KILO_DATA_DIR", kiloDir)
	got, err = DefaultSourceRoots(context.Background(), "")
	mustNoError(t, err)
	if got.OpenCodeData != openCodeDir || got.KiloData != kiloDir {
		t.Fatalf("roots = %q, %q, want the explicit data dirs", got.OpenCodeData, got.KiloData)
	}
}

func TestCollectorRegistersOpenCodeFamilyDatabaseForNativeSession(t *testing.T) {
	const nativeID = "ses_1fd2e72e8ffeUicgn3R3RsfdYt"
	tests := []struct {
		harness domain.AgentHarness
		dbName  string
		kind    domain.UsageSourceKind
		roots   func(string) SourceRoots
	}{
		{domain.HarnessOpenCode, "opencode.db", domain.UsageSourceOpenCodeDB, func(root string) SourceRoots { return SourceRoots{OpenCodeData: root} }},
		{domain.HarnessKilocode, "kilo.db", domain.UsageSourceKiloDB, func(root string) SourceRoots { return SourceRoots{KiloData: root} }},
	}
	for _, tt := range tests {
		t.Run(string(tt.harness), func(t *testing.T) {
			if !SupportedHarness(tt.harness) {
				t.Fatalf("%s usage is not reported as supported", tt.harness)
			}
			store := collectorTestStore(t)
			session := collectorTestSession(t, store, tt.harness, nativeID, false)
			root := t.TempDir()
			path := filepath.Join(root, tt.dbName)
			writeOpenCodeSessionDB(t, path, nativeID)
			collector := NewCollector(store, tt.roots(root), nil)

			mustNoError(t, collector.RecordHook(context.Background(), session.ID, HookSignal{
				Harness: tt.harness, Event: "session-start", NativeSessionID: nativeID,
			}))
			bindings, err := store.ListUsageBindingsForSession(context.Background(), session.ID)
			if err != nil || len(bindings) != 1 || bindings[0].NativeRootID != nativeID {
				t.Fatalf("bindings=%+v err=%v", bindings, err)
			}
			sources, err := store.ListUsageSourcesForBinding(context.Background(), bindings[0].ID)
			if err != nil || len(sources) != 1 || sources[0].Kind != tt.kind ||
				sources[0].NativeSessionID != nativeID || sources[0].ArtifactPath != canonicalUsagePath(t, path) {
				t.Fatalf("sources=%+v err=%v", sources, err)
			}
		})
	}
}

func TestValidateOpenCodeFamilySourceRejectsOtherDatabases(t *testing.T) {
	root := t.TempDir()
	openCodeDB := filepath.Join(root, "opencode.db")
	export := filepath.Join(root, "session-export.db")
	writeUsageFixture(t, openCodeDB, "SQLite format 3\x00")
	writeUsageFixture(t, export, "SQLite format 3\x00")
	collector := NewCollector(collectorTestStore(t), SourceRoots{OpenCodeData: root, KiloData: root}, nil)
	const nativeID = "ses_1fd2e72e8ffeUicgn3R3RsfdYt"

	resolved, _, _, err := collector.validateSourcePath(context.Background(), domain.HarnessKilocode, "", openCodeDB)
	mustNoError(t, err)
	kilo := domain.UsageBindingRecord{Harness: domain.HarnessKilocode, NativeRootID: nativeID}
	if err := validateSourceAttribution(
		context.Background(), kilo, domain.UsageSourceKiloDB, nativeID, "", resolved, "",
	); err == nil {
		t.Fatal("accepted OpenCode's database as Kilo's")
	}
	resolved, _, _, err = collector.validateSourcePath(context.Background(), domain.HarnessOpenCode, "", export)
	mustNoError(t, err)
	openCode := domain.UsageBindingRecord{Harness: domain.HarnessOpenCode, NativeRootID: nativeID}
	if err := validateSourceAttribution(
		context.Background(), openCode, domain.UsageSourceOpenCodeDB, nativeID, "", resolved, "",
	); err == nil {
		t.Fatal("accepted a database other than OpenCode's opencode.db")
	}
	if _, _, _, err := collector.validateSourcePath(
		context.Background(), domain.HarnessOpenCode, "", filepath.Join(root, "opencode.jsonl"),
	); err == nil {
		t.Fatal("accepted a non-database OpenCode source")
	}
}

func writeOpenCodeSessionDB(t *testing.T, path string, ids ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	mustNoError(t, err)
	defer func() { _ = db.Close() }()
	_, err = db.Exec(`CREATE TABLE session (id TEXT PRIMARY KEY)`)
	mustNoError(t, err)
	for _, id := range ids {
		_, err = db.Exec(`INSERT INTO session (id) VALUES (?)`, id)
		mustNoError(t, err)
	}
}

func TestCollectorFindsTheReleaseChannelDatabaseHoldingTheSession(t *testing.T) {
	const nativeID = "ses_e5ae49e2cffewYM3wbJnh0EEBm"
	root := t.TempDir()
	writeOpenCodeSessionDB(t, filepath.Join(root, "kilo.db"), "ses_older")
	rc := filepath.Join(root, "kilo-rc.db")
	writeOpenCodeSessionDB(t, rc, nativeID)
	writeOpenCodeSessionDB(t, filepath.Join(root, "session-export.db"), nativeID)

	got, err := discoverOpenCodeFamilyDB(context.Background(), root, kiloApp, nativeID)
	if err != nil || got != rc {
		t.Fatalf("discovered %q, %v; want %q", got, err, rc)
	}
	if got, err := discoverOpenCodeFamilyDB(context.Background(), root, kiloApp, "ses_missing"); err != nil || got != "" {
		t.Fatalf("session no database lists = %q, %v; want none yet", got, err)
	}
	for name, want := range map[string]bool{
		"kilo.db": true, "kilo-rc.db": true, "kilo-dev.db": true, "kilo-1.2.db": true,
		"kilo.db-wal": false, "kilo-rc.db-shm": false, "kilo-.db": false, "session-export.db": false,
		"kilo-RC.db": false, "opencode.db": false,
	} {
		if got := openCodeFamilyDBName(name, kiloApp); got != want {
			t.Errorf("openCodeFamilyDBName(%q) = %v, want %v", name, got, want)
		}
	}
}
