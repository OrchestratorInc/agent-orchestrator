package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pressly/goose/v3"

	sqlitestore "github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"

	_ "modernc.org/sqlite"
)

type Store = sqlitestore.Store

//go:embed migrations/*.sql
var migrationsFS embed.FS

const pragmas = "?_pragma=journal_mode(WAL)" +
	"&_pragma=busy_timeout(5000)" +
	"&_pragma=foreign_keys(ON)" +
	"&_pragma=synchronous(NORMAL)" +
	"&_pragma=journal_size_limit(33554432)"

const readOnlyPragmas = "?mode=ro" +
	"&_pragma=busy_timeout(5000)" +
	"&_pragma=foreign_keys(ON)"

const maxReaders = 8

func databaseURI(dataDir string) string {
	file := url.URL{Path: filepath.Join(dataDir, "ao.db")}
	return "file:" + file.EscapedPath()
}

func checkLegacyDatabasePath(dataDir string) error {
	intended := filepath.Join(dataDir, "ao.db")
	if _, err := os.Stat(intended); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect intended database: %w", err)
	}
	raw := intended
	if end := strings.IndexAny(raw, "?#"); end >= 0 {
		raw = raw[:end]
	}
	var decoded strings.Builder
	for i := 0; i < len(raw); i++ {
		if raw[i] == '%' && i+2 < len(raw) {
			if value, err := url.PathUnescape(raw[i : i+3]); err == nil {
				if value[0] == 0 {
					break
				}
				decoded.WriteString(value)
				i += 2
				continue
			}
		}
		decoded.WriteByte(raw[i])
	}
	legacy := decoded.String()
	if legacy == intended {
		return nil
	}
	info, err := os.Stat(legacy)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect legacy database path: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	file, err := os.Open(legacy)
	if err != nil {
		return fmt.Errorf("inspect legacy database: %w", err)
	}
	var header [16]byte
	n, readErr := io.ReadFull(file, header[:])
	_ = file.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return fmt.Errorf("inspect legacy database: %w", readErr)
	}
	if n == len(header) && string(header[:]) == "SQLite format 3\x00" {
		return fmt.Errorf("legacy SQLite path parsing stored data at %q; refusing to create an empty database at %q: stop Orchestrator and recover the legacy database explicitly before retrying", legacy, intended)
	}
	return nil
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	if err := checkLegacyDatabasePath(dataDir); err != nil {
		return nil, err
	}
	dsn := databaseURI(dataDir) + pragmas

	writeDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite writer: %w", err)
	}
	writeDB.SetMaxOpenConns(1)
	writeDB.SetMaxIdleConns(1)
	if err := migrate(writeDB); err != nil {
		_ = writeDB.Close()
		return nil, err
	}
	store, err := withSearchIndex(dataDir, dsn, writeDB)
	if err != nil {
		_ = writeDB.Close()
		return nil, err
	}
	return store, nil
}

func withSearchIndex(dataDir, dsn string, writeDB *sql.DB) (*Store, error) {
	readDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite reader: %w", err)
	}
	readDB.SetMaxOpenConns(maxReaders)
	readDB.SetMaxIdleConns(maxReaders)
	store := sqlitestore.NewStore(writeDB, readDB)

	if sessionSearchSwitchedOff() {
		store.SetSearchUnavailable(errSessionSearchOff)
		return store, nil
	}
	searchDB, reset, err := openSearchWriter(dataDir, writeDB)
	if err != nil {
		store.SetSearchUnavailable(fmt.Errorf("open session search index: %w", err))
		return store, nil
	}
	searchReaders, err := openSearchReaders(dataDir, dsn)
	if err != nil {
		_ = searchDB.Close()
		store.SetSearchUnavailable(fmt.Errorf("open session search readers: %w", err))
		return store, nil
	}
	store.SetSearchIndex(searchDB, searchReaders, reset)
	return store, nil
}

func OpenReadOnly(ctx context.Context, dataDir string) (*Store, error) {
	dsn := databaseURI(dataDir) + readOnlyPragmas

	writeDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite read-only writer: %w", err)
	}
	writeDB.SetMaxOpenConns(1)
	writeDB.SetMaxIdleConns(1)
	if err := writeDB.PingContext(ctx); err != nil {
		_ = writeDB.Close()
		return nil, fmt.Errorf("open sqlite read-only writer: %w", err)
	}

	readDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		_ = writeDB.Close()
		return nil, fmt.Errorf("open sqlite read-only reader: %w", err)
	}
	readDB.SetMaxOpenConns(maxReaders)
	readDB.SetMaxIdleConns(maxReaders)
	if err := readDB.PingContext(ctx); err != nil {
		_ = readDB.Close()
		_ = writeDB.Close()
		return nil, fmt.Errorf("open sqlite read-only reader: %w", err)
	}

	return sqlitestore.NewStore(writeDB, readDB), nil
}

var gooseMu sync.Mutex

var cachedMigrationVersion struct {
	sync.Once
	version int64
	err     error
}

func expectedMigrationVersion() (int64, error) {
	cachedMigrationVersion.Do(func() {
		cachedMigrationVersion.err = computeExpectedMigrationVersion()
	})
	return cachedMigrationVersion.version, cachedMigrationVersion.err
}

func computeExpectedMigrationVersion() error {
	gooseMu.Lock()
	defer gooseMu.Unlock()
	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	migrations, err := goose.CollectMigrations("migrations", 0, goose.MaxVersion)
	if err != nil {
		return fmt.Errorf("collect migrations: %w", err)
	}
	if len(migrations) == 0 {
		return fmt.Errorf("no embedded migrations found")
	}
	cachedMigrationVersion.version = migrations[len(migrations)-1].Version
	return nil
}

func OpenPreMigrated(dataDir string) (*Store, error) {
	want, err := expectedMigrationVersion()
	if err != nil {
		return nil, fmt.Errorf("determine expected migration version: %w", err)
	}

	dsn := databaseURI(dataDir) + pragmas

	writeDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite writer: %w", err)
	}
	writeDB.SetMaxOpenConns(1)
	writeDB.SetMaxIdleConns(1)

	var got int64
	if err := writeDB.QueryRow(
		`SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = 1`,
	).Scan(&got); err != nil {
		_ = writeDB.Close()
		return nil, fmt.Errorf("read applied migration version: %w", err)
	}
	if got != want {
		_ = writeDB.Close()
		return nil, fmt.Errorf(
			"database schema version mismatch: database has version %d but binary expects %d; "+
				"the template is stale — rebuild it with a full sqlite.Open call",
			got, want,
		)
	}

	store, err := withSearchIndex(dataDir, dsn, writeDB)
	if err != nil {
		_ = writeDB.Close()
		return nil, err
	}
	return store, nil
}

