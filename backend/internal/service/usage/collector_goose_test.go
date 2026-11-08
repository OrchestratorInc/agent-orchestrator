package usage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestDefaultSourceRootsResolvesGooseSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("GOOSE_PATH_ROOT", "")
	t.Setenv("XDG_DATA_HOME", "")
	got, err := DefaultSourceRoots(context.Background(), "")
	mustNoError(t, err)
	if want := filepath.Join(gooseDataDir(home), "sessions"); got.GooseSessions != want {
		t.Fatalf("Goose sessions root = %q, want %q", got.GooseSessions, want)
	}

	root := t.TempDir()
	t.Setenv("GOOSE_PATH_ROOT", root)
	got, err = DefaultSourceRoots(context.Background(), "")
	mustNoError(t, err)
	if want := filepath.Join(root, "data", "sessions"); got.GooseSessions != want {
		t.Fatalf("Goose sessions root = %q, want GOOSE_PATH_ROOT %q", got.GooseSessions, want)
	}
}

func TestCollectorRegistersGooseLedgerForNativeSession(t *testing.T) {
	const nativeID = "20261103_1"
	store := collectorTestStore(t)
	session := collectorTestSession(t, store, domain.HarnessGoose, nativeID, false)
	root := t.TempDir()
	path := filepath.Join(root, "sessions.db")
	writeUsageFixture(t, path, "SQLite format 3\x00")
	collector := NewCollector(store, SourceRoots{GooseSessions: root}, nil)

	mustNoError(t, collector.RecordHook(context.Background(), session.ID, HookSignal{
		Harness: domain.HarnessGoose, Event: "session-start", NativeSessionID: nativeID,
	}))
	bindings, err := store.ListUsageBindingsForSession(context.Background(), session.ID)
	if err != nil || len(bindings) != 1 || bindings[0].NativeRootID != nativeID {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	sources, err := store.ListUsageSourcesForBinding(context.Background(), bindings[0].ID)
	if err != nil || len(sources) != 1 || sources[0].Kind != domain.UsageSourceGooseLedger ||
		sources[0].NativeSessionID != nativeID || sources[0].ArtifactPath != canonicalUsagePath(t, path) {
		t.Fatalf("sources=%+v err=%v", sources, err)
	}
}

func TestValidateGooseSourceRejectsOtherDatabases(t *testing.T) {
	root := t.TempDir()
	other := filepath.Join(root, "other.db")
	writeUsageFixture(t, other, "SQLite format 3\x00")
	collector := NewCollector(collectorTestStore(t), SourceRoots{GooseSessions: root}, nil)
	resolved, _, _, err := collector.validateSourcePath(context.Background(), domain.HarnessGoose, "", other)
	mustNoError(t, err)
	binding := domain.UsageBindingRecord{Harness: domain.HarnessGoose, NativeRootID: "20261103_1"}
	if err := validateSourceAttribution(
		context.Background(), binding, domain.UsageSourceGooseLedger, "20261103_1", "", resolved, "",
	); err == nil {
		t.Fatal("accepted a database other than Goose's sessions.db")
	}
	if _, _, _, err := collector.validateSourcePath(
		context.Background(), domain.HarnessGoose, "", filepath.Join(root, "sessions.jsonl"),
	); err == nil {
		t.Fatal("accepted a non-database Goose source")
	}
}