func migrate(db *sql.DB) error {
	gooseMu.Lock()
	defer gooseMu.Unlock()
	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	if err := repairRenumberedAgentInstallJobsMigrationHistory(db); err != nil {
		return fmt.Errorf("repair renumbered agent-install-jobs migration history: %w", err)
	}
	if err := repairRenumberedProviderSwitchingMigrationHistory(db); err != nil {
		return fmt.Errorf("repair renumbered provider-switching migration history: %w", err)
	}
	if err := repairRenumberedChatMigrationHistory(db); err != nil {
		return fmt.Errorf("repair renumbered chat migration history: %w", err)
	}
	if err := repairRenumberedCueMigrationHistory(db); err != nil {
		return fmt.Errorf("repair renumbered cue migration history: %w", err)
	}
	if err := repairRenumberedTaskProvisioningMigrationHistory(db); err != nil {
		return fmt.Errorf("repair renumbered task-provisioning migration history: %w", err)
	}
	if err := repairRenumberedUsageCostMigrationHistory(db); err != nil {
		return fmt.Errorf("repair renumbered usage-cost migration history: %w", err)
	}
	if err := repairRenumberedUsageLimitMigrationHistory(db); err != nil {
		return fmt.Errorf("repair renumbered usage-limit migration history: %w", err)
	}
	if err := repairRenumberedComputerUseMigrationHistory(db); err != nil {
		return fmt.Errorf("repair renumbered computer-use migration history: %w", err)
	}
	if err := prepareCancelledConversationTurnsMigration(db); err != nil {
		return fmt.Errorf("prepare cancelled conversation-turn migration: %w", err)
	}
	if err := repairRenumberedCodexProfileMigrationHistory(db); err != nil {
		return fmt.Errorf("repair renumbered Codex profile migration history: %w", err)
	}
	if err := repairRenumberedAgentInventoryMigrationHistory(db); err != nil {
		return fmt.Errorf("repair renumbered agent-inventory migration history: %w", err)
	}
	if err := prepareUsageCostMigration(db); err != nil {
		return fmt.Errorf("prepare usage cost migration: %w", err)
	}
	if err := prepareAutoInjectReviewMigration(db); err != nil {
		return fmt.Errorf("prepare auto-inject-review migration: %w", err)
	}
	if err := preparePRCommentReviewIDMigration(db); err != nil {
		return fmt.Errorf("prepare PR comment review-id migration: %w", err)
	}
	if err := repairRenumberedPRReviewPartialMigrationHistory(db); err != nil {
		return fmt.Errorf("repair renumbered PR review-partial migration history: %w", err)
	}
	if err := repairBurnedAutomationsMigrationHistory(db); err != nil {
		return fmt.Errorf("repair burned automations migration history: %w", err)
	}
	if err := prepareBurnedSchemaRepairs(db); err != nil {
		return fmt.Errorf("prepare burned schema repairs: %w", err)
	}
	if err := prepareReviewPerHarnessMigration(db); err != nil {
		return fmt.Errorf("prepare review per-harness migration: %w", err)
	}
	if err := prepareBrowserVerifierMigration(db); err != nil {
		return fmt.Errorf("prepare browser verifier migration: %w", err)
	}
	if err := prepareQueuedTurnPromotionMigration(db); err != nil {
		return fmt.Errorf("prepare queued-turn promotion migration: %w", err)
	}
	if err := prepareSessionReviewerAgentConfigMigration(db); err != nil {
		return fmt.Errorf("prepare session reviewer agent-config migration: %w", err)
	}
	if err := goose.Up(db, "migrations", goose.WithAllowMissing()); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return reconcileSchema(db)
}

const (
	computerUseBranchMigrationVersion int64 = 20261021135126673
	computerUseMigrationVersion       int64 = 20261023123504180
)

func repairRenumberedComputerUseMigrationHistory(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}
	var column int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('app_settings') WHERE name = 'computer_use'`).Scan(&column); err != nil {
		return err
	}
	if column == 0 {
		return nil
	}
	_, err := db.Exec(`UPDATE goose_db_version SET version_id = ?
WHERE version_id = ? AND NOT EXISTS (SELECT 1 FROM goose_db_version WHERE version_id = ?)`,
		computerUseMigrationVersion, computerUseBranchMigrationVersion, computerUseMigrationVersion)
	return err
}

const (
	usageLimitMigrationVersion      int64 = 20261019220421171
	usageLimitResetMigrationVersion int64 = 20261019220421514
)

func repairRenumberedUsageLimitMigrationHistory(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}
	var legacyApplied, previewApplied, canonicalApplied int
	if err := db.QueryRow(`
SELECT COALESCE((SELECT is_applied FROM goose_db_version WHERE version_id = 201 ORDER BY id DESC LIMIT 1), 0),
       COALESCE((SELECT is_applied FROM goose_db_version WHERE version_id = 213 ORDER BY id DESC LIMIT 1), 0),
       COALESCE((SELECT is_applied FROM goose_db_version WHERE version_id = ? ORDER BY id DESC LIMIT 1), 0)`,
		usageLimitMigrationVersion).Scan(&legacyApplied, &previewApplied, &canonicalApplied); err != nil {
		return err
	}
	if legacyApplied != 1 && previewApplied != 1 {
		return nil
	}
	var quotaShape int
	if err := db.QueryRow(`
SELECT (SELECT COUNT(*) FROM pragma_table_info('conversation_turns')
        WHERE (name = 'usage_limit_resets_at' AND upper(type) = 'TIMESTAMP' AND "notnull" = 0)
           OR (name = 'usage_limit_auto_resume' AND upper(type) = 'INTEGER' AND "notnull" = 1 AND dflt_value = '0')
           OR (name = 'usage_limit_resume_error' AND upper(type) = 'TEXT' AND "notnull" = 1 AND dflt_value = quote('')))
     + (SELECT COUNT(*) FROM sqlite_master WHERE type = 'index'
        AND name = 'conversation_turns_auto_resume' AND tbl_name = 'conversation_turns'
        AND instr(sql, 'usage_limit_resets_at') > 0 AND instr(sql, 'usage_limit_auto_resume = 1') > 0)
     + (SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger'
        AND name = 'conversation_turns_auto_resume_cdc_update' AND tbl_name = 'conversation_turns'
        AND instr(sql, 'usage_limit_auto_resume') > 0 AND instr(sql, 'usage_limit_resume_error') > 0
        AND instr(sql, 'INSERT INTO change_log') > 0)`).Scan(&quotaShape); err != nil {
		return err
	}
	if quotaShape != 5 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	moved, err := tx.Exec(`UPDATE goose_db_version SET version_id = ? WHERE version_id = 213
AND NOT EXISTS (SELECT 1 FROM pragma_table_info('sessions') WHERE name = 'stale_binary_since')`, usageLimitMigrationVersion)
	if err != nil {
		return err
	}
	n, err := moved.RowsAffected()
	if err != nil {
		return err
	}
	if canonicalApplied != 1 && n == 0 {
		if _, err := tx.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`, usageLimitMigrationVersion); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE goose_db_version SET version_id = ? WHERE version_id = 214
AND NOT EXISTS (SELECT 1 FROM pragma_table_info('app_settings') WHERE name = 'version_refresh')
AND EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'trigger'
 AND name = 'conversation_turns_auto_resume_cdc_update'
 AND instr(sql, 'OLD.usage_limit_resets_at IS NOT NEW.usage_limit_resets_at') > 0)`, usageLimitResetMigrationVersion); err != nil {
		return err
	}
	return tx.Commit()
}

func repairRenumberedCueMigrationHistory(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`).Scan(&gooseTable); err != nil || gooseTable == 0 {
		return err
	}
	var cueColumns, reviewerColumn, provisionColumns, unrealHarness int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('cues') WHERE name IN ('id', 'project_id', 'name', 'description', 'type', 'command', 'prompt', 'created_at', 'updated_at')`).Scan(&cueColumns); err != nil {
		return err
	}
	if cueColumns != 9 {
		return nil
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('review') WHERE name = 'interface_mode'`).Scan(&reviewerColumn); err != nil {
		return err
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name IN ('provision_state', 'provision_error')`).Scan(&provisionColumns); err != nil {
		return err
	}
	if err := db.QueryRow(`SELECT instr(sql, 'unreal-agent') FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&unrealHarness); err != nil {
		return err
	}
	var applied149, applied155, applied156, applied159, applied161, applied162, applied163, applied168, discussionCountColumn, fxHarness int
	for _, item := range []struct {
		version int
		result  *int
	}{{149, &applied149}, {155, &applied155}, {156, &applied156}, {159, &applied159}, {161, &applied161}, {162, &applied162}, {163, &applied163}, {168, &applied168}} {
		if err := db.QueryRow(`SELECT COALESCE((SELECT is_applied FROM goose_db_version WHERE version_id = ? ORDER BY id DESC LIMIT 1), 0)`, item.version).Scan(item.result); err != nil {
			return err
		}
	}
	if applied168 != 0 {
		return nil
	}
	if err := db.QueryRow(`SELECT instr(sql, '''fx''') FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&fxHarness); err != nil {
		return err
	}
	if applied163 != 0 && fxHarness != 0 {
		return nil
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('pr') WHERE name = 'discussion_comment_count'`).Scan(&discussionCountColumn); err != nil {
		return err
	}
	oldVersion := 0
	switch {
	case applied163 != 0 && fxHarness == 0:
		oldVersion = 163
	case applied159 != 0 && discussionCountColumn == 0:
		oldVersion = 159
	case applied156 != 0 && provisionColumns != 2:
		oldVersion = 156
	case applied155 != 0 && unrealHarness == 0 && provisionColumns != 2:
		oldVersion = 155
	case applied149 != 0 && reviewerColumn == 0 && provisionColumns != 2:
		oldVersion = 149
	case applied162 != 0:
		oldVersion = 162
	case applied161 != 0:
		oldVersion = 161
	}
	if oldVersion == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (168, 1)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM goose_db_version WHERE version_id = ?`, oldVersion); err != nil {
		return err
	}
	return tx.Commit()
}

func repairRenumberedTaskProvisioningMigrationHistory(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}

	var provisionColumns, taskPreparationColumn, creationSHAColumn, reviewerColumn, catalogColumn, unrealHarness int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name IN ('provision_state', 'provision_error')`,
	).Scan(&provisionColumns); err != nil {
		return err
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'is_task_preparation'`,
	).Scan(&taskPreparationColumn); err != nil {
		return err
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('session_worktrees') WHERE name = 'creation_sha'`,
	).Scan(&creationSHAColumn); err != nil {
		return err
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('review') WHERE name = 'interface_mode'`,
	).Scan(&reviewerColumn); err != nil {
		return err
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('agent_model_catalog') WHERE name = 'metadata_json'`,
	).Scan(&catalogColumn); err != nil {
		return err
	}
	if provisionColumns != 2 && taskPreparationColumn == 0 {
		return nil
	}
	if err := db.QueryRow(
		`SELECT instr(sql, 'unreal-agent') FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`,
	).Scan(&unrealHarness); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	applied := func(version int64) (bool, error) {
		var applied int
		if err := tx.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = ? ORDER BY id DESC LIMIT 1
), 0)`, version).Scan(&applied); err != nil {
			return false, err
		}
		return applied == 1, nil
	}
	mapVersion := func(legacyVersion, canonicalVersion int64) (bool, error) {
		legacyApplied, err := applied(legacyVersion)
		if err != nil || !legacyApplied {
			return false, err
		}
		if _, err := tx.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`, canonicalVersion); err != nil {
			return false, err
		}
		if _, err := tx.Exec(`DELETE FROM goose_db_version WHERE version_id = ?`, legacyVersion); err != nil {
			return false, err
		}
		return true, nil
	}
	oldProvision155, err := applied(155)
	if err != nil {
		return err
	}
	if oldProvision155 && provisionColumns == 2 && unrealHarness == 0 {
		if creationSHAColumn != 0 {
			if _, err := mapVersion(157, 158); err != nil {
				return err
			}
		}
		if taskPreparationColumn != 0 {
			if _, err := mapVersion(156, 157); err != nil {
				return err
			}
		}
		if _, err := mapVersion(155, 156); err != nil {
			return err
		}
	}

	provisionMappedFrom150 := false
	canonicalProvisionApplied, err := applied(156)
	if err != nil {
		return err
	}
	if provisionColumns == 2 && !canonicalProvisionApplied {
		legacyVersion := int64(149)
		if reviewerColumn != 0 {
			if catalogColumn == 0 {
				legacyVersion = 150
			} else {
				legacyVersion = 0
			}
		}
		if legacyVersion != 0 {
			mapped, err := mapVersion(legacyVersion, 156)
			if err != nil {
				return err
			}
			provisionMappedFrom150 = mapped && legacyVersion == 150
		}
	}

	canonicalPreparationApplied, err := applied(157)
	if err != nil {
		return err
	}
	released150 := provisionMappedFrom150
	if taskPreparationColumn != 0 && !canonicalPreparationApplied {
		legacyVersion := int64(150)
		if catalogColumn != 0 || provisionMappedFrom150 {
			legacyVersion = 151
		}
		mapped, err := mapVersion(legacyVersion, 157)
		if err != nil {
			return err
		}
		released150 = released150 || mapped && legacyVersion == 150
	}
	if released150 && catalogColumn == 0 {
		if _, err := tx.Exec(`DROP TRIGGER IF EXISTS agent_model_catalog_cdc_insert`); err != nil {
			return err
		}
		if _, err := tx.Exec(`DROP TRIGGER IF EXISTS agent_model_catalog_cdc_update`); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM goose_db_version WHERE version_id = 151`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func repairRenumberedAgentInstallJobsMigrationHistory(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}

	var installJobShape int
	if err := db.QueryRow(`
SELECT COUNT(*) FROM pragma_table_info('agent_install_jobs')
WHERE name IN (
    'target', 'status', 'method', 'command', 'expected_destination',
    'output', 'error', 'started_at', 'finished_at', 'updated_at'
)`).Scan(&installJobShape); err != nil {
		return err
	}
	if installJobShape != 10 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var canonicalApplied int
	if err := tx.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 123 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&canonicalApplied); err != nil {
		return err
	}
	if canonicalApplied != 0 {
		return tx.Commit()
	}
	if _, err := tx.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (123, 1)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM goose_db_version WHERE version_id IN (119, 120)`); err != nil {
		return err
	}
	return tx.Commit()
}

func prepareCancelledConversationTurnsMigration(db *sql.DB) error {
	var sourceTable, stagingTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'conversation_turns'`,
	).Scan(&sourceTable); err != nil {
		return err
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'conversation_turns_next'`,
	).Scan(&stagingTable); err != nil {
		return err
	}
	if stagingTable == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if sourceTable != 0 {
		if _, err := tx.Exec(`DROP TABLE conversation_turns_next`); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(`ALTER TABLE conversation_turns_next RENAME TO conversation_turns`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func prepareUsageCostMigration(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil || gooseTable == 0 {
		return err
	}
	var usageTables int
	if err := db.QueryRow(`
SELECT COUNT(*) FROM sqlite_master
WHERE type = 'table' AND name IN ('usage_bindings', 'usage_sources', 'model_usage_events')`).Scan(&usageTables); err != nil {
		return err
	}
	if usageTables == 3 {
		return nil
	}
	_, err := db.Exec(`DELETE FROM goose_db_version WHERE version_id = 52`)
	return err
}

func prepareAutoInjectReviewMigration(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}

	var applied int
	if err := db.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 84 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied); err != nil {
		return err
	}
	if applied != 0 {
		return nil
	}

	for _, table := range []string{"sessions", "review_run", "pr_reviews", "pr_comment"} {
		var present int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = 'auto_inject_review'`, table,
		).Scan(&present); err != nil {
			return err
		}
		if present == 0 {
			return nil
		}
	}

	_, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (84, 1)`)
	return err
}

func preparePRCommentReviewIDMigration(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}

	var applied, reviewIDColumn int
	if err := db.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 106 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied); err != nil {
		return err
	}
	if applied != 0 {
		return nil
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('pr_comment') WHERE name = 'review_id'`,
	).Scan(&reviewIDColumn); err != nil {
		return err
	}
	if reviewIDColumn == 0 {
		return nil
	}
	_, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (106, 1)`)
	return err
}

func prepareBrowserVerifierMigration(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}
	var applied int
	if err := db.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 81 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied); err != nil {
		return err
	}
	if applied != 0 {
		return nil
	}
	var verifierColumn int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'browser_capability_verifier'`,
	).Scan(&verifierColumn); err != nil {
		return err
	}
	if verifierColumn == 0 {
		return nil
	}
	_, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (81, 1)`)
	return err
}

func prepareSessionReviewerAgentConfigMigration(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}

	var applied, reviewerConfigColumn int
	if err := db.QueryRow(`
SELECT
    COALESCE((
        SELECT is_applied FROM goose_db_version
        WHERE version_id = 118 ORDER BY id DESC LIMIT 1
    ), 0),
    (SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'reviewer_agent_config')
`).Scan(&applied, &reviewerConfigColumn); err != nil {
		return err
	}
	if applied != 0 || reviewerConfigColumn == 0 {
		return nil
	}

	_, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (118, 1)`)
	return err
}

func prepareQueuedTurnPromotionMigration(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var gooseTable int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return tx.Commit()
	}

	var promotionColumns int
	if err := tx.QueryRow(`
SELECT COUNT(*) FROM pragma_table_info('conversation_turns')
WHERE name IN ('promotion_started_at', 'promoted_to_turn_id')`).Scan(&promotionColumns); err != nil {
		return err
	}
	if promotionColumns != 2 {
		return tx.Commit()
	}

	var applied89 int
	if err := tx.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 89 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied89); err != nil {
		return err
	}
	if applied89 == 0 {
		if _, err := tx.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (89, 1)`); err != nil {
			return err
		}
	}

	var applied88, autoInjectCIColumns int
	if err := tx.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 88 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied88); err != nil {
		return err
	}
	if applied88 != 0 {
		if err := tx.QueryRow(`
SELECT (SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'auto_inject_ci')
     + (SELECT COUNT(*) FROM pragma_table_info('pr') WHERE name = 'auto_inject_ci')`).Scan(&autoInjectCIColumns); err != nil {
			return err
		}
		if autoInjectCIColumns == 0 {
			if _, err := tx.Exec(`DELETE FROM goose_db_version WHERE version_id = 88`); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

func prepareBurnedSchemaRepairs(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}
	for _, rc := range schemaRepairs {
		var applied int
		if err := db.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = ? ORDER BY id DESC LIMIT 1
), 0)`, rc.version).Scan(&applied); err != nil {
			return err
		}
		if applied == 0 {
			continue
		}
		var tableCount int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, rc.table,
		).Scan(&tableCount); err != nil {
			return err
		}
		if tableCount == 0 {
			continue
		}
		var columnCount int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, rc.table, rc.column,
		).Scan(&columnCount); err != nil {
			return err
		}
		if columnCount > 0 {
			continue
		}
		if _, err := db.Exec(rc.addDDL); err != nil {
			return err
		}
		for _, stmt := range rc.postAdd {
			if _, err := db.Exec(stmt); err != nil {
				return err
			}
		}
	}
	return nil
}

func prepareReviewPerHarnessMigration(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var gooseTable int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return tx.Commit()
	}
	var applied80 int
	if err := tx.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 80 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied80); err != nil {
		return err
	}
	var applied48 int
	if err := tx.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 48 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied48); err != nil {
		return err
	}
	if applied48 == 0 {
		return tx.Commit()
	}
	var reviewTable int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'review'`,
	).Scan(&reviewTable); err != nil {
		return err
	}
	if reviewTable == 0 {
		return tx.Commit()
	}
	var agentSessionColumn int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('review') WHERE name = 'agent_session_id'`,
	).Scan(&agentSessionColumn); err != nil {
		return err
	}
	if agentSessionColumn == 0 {
		if _, err := tx.Exec(`ALTER TABLE review ADD COLUMN agent_session_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if applied80 != 0 {
		if err := tx.Commit(); err != nil {
			return err
		}
		return repairReviewPerHarnessShape(db)
	}
	var reviewSessionTable int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'review_session'`,
	).Scan(&reviewSessionTable); err != nil {
		return err
	}
	if reviewSessionTable == 0 {
		if _, err := tx.Exec(`
CREATE TABLE review_session (
    session_id         TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    project_id         TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    harness            TEXT NOT NULL,
    reviewer_handle_id TEXT NOT NULL DEFAULT '',
    agent_session_id   TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at         TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (session_id, harness)
)`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func repairReviewPerHarnessShape(db *sql.DB) error {
	ok, err := reviewHasSessionHarnessUnique(db)
	if err != nil || ok {
		return err
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer func() { _, _ = db.Exec(`PRAGMA foreign_keys=ON`) }()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`
CREATE TABLE review_repair (
    id                 TEXT PRIMARY KEY,
    session_id         TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    project_id         TEXT NOT NULL REFERENCES projects (id),
    harness            TEXT NOT NULL,
    pr_url             TEXT NOT NULL DEFAULT '',
    reviewer_handle_id TEXT NOT NULL DEFAULT '',
    agent_session_id   TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMP NOT NULL,
    updated_at         TIMESTAMP NOT NULL,
    UNIQUE(session_id, harness)
)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`
INSERT INTO review_repair (
    id, session_id, project_id, harness, pr_url, reviewer_handle_id,
    agent_session_id, created_at, updated_at
)
SELECT
    id, session_id, project_id, harness, pr_url, reviewer_handle_id,
    agent_session_id, created_at, updated_at
FROM review`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE review`); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE review_repair RENAME TO review`); err != nil {
		return err
	}
	return tx.Commit()
}

func reviewHasSessionHarnessUnique(db *sql.DB) (bool, error) {
	rows, err := db.Query(`PRAGMA index_list('review')`)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	uniqueIndexes := []string{}
	for rows.Next() {
		var seq int
		var name, origin string
		var unique, partial int
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			return false, err
		}
		if unique == 0 {
			continue
		}
		uniqueIndexes = append(uniqueIndexes, name)
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	for _, name := range uniqueIndexes {
		if indexColumnsMatch(db, name, []string{"session_id", "harness"}) {
			return true, nil
		}
	}
	return false, nil
}

func indexColumnsMatch(db *sql.DB, indexName string, want []string) bool {
	rows, err := db.Query(`PRAGMA index_info(` + quoteSQLiteIdent(indexName) + `)`)
	if err != nil {
		return false
	}
	defer func() { _ = rows.Close() }()
	got := make([]string, 0, len(want))
	for rows.Next() {
		var seqno, cid int
		var name string
		if err := rows.Scan(&seqno, &cid, &name); err != nil {
			return false
		}
		got = append(got, name)
	}
	if rows.Err() != nil || len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func quoteSQLiteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

const providerSwitchingVersion = 20261020131539535

func repairRenumberedProviderSwitchingMigrationHistory(db *sql.DB) error {
	var gooseTable, turnsTable, applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`).Scan(&gooseTable); err != nil || gooseTable == 0 {
		return err
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'session_terminal_turns'`).Scan(&turnsTable); err != nil || turnsTable == 0 {
		return err
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = ? AND is_applied = 1`, providerSwitchingVersion).Scan(&applied); err != nil || applied != 0 {
		return err
	}
	for _, old := range []struct {
		version int64
		mainHas string
	}{
		{version: 201}, {version: 216},
		{196, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'session_artifact_versions'`},
		{199, `SELECT COUNT(*) FROM pragma_table_info('conversation_messages') WHERE name = 'continuation'`},
		{200, `SELECT COUNT(*) FROM pragma_table_info('session_artifact_versions') WHERE name = 'source'`},
	} {
		if old.mainHas != "" {
			var present int
			if err := db.QueryRow(old.mainHas).Scan(&present); err != nil {
				return err
			}
			if present != 0 {
				continue
			}
		}
		res, err := db.Exec(`UPDATE goose_db_version SET version_id = ? WHERE version_id = ? AND is_applied = 1`, providerSwitchingVersion, old.version)
		if err != nil {
			return err
		}
		if moved, err := res.RowsAffected(); err != nil || moved > 0 {
			return err
		}
	}
	return nil
}

func repairRenumberedChatMigrationHistory(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}

	var chatColumn, conversationsTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'session_mode'`,
	).Scan(&chatColumn); err != nil {
		return err
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'conversations'`,
	).Scan(&conversationsTable); err != nil {
		return err
	}
	if chatColumn == 0 || conversationsTable == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
UPDATE goose_db_version SET version_id = 190
WHERE is_applied = 1
  AND EXISTS (SELECT 1 FROM pragma_table_info('sessions') WHERE name = 'hibernated_at')
  AND ((version_id = 179 AND NOT EXISTS (SELECT 1 FROM pragma_table_info('conversation_messages') WHERE name = 'sender_session_id'))
    OR (version_id = 180 AND NOT EXISTS (SELECT 1 FROM pragma_table_info('sessions') WHERE name = 'provision_steps'))
    OR (version_id = 181 AND NOT EXISTS (SELECT 1 FROM pragma_table_info('sessions') WHERE name IN ('artifact_dir', 'startup_cue_json'))))
  AND NOT EXISTS (SELECT 1 FROM goose_db_version WHERE version_id = 190)`); err != nil {
		return err
	}

	legacyApplied := false
	for oldVersion := int64(52); oldVersion <= 65; oldVersion++ {
		var applied int
		if err := tx.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = ? ORDER BY id DESC LIMIT 1
), 0)`, oldVersion).Scan(&applied); err != nil {
			return err
		}
		if applied == 0 {
			continue
		}
		legacyApplied = true
		newVersion := oldVersion + 14
		var alreadyMapped int
		if err := tx.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = ? ORDER BY id DESC LIMIT 1
), 0)`, newVersion).Scan(&alreadyMapped); err != nil {
			return err
		}
		if alreadyMapped == 0 {
			if _, err := tx.Exec(
				`INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`,
				newVersion,
			); err != nil {
				return err
			}
		}
	}
	if !legacyApplied {
		return tx.Commit()
	}

	var modelUsageTable int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'model_usage_events'`,
	).Scan(&modelUsageTable); err != nil {
		return err
	}
	if modelUsageTable == 0 {
		if _, err := tx.Exec(`DELETE FROM goose_db_version WHERE version_id = 52`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func repairRenumberedUsageCostMigrationHistory(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}

	var costShape int
	if err := db.QueryRow(`
SELECT (SELECT COUNT(*) FROM pragma_table_info('usage_bindings') WHERE name = 'provider_hint')
     + (SELECT COUNT(*) FROM pragma_table_info('model_usage_events') WHERE name = 'billing_provider_id')
     + (SELECT COUNT(*) FROM pragma_table_info('model_usage_events') WHERE name = 'estimated_cost_nanos')
     + (SELECT COUNT(*) FROM pragma_table_info('model_usage_events') WHERE name = 'pricing_version')`).Scan(&costShape); err != nil {
		return err
	}
	if costShape != 4 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	markApplied := func(version int64) error {
		var applied int
		if err := tx.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = ? ORDER BY id DESC LIMIT 1
), 0)`, version).Scan(&applied); err != nil {
			return err
		}
		if applied != 0 {
			return nil
		}
		_, err := tx.Exec(
			`INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`,
			version,
		)
		return err
	}

	if err := markApplied(113); err != nil {
		return err
	}
	var canonicalIndex int
	if err := tx.QueryRow(`
SELECT COUNT(*) FROM sqlite_master
WHERE type = 'index' AND name = 'idx_model_usage_events_canonical_cost_candidates'`).Scan(&canonicalIndex); err != nil {
		return err
	}
	if canonicalIndex != 0 {
		if err := markApplied(114); err != nil {
			return err
		}
	}

	var measurementShape int
	if err := tx.QueryRow(`
SELECT COUNT(*) FROM pragma_table_info('model_usage_events')
WHERE name IN ('usage_measurement_kind', 'provider_usage_json')`).Scan(&measurementShape); err != nil {
		return err
	}
	if measurementShape == 2 {
		if err := markApplied(115); err != nil {
			return err
		}
	}

	var providerSource int
	if err := tx.QueryRow(`
SELECT COUNT(*) FROM pragma_table_info('model_usage_events')
WHERE name = 'billing_provider_source'`).Scan(&providerSource); err != nil {
		return err
	}
	if providerSource != 0 {
		if err := markApplied(116); err != nil {
			return err
		}
	}

	var latestPromptShape, branchShape, prTriggerShape, cloudShape int
	if err := tx.QueryRow(`
SELECT COUNT(*) FROM pragma_table_info('sessions')
WHERE name = 'latest_user_prompt_at'`).Scan(&latestPromptShape); err != nil {
		return err
	}
	if err := tx.QueryRow(`
SELECT COUNT(*) FROM pragma_table_info('conversation_branches')
WHERE name IN ('strategy', 'replay_cutoff_sequence', 'replay_truncated', 'provider_scope_id')`).Scan(&branchShape); err != nil {
		return err
	}
	if err := tx.QueryRow(`
SELECT COUNT(*) FROM sqlite_master
WHERE type = 'trigger' AND name = 'pr_cdc_update'
  AND instr(COALESCE(sql, ''), 'auto_inject_ci') > 0`).Scan(&prTriggerShape); err != nil {
		return err
	}
	if err := tx.QueryRow(`
SELECT COUNT(*) FROM pragma_table_info('app_settings')
WHERE name = 'cloud_offering'`).Scan(&cloudShape); err != nil {
		return err
	}
	for _, migration := range []struct {
		version int64
		present bool
	}{
		{version: 109, present: latestPromptShape != 0},
		{version: 110, present: branchShape == 4},
		{version: 111, present: prTriggerShape != 0},
		{version: 112, present: cloudShape != 0},
	} {
		if migration.present {
			continue
		}
		if _, err := tx.Exec(
			`DELETE FROM goose_db_version WHERE version_id = ?`, migration.version,
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func repairRenumberedCodexProfileMigrationHistory(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}

	var applied117 int
	if err := db.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 117 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied117); err != nil {
		return err
	}
	if applied117 == 0 {
		return nil
	}

	var kimiUsageShape int
	if err := db.QueryRow(`
SELECT (SELECT COUNT(*) FROM sqlite_master
        WHERE type = 'table' AND name = 'usage_bindings'
          AND instr(COALESCE(sql, ''), '''kimi''') > 0)
     + (SELECT COUNT(*) FROM sqlite_master
        WHERE type = 'table' AND name = 'usage_sources'
          AND instr(COALESCE(sql, ''), '''kimi_wire''') > 0)`).Scan(&kimiUsageShape); err != nil {
		return err
	}
	if kimiUsageShape == 2 {
		return nil
	}
	var openUsageShape int
	if err := db.QueryRow(`
SELECT (SELECT COUNT(*) FROM sqlite_master
        WHERE type = 'table' AND name = 'usage_bindings'
          AND instr(COALESCE(sql, ''), 'trim(harness)') > 0)
     + (SELECT COUNT(*) FROM sqlite_master
        WHERE type = 'table' AND name = 'usage_sources'
          AND instr(COALESCE(sql, ''), 'trim(kind)') > 0)`).Scan(&openUsageShape); err != nil {
		return err
	}
	if openUsageShape == 2 {
		return nil
	}

	_, err := db.Exec(`DELETE FROM goose_db_version WHERE version_id = 117`)
	return err
}

func repairRenumberedAgentInventoryMigrationHistory(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}

	var applied119 int
	if err := db.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 119 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied119); err != nil {
		return err
	}
	var applied120 int
	if err := db.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 120 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied120); err != nil {
		return err
	}
	var applied121 int
	if err := db.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 121 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied121); err != nil {
		return err
	}
	var applied122 int
	if err := db.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 122 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied122); err != nil {
		return err
	}
	if applied122 != 0 || (applied119 == 0 && applied120 == 0 && applied121 == 0) {
		return nil
	}

	var inventoryTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'agent_inventory_cache'`,
	).Scan(&inventoryTable); err != nil {
		return err
	}
	if inventoryTable != 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (122, 1)`); err != nil {
		return err
	}
	collidedVersion := int64(119)
	if applied120 != 0 {
		collidedVersion = 120
	}
	if applied121 != 0 {
		collidedVersion = 121
	}
	if _, err := tx.Exec(`DELETE FROM goose_db_version WHERE version_id = ?`, collidedVersion); err != nil {
		return err
	}
	return tx.Commit()
}

func repairRenumberedPRReviewPartialMigrationHistory(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}

	var reviewPartialColumn int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('pr') WHERE name = 'review_partial'`,
	).Scan(&reviewPartialColumn); err != nil {
		return err
	}
	if reviewPartialColumn == 0 {
		return nil
	}

	var agentInstallJobsTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'agent_install_jobs'`,
	).Scan(&agentInstallJobsTable); err != nil {
		return err
	}
	if agentInstallJobsTable != 0 {
		return nil
	}

	var applied123 int
	if err := db.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 123 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied123); err != nil {
		return err
	}
	if applied123 == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM goose_db_version WHERE version_id = 123`); err != nil {
		return err
	}
	var applied130 int
	if err := tx.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 130 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied130); err != nil {
		return err
	}
	if applied130 == 0 {
		if _, err := tx.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (130, 1)`); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE pr SET review_partial = TRUE`); err != nil {
		return err
	}
	return tx.Commit()
}

func repairBurnedAutomationsMigrationHistory(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}

	var automationsTable, automationRunID, provisionColumns, discussionColumn int
	if err := db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'automations'),
		(SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'automation_run_id'),
		(SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name IN ('provision_state', 'provision_error')),
		(SELECT COUNT(*) FROM pragma_table_info('pr') WHERE name = 'discussion_comment_count')`).Scan(
		&automationsTable, &automationRunID, &provisionColumns, &discussionColumn,
	); err != nil {
		return err
	}
	applied := func(version int64) (bool, error) {
		var value int
		if err := db.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = ? ORDER BY id DESC LIMIT 1
), 0)`, version).Scan(&value); err != nil {
			return false, err
		}
		return value == 1, nil
	}
	applied159, err := applied(159)
	if err != nil {
		return err
	}
	applied161, err := applied(161)
	if err != nil {
		return err
	}
	applied156, err := applied(156)
	if err != nil {
		return err
	}
	applied162, err := applied(162)
	if err != nil {
		return err
	}
	if automationsTable > 0 && automationRunID > 0 {
		if applied156 && provisionColumns != 2 {
			if _, err := db.Exec(`DELETE FROM goose_db_version WHERE version_id = 156`); err != nil {
				return err
			}
		}
		if !applied161 {
			if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (161, 1)`); err != nil {
				return err
			}
		}
	} else if applied161 {
		if _, err := db.Exec(`DELETE FROM goose_db_version WHERE version_id = 161`); err != nil {
			return err
		}
	}
	if applied162 {
		return nil
	}
	if discussionColumn > 0 && !applied159 {
		_, err = db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (159, 1)`)
		return err
	}
	if discussionColumn == 0 && applied159 {
		_, err = db.Exec(`DELETE FROM goose_db_version WHERE version_id = 159`)
		return err
	}
	return nil
}

var schemaRepairs = []struct {
	version int64
	table   string
	column  string
	addDDL  string
	postAdd []string
}{
	{version: 40, table: "sessions", column: "diff_base_sha",
		addDDL: `ALTER TABLE sessions ADD COLUMN diff_base_sha TEXT NOT NULL DEFAULT ''`},
	{version: 40, table: "sessions", column: "diff_base_ref",
		addDDL: `ALTER TABLE sessions ADD COLUMN diff_base_ref TEXT NOT NULL DEFAULT ''`},
	{version: 41, table: "notifications", column: "resolved_at",
		addDDL: `ALTER TABLE notifications ADD COLUMN resolved_at TIMESTAMP`,
		postAdd: []string{
			`UPDATE notifications SET resolved_at = created_at WHERE status = 'read'`,
			`DROP INDEX IF EXISTS idx_notifications_unread_dedupe`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_notifications_open_dedupe
    ON notifications(session_id, type, pr_url)
    WHERE status = 'unread' OR resolved_at IS NULL`,
			`CREATE INDEX IF NOT EXISTS idx_notifications_unresolved
    ON notifications(resolved_at, created_at DESC, id DESC)`,
		}},
	{version: 42, table: "sessions", column: "reviewer_harness",
		addDDL: `ALTER TABLE sessions ADD COLUMN reviewer_harness TEXT NOT NULL DEFAULT ''`,
		postAdd: []string{
			`DROP INDEX IF EXISTS idx_review_run_session_pr_sha`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_review_run_session_pr_sha_harness
    ON review_run (session_id, pr_url, target_sha, harness)
    WHERE target_sha != ''
        AND status NOT IN ('failed', 'cancelled')
        AND (status = 'running' OR verdict NOT IN ('', 'changes_requested'))`,
		}},
	{version: 43, table: "sessions", column: "is_pinned",
		addDDL: `ALTER TABLE sessions ADD COLUMN is_pinned BOOLEAN NOT NULL DEFAULT 0`},
	{version: 43, table: "sessions", column: "pinned_at",
		addDDL: `ALTER TABLE sessions ADD COLUMN pinned_at DATETIME`,
		postAdd: []string{
			`DROP TRIGGER IF EXISTS sessions_cdc_update`,
			`CREATE TRIGGER sessions_cdc_update
AFTER UPDATE ON sessions
WHEN OLD.activity_state <> NEW.activity_state
    OR OLD.is_terminated <> NEW.is_terminated
    OR (OLD.first_signal_at IS NULL AND NEW.first_signal_at IS NOT NULL)
    OR OLD.preview_url <> NEW.preview_url
    OR OLD.preview_revision <> NEW.preview_revision
    OR OLD.display_name <> NEW.display_name
    OR OLD.terminate_on_pr_merge <> NEW.terminate_on_pr_merge
    OR OLD.is_pinned <> NEW.is_pinned
    OR OLD.pinned_at <> NEW.pinned_at
    OR (OLD.pinned_at IS NULL AND NEW.pinned_at IS NOT NULL)
    OR (OLD.pinned_at IS NOT NULL AND NEW.pinned_at IS NULL)
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated',
        json_object(
            'id', NEW.id,
            'activity', NEW.activity_state,
            'isTerminated', json(CASE WHEN NEW.is_terminated THEN 'true' ELSE 'false' END),
            'terminateOnPrMerge', json(CASE WHEN NEW.terminate_on_pr_merge THEN 'true' ELSE 'false' END),
            'previewUrl', NEW.preview_url,
            'previewRevision', NEW.preview_revision,
            'isPinned', json(CASE WHEN NEW.is_pinned THEN 'true' ELSE 'false' END)
        ),
        NEW.updated_at);
			END`,
		}},
	{version: 81, table: "sessions", column: "browser_capability_verifier",
		addDDL: `ALTER TABLE sessions ADD COLUMN browser_capability_verifier TEXT NOT NULL DEFAULT ''`},
	{version: 66, table: "conversations", column: "current_session_id",
		addDDL: `ALTER TABLE conversations ADD COLUMN current_session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL`,
		postAdd: []string{
			`UPDATE conversations SET current_session_id = session_id WHERE current_session_id IS NULL AND session_id IS NOT NULL`,
			`CREATE INDEX IF NOT EXISTS idx_conversations_current_session ON conversations(current_session_id)
    WHERE current_session_id IS NOT NULL`,
		}},
	{version: 86, table: "workspace_repos", column: "default_branch",
		addDDL: `ALTER TABLE workspace_repos ADD COLUMN default_branch TEXT NOT NULL DEFAULT ''`},
	{version: 89, table: "conversation_turns", column: "promotion_started_at",
		addDDL: `ALTER TABLE conversation_turns ADD COLUMN promotion_started_at TIMESTAMP`},
	{version: 89, table: "conversation_turns", column: "promoted_to_turn_id",
		addDDL: `ALTER TABLE conversation_turns ADD COLUMN promoted_to_turn_id TEXT REFERENCES conversation_turns(id) ON DELETE SET NULL`},
	{version: 106, table: "pr_comment", column: "review_id",
		addDDL: `ALTER TABLE pr_comment ADD COLUMN review_id TEXT NOT NULL DEFAULT ''`},
	{version: 108, table: "conversation_turns", column: "retry_of_turn_id",
		addDDL: `ALTER TABLE conversation_turns ADD COLUMN retry_of_turn_id TEXT REFERENCES conversation_turns(id) ON DELETE RESTRICT`,
		postAdd: []string{
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_conversation_turns_retry_source
    ON conversation_turns(conversation_id, retry_of_turn_id)
    WHERE retry_of_turn_id IS NOT NULL`,
		}},
	{version: 130, table: "pr", column: "review_partial",
		addDDL: `ALTER TABLE pr ADD COLUMN review_partial BOOLEAN NOT NULL DEFAULT TRUE`},
	{version: 132, table: "conversations", column: "opencode_mode",
		addDDL: `ALTER TABLE conversations ADD COLUMN opencode_mode TEXT NOT NULL DEFAULT ''`},
	{version: 199, table: "conversation_messages", column: "continuation",
		addDDL: `ALTER TABLE conversation_messages ADD COLUMN continuation INTEGER NOT NULL DEFAULT 0 CHECK (continuation IN (0, 1))`},
}

func reconcileSchema(db *sql.DB) error {
	for _, rc := range schemaRepairs {
		var count int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, rc.table, rc.column,
		).Scan(&count); err != nil {
			return fmt.Errorf("schema verification: inspect %s.%s: %w", rc.table, rc.column, err)
		}
		if count > 0 {
			continue
		}
		if _, err := db.Exec(rc.addDDL); err != nil {
			return fmt.Errorf(
				"schema repair: %s.%s is missing (a burned goose version skipped the migration that adds it, see #3475) and could not be added: %w",
				rc.table, rc.column, err,
			)
		}
		for _, stmt := range rc.postAdd {
			if _, err := db.Exec(stmt); err != nil {
				return fmt.Errorf("schema repair: replay skipped migration effects for %s.%s: %w", rc.table, rc.column, err)
			}
		}
	}
	if err := reconcileHarnessConstraint(db); err != nil {
		return err
	}
	var revisionColumn, revisionTrigger int
	if err := db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'revision'),
		(SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND tbl_name = 'sessions' AND name = 'sessions_revision_update')`).Scan(&revisionColumn, &revisionTrigger); err != nil {
		return fmt.Errorf("schema verification: inspect session revision fence: %w", err)
	}
	if revisionColumn > 0 && revisionTrigger != 1 {
		return errors.New("schema verification: sessions_revision_update trigger is missing; restore the session revision trigger before starting Orchestrator")
	}
	return nil
}

const (
	sessionsHarnessCheckWithoutMuse                   = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'kiro', 'kilocode', 'vibe', 'pi', 'autohand', 'fake'))`
	sessionsHarnessCheckWithMuse                      = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'autohand', 'fake'))`
	sessionsHarnessCheckWithoutMuseQM                 = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'kiro', 'kilocode', 'vibe', 'pi', 'autohand', 'qm', 'fake'))`
	sessionsHarnessCheckWithMuseQM                    = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'autohand', 'qm', 'fake'))`
	sessionsHarnessCheckWithMuseKimchi                = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'autohand', 'fake'))`
	sessionsHarnessCheckWithMuseQMKimchi              = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'autohand', 'qm', 'fake'))`
	sessionsHarnessCheckWithMusePrimeAgent            = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'prime-agent', 'autohand', 'fake'))`
	sessionsHarnessCheckWithMuseQMPrimeAgent          = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'prime-agent', 'autohand', 'qm', 'fake'))`
	sessionsHarnessCheckWithMuseQMLegacyPrimeAgent    = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'autohand', 'qm', 'prime-agent', 'fake'))`
	sessionsHarnessCheckWithMuseKimchiPrimeAgent      = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'prime-agent', 'autohand', 'fake'))`
	sessionsHarnessCheckWithMuseQMKimchiPrimeAgent    = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'prime-agent', 'autohand', 'qm', 'fake'))`
	sessionsHarnessCheckWithMuseKimchiPrimeAgentOMP   = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'prime-agent', 'autohand', 'omp', 'fake'))`
	sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMP = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'prime-agent', 'autohand', 'omp', 'qm', 'fake'))`
)

const (
	sessionsHarnessCheckWithMuseKimchiPrimeAgentOMPUnreal         = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'prime-agent', 'autohand', 'omp', 'unreal-agent', 'fake'))`
	sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMPUnreal       = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'prime-agent', 'autohand', 'omp', 'unreal-agent', 'qm', 'fake'))`
	sessionsHarnessCheckWithMuseKimchiPrimeAgentOMPUnrealMiMo     = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'prime-agent', 'autohand', 'omp', 'unreal-agent', 'mimo-code', 'fake'))`
	sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMPUnrealMiMo   = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'prime-agent', 'autohand', 'omp', 'unreal-agent', 'mimo-code', 'qm', 'fake'))`
	sessionsHarnessCheckWithMuseKimchiPrimeAgentOMPUnrealFX       = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'prime-agent', 'autohand', 'omp', 'unreal-agent', 'fx', 'fake'))`
	sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMPUnrealFX     = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'prime-agent', 'autohand', 'omp', 'unreal-agent', 'fx', 'qm', 'fake'))`
	sessionsHarnessCheckWithMuseKimchiPrimeAgentOMPUnrealFXMiMo   = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'prime-agent', 'autohand', 'omp', 'unreal-agent', 'fx', 'mimo-code', 'fake'))`
	sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMPUnrealFXMiMo = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'prime-agent', 'autohand', 'omp', 'unreal-agent', 'fx', 'mimo-code', 'qm', 'fake'))`
)

func reconcileHarnessConstraint(db *sql.DB) error {
	var schema string
	if err := db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`,
	).Scan(&schema); err != nil {
		return fmt.Errorf("schema verification: inspect sessions harness constraint: %w", err)
	}
	needsNativeAssistant := !strings.Contains(schema, "'hermes'")
	needsMuse := !strings.Contains(schema, "'muse'")
	needsKimchi := !strings.Contains(schema, "'kimchi'")
	needsPrimeAgent := !strings.Contains(schema, "'prime-agent'")
	needsOMP := !strings.Contains(schema, "'omp'")
	needsGemini := !strings.Contains(schema, "'gemini'")
	needsUnreal := !strings.Contains(schema, "'unreal-agent'")
	needsCodewhale := !strings.Contains(schema, "'codewhale'")
	needsMiMo := !strings.Contains(schema, "'mimo-code'")
	needsDeepSeek := !strings.Contains(schema, "'deepseek-harness'")
	needsOpenHands := !strings.Contains(schema, "'openhands'")
	needsDockerAgent := !strings.Contains(schema, "'docker-agent'")
	needsFX := !strings.Contains(schema, "'fx'")
	needsOpenCodeV2 := !strings.Contains(schema, "'opencode-v2'")
	needsMemcode := !strings.Contains(schema, "'memcode'")
	needsCommandCode := !strings.Contains(schema, "'command-code'")
	needsStakpak := !strings.Contains(schema, "'stakpak'")
	needsMiniMaxCode := !strings.Contains(schema, "'minimax-code'")
	if !needsNativeAssistant && !needsMemcode && !needsStakpak && !needsMuse && !needsKimchi && !needsPrimeAgent && !needsOMP && !needsGemini && !needsUnreal && !needsCodewhale && !needsMiMo && !needsDeepSeek && !needsOpenHands && !needsCommandCode && !needsDockerAgent && !needsFX && !needsOpenCodeV2 && !needsMiniMaxCode {
		return nil
	}
	if _, err := db.Exec(`PRAGMA writable_schema = ON`); err != nil {
		return fmt.Errorf("schema repair: enable writable_schema for sessions harness constraint: %w", err)
	}
	if !needsNativeAssistant {
		if _, err := db.Exec(`UPDATE sqlite_master SET sql = replace(sql, '''hermes'', ', '') WHERE type = 'table' AND name = 'sessions'`); err != nil {
			return fmt.Errorf("schema repair: normalize Hermes constraint: %w", err)
		}
		needsNativeAssistant = true
	}
	if !needsStakpak {
		if _, err := db.Exec(`UPDATE sqlite_master SET sql = replace(sql, '''stakpak'', ', '') WHERE type = 'table' AND name = 'sessions'`); err != nil {
			return fmt.Errorf("schema repair: normalize Stakpak constraint: %w", err)
		}
		needsStakpak = true
	}
	if !needsDockerAgent {
		if _, err := db.Exec(`UPDATE sqlite_master SET sql = replace(sql, '''docker-agent'', ', '') WHERE type = 'table' AND name = 'sessions'`); err != nil {
			return fmt.Errorf("schema repair: normalize Docker Agent constraint: %w", err)
		}
		needsDockerAgent = true
	}
	if !needsMiniMaxCode {
		if _, err := db.Exec(`UPDATE sqlite_master SET sql = replace(sql, '''minimax-code'', ', '') WHERE type = 'table' AND name = 'sessions'`); err != nil {
			return fmt.Errorf("schema repair: normalize MiniMax Code constraint: %w", err)
		}
		needsMiniMaxCode = true
	}
	type replacement struct {
		old string
		new string
	}
	var repairs []replacement
	if needsMuse {
		repairs = append(repairs,
			replacement{sessionsHarnessCheckWithoutMuse, sessionsHarnessCheckWithMuse},
			replacement{sessionsHarnessCheckWithoutMuseQM, sessionsHarnessCheckWithMuseQM},
		)
	}
	if needsKimchi {
		repairs = append(repairs,
			replacement{sessionsHarnessCheckWithMuse, sessionsHarnessCheckWithMuseKimchi},
			replacement{sessionsHarnessCheckWithMuseQM, sessionsHarnessCheckWithMuseQMKimchi},
			replacement{sessionsHarnessCheckWithMusePrimeAgent, sessionsHarnessCheckWithMuseKimchiPrimeAgent},
			replacement{sessionsHarnessCheckWithMuseQMPrimeAgent, sessionsHarnessCheckWithMuseQMKimchiPrimeAgent},
			replacement{sessionsHarnessCheckWithMuseQMLegacyPrimeAgent, sessionsHarnessCheckWithMuseQMKimchiPrimeAgent},
		)
	}
	if needsPrimeAgent {
		repairs = append(repairs,
			replacement{sessionsHarnessCheckWithMuseKimchi, sessionsHarnessCheckWithMuseKimchiPrimeAgent},
			replacement{sessionsHarnessCheckWithMuseQMKimchi, sessionsHarnessCheckWithMuseQMKimchiPrimeAgent},
		)
	}
	if needsOMP {
		repairs = append(repairs,
			replacement{sessionsHarnessCheckWithMuseKimchiPrimeAgent, sessionsHarnessCheckWithMuseKimchiPrimeAgentOMP},
			replacement{sessionsHarnessCheckWithMuseQMKimchiPrimeAgent, sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMP},
		)
	}
	if needsGemini {
		for _, old := range []string{
			sessionsHarnessCheckWithMuseKimchiPrimeAgentOMP,
			sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMP,
			sessionsHarnessCheckWithMuseKimchiPrimeAgentOMPUnreal,
			sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMPUnreal,
			sessionsHarnessCheckWithMuseKimchiPrimeAgentOMPUnrealFX,
			sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMPUnrealFX,
			sessionsHarnessCheckWithMuseKimchiPrimeAgentOMPUnrealMiMo,
			sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMPUnrealMiMo,
			sessionsHarnessCheckWithMuseKimchiPrimeAgentOMPUnrealFXMiMo,
			sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMPUnrealFXMiMo,
		} {
			repairs = append(repairs, replacement{old, strings.Replace(old, "'omp'", "'gemini', 'omp'", 1)})
		}
	}
	if needsUnreal {
		repairs = append(repairs,
			replacement{sessionsHarnessCheckWithMuseKimchiPrimeAgentOMP, sessionsHarnessCheckWithMuseKimchiPrimeAgentOMPUnreal},
			replacement{sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMP, sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMPUnreal},
		)
		for _, old := range []string{sessionsHarnessCheckWithMuseKimchiPrimeAgentOMP, sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMP} {
			withGemini := strings.Replace(old, "'omp'", "'gemini', 'omp'", 1)
			repairs = append(repairs, replacement{withGemini, strings.Replace(withGemini, "'omp'", "'omp', 'unreal-agent'", 1)})
		}
	}
	if needsMiMo {
		withoutGemini := []replacement{
			replacement{sessionsHarnessCheckWithMuseKimchiPrimeAgentOMPUnreal, sessionsHarnessCheckWithMuseKimchiPrimeAgentOMPUnrealMiMo},
			replacement{sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMPUnreal, sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMPUnrealMiMo},
			replacement{sessionsHarnessCheckWithMuseKimchiPrimeAgentOMPUnrealFX, sessionsHarnessCheckWithMuseKimchiPrimeAgentOMPUnrealFXMiMo},
			replacement{sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMPUnrealFX, sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMPUnrealFXMiMo},
		}
		for _, r := range withoutGemini {
			repairs = append(repairs, r, replacement{
				strings.Replace(r.old, "'omp'", "'gemini', 'omp'", 1),
				strings.Replace(r.new, "'omp'", "'gemini', 'omp'", 1),
			})
		}
	}
	if needsDeepSeek {
		repairs = append(repairs, replacement{"'fake'))", "'deepseek-harness', 'fake'))"})
	}
	if needsOpenHands {
		repairs = append(repairs, replacement{"'fake'))", "'openhands', 'fake'))"})
	}
	if needsCodewhale {
		repairs = append(repairs, replacement{"'fake'))", "'codewhale', 'fake'))"})
	}
	if needsCommandCode {
		repairs = append(repairs, replacement{"'fake'))", "'command-code', 'fake'))"})
	}
	if needsFX {
		repairs = append(repairs, replacement{"'fake'))", "'fx', 'fake'))"})
	}
	if needsOpenCodeV2 {
		repairs = append(repairs, replacement{"'fake'))", "'opencode-v2', 'fake'))"})
	}
	if needsNativeAssistant {
		repairs = append(repairs, replacement{"'fake'))", "'hermes', 'fake'))"})
	}
	if needsMemcode {
		repairs = append(repairs, replacement{"'fake'))", "'memcode', 'fake'))"})
	}
	if needsStakpak {
		repairs = append(repairs, replacement{"'fake'))", "'stakpak', 'fake'))"})
	}
	if needsDockerAgent {
		repairs = append(repairs, replacement{"'fake'))", "'docker-agent', 'fake'))"})
	}
	if needsMiniMaxCode {
		repairs = append(repairs, replacement{"'fake'))", "'minimax-code', 'fake'))"})
	}
	for _, r := range repairs {
		if _, err := db.Exec(
			`UPDATE sqlite_master
SET sql = replace(sql, ?, ?)
WHERE type = 'table' AND name = 'sessions'`,
			r.old,
			r.new,
		); err != nil {
			return fmt.Errorf("schema repair: widen sessions harness constraint: %w", err)
		}
	}
	if _, err := db.Exec(`PRAGMA writable_schema = RESET`); err != nil {
		return fmt.Errorf("schema repair: reparse sessions harness constraint: %w", err)
	}
	if err := db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`,
	).Scan(&schema); err != nil {
		return fmt.Errorf("schema verification: inspect repaired sessions harness constraint: %w", err)
	}
	if !strings.Contains(schema, "'muse'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing Muse and did not match known pre-Muse schema")
	}
	if !strings.Contains(schema, "'kimchi'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing Kimchi and did not match known pre-Kimchi schema")
	}
	if !strings.Contains(schema, "'prime-agent'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing Prime Agent and did not match known pre-Prime-Agent schema")
	}
	if !strings.Contains(schema, "'omp'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing OMP and did not match known pre-OMP schema")
	}
	if !strings.Contains(schema, "'gemini'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing Gemini and did not match known pre-Gemini schema")
	}
	if !strings.Contains(schema, "'unreal-agent'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing Unreal Agent and did not match known pre-Unreal-Agent schema")
	}
	if !strings.Contains(schema, "'mimo-code'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing MiMo Code and did not match known pre-MiMo-Code schema")
	}
	if !strings.Contains(schema, "'deepseek-harness'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing DeepSeek Harness and did not match known pre-DeepSeek schema")
	}
	if !strings.Contains(schema, "'codewhale'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing Codewhale")
	}
	if !strings.Contains(schema, "'openhands'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing OpenHands and did not match known pre-OpenHands schema")
	}
	if !strings.Contains(schema, "'memcode'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing Memcode and did not match known pre-Memcode schema")
	}
	if !strings.Contains(schema, "'minimax-code'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing MiniMax Code and did not match known pre-MiniMax-Code schema")
	}
	if !strings.Contains(schema, "'docker-agent'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing Docker Agent")
	}
	if !strings.Contains(schema, "'command-code'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing Command Code and did not match known pre-Command-Code schema")
	}
	if !strings.Contains(schema, "'hermes'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing hermes")
	}
	if !strings.Contains(schema, "'stakpak'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing Stakpak")
	}
	return nil
}
