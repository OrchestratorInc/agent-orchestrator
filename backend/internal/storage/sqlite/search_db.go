package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	moderncsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const (
	searchDatabaseFile  = "search.db"
	searchSchemaName    = "searchidx"
	searchSchemaVersion = 1
)

const searchPragmas = "?_pragma=journal_mode(WAL)" +
	"&_pragma=busy_timeout(5000)" +
	"&_pragma=synchronous(NORMAL)" +
	"&_pragma=journal_size_limit(33554432)"

const sessionSearchSwitch = "AO_SESSION_SEARCH"

var errSessionSearchOff = errors.New("session search is turned off (" + sessionSearchSwitch + "=off)")

func sessionSearchSwitchedOff() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(sessionSearchSwitch)), "off")
}

func searchDatabasePath(dataDir string) string {
	return filepath.Join(dataDir, searchDatabaseFile)
}

func searchDatabaseURI(dataDir string) string {
	file := url.URL{Path: searchDatabasePath(dataDir)}
	return "file:" + file.EscapedPath()
}

func openSearchWriter(dataDir string, appWriter *sql.DB) (db *sql.DB, reset func(context.Context) error, err error) {
	owner, err := searchIndexOwner(appWriter)
	if err != nil {
		return nil, nil, err
	}
	db, err = openSearchFile(dataDir, owner)
	if err != nil && searchFileDamaged(err) {
		if removeErr := removeSearchFiles(dataDir); removeErr != nil {
			return nil, nil, errors.Join(err, removeErr)
		}
		db, err = openSearchFile(dataDir, owner)
	}
	if err != nil {
		return nil, nil, err
	}
	return db, func(ctx context.Context) error { return resetSearchSchema(ctx, db, owner) }, nil
}

func openSearchFile(dataDir, owner string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", searchDatabaseURI(dataDir)+searchPragmas)
	if err != nil {
		return nil, fmt.Errorf("open session search database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := ensureSearchSchema(db, owner); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func searchFileDamaged(err error) bool {
	var sqliteErr *moderncsqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	switch sqliteErr.Code() & 0xff {
	case sqlite3.SQLITE_CORRUPT, sqlite3.SQLITE_NOTADB:
		return true
	}
	return false
}

func removeSearchFiles(dataDir string) error {
	path := searchDatabasePath(dataDir)
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove damaged session search database: %w", err)
		}
	}
	return nil
}

func searchIndexOwner(appWriter *sql.DB) (string, error) {
	ctx := context.Background()
	if _, err := appWriter.ExecContext(ctx,
		`INSERT OR IGNORE INTO session_search_owner (id, token) VALUES (1, lower(hex(randomblob(16))))`,
	); err != nil {
		return "", fmt.Errorf("create session search owner: %w", err)
	}
	var token string
	if err := appWriter.QueryRowContext(ctx, `SELECT token FROM session_search_owner WHERE id = 1`).Scan(&token); err != nil {
		return "", fmt.Errorf("read session search owner: %w", err)
	}
	return token, nil
}

func ensureSearchSchema(db *sql.DB, owner string) error {
	ctx := context.Background()
	var version int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read session search schema version: %w", err)
	}
	if version == searchSchemaVersion {
		var recorded string
		err := db.QueryRowContext(ctx, `SELECT value FROM session_search_meta WHERE key = 'owner'`).Scan(&recorded)
		if err == nil && recorded == owner {
			return nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read session search owner: %w", err)
		}
	}
	return resetSearchSchema(ctx, db, owner)
}

func resetSearchSchema(ctx context.Context, db *sql.DB, owner string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin session search schema: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range append(dropSearchSchema, createSearchSchema...) {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create session search schema: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO session_search_meta (key, value) VALUES ('owner', ?)`, owner); err != nil {
		return fmt.Errorf("record session search owner: %w", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, searchSchemaVersion)); err != nil {
		return fmt.Errorf("set session search schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit session search schema: %w", err)
	}
	return nil
}

var dropSearchSchema = []string{
	`DROP TRIGGER IF EXISTS session_search_docs_ad`,
	`DROP TABLE IF EXISTS session_search_vocab`,
	`DROP TABLE IF EXISTS session_search_fts`,
	`DROP TABLE IF EXISTS session_search_text`,
	`DROP TABLE IF EXISTS session_search_docs`,
	`DROP TABLE IF EXISTS session_search_sources`,
	`DROP TABLE IF EXISTS session_search_meta`,
}

var createSearchSchema = []string{
	`CREATE TABLE session_search_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
)`,
	`CREATE TABLE session_search_docs (
    id          INTEGER PRIMARY KEY,
    -- source_key identifies the unit's origin: 'chat:<sessionId>' for AO Chat,
    -- 'native:<harness>:<sessionId>' for a provider transcript.
    source_key  TEXT NOT NULL,
    -- doc_key is stable within its source: a message id, an activity id, a
    -- transcript byte offset, or 'title'/'paths' for the synthetic docs.
    doc_key     TEXT NOT NULL,
    session_id  TEXT NOT NULL DEFAULT '',
    project_id  TEXT NOT NULL DEFAULT '',
    harness     TEXT NOT NULL DEFAULT '',
    -- kind is what the row is; ranking weighs bm25 by it.
    kind        TEXT NOT NULL CHECK (kind IN (
        'title', 'user', 'assistant', 'tool', 'command', 'path'
    )),
    -- message_id is the Chat timeline anchor a result opens at.
    message_id  TEXT NOT NULL DEFAULT '',
    -- seq is the conversation sequence (Chat) or the transcript byte offset.
    seq         INTEGER NOT NULL DEFAULT 0,
    -- Unix milliseconds, so range filters compare numerically.
    created_at  INTEGER NOT NULL,
    indexed_at  INTEGER NOT NULL,
    -- fingerprint hashes everything the indexer wrote for the row, so a pass
    -- rewrites only the documents that changed.
    fingerprint INTEGER NOT NULL DEFAULT 0,
    UNIQUE (source_key, doc_key, kind)
)`,
	`CREATE INDEX idx_session_search_docs_session ON session_search_docs (session_id)`,
	`CREATE TABLE session_search_text (
    id     INTEGER PRIMARY KEY,
    title  TEXT NOT NULL DEFAULT '',
    paths  TEXT NOT NULL DEFAULT '',
    body   BLOB,
    hash   INTEGER NOT NULL DEFAULT 0
)`,
	`CREATE VIRTUAL TABLE session_search_fts USING fts5(
    title,
    body,
    paths,
    content='',
    tokenize='unicode61 remove_diacritics 2'
)`,
	`CREATE TRIGGER session_search_docs_ad AFTER DELETE ON session_search_docs BEGIN
    INSERT INTO session_search_fts (session_search_fts, rowid, title, body, paths)
    SELECT 'delete', t.id, t.title, ao_search_body(t.body), t.paths
    FROM session_search_text t WHERE t.id = old.id;
    DELETE FROM session_search_text WHERE id = old.id;
END`,
	`CREATE TABLE session_search_sources (
    source_key      TEXT PRIMARY KEY,
    kind            TEXT NOT NULL CHECK (kind IN ('chat', 'native')),
    session_id      TEXT NOT NULL DEFAULT '',
    project_id      TEXT NOT NULL DEFAULT '',
    harness         TEXT NOT NULL DEFAULT '',
    path            TEXT NOT NULL DEFAULT '',
    -- the conversation sequence (chat) or byte offset (native)
    cursor_value    INTEGER NOT NULL DEFAULT 0,
    file_size       INTEGER NOT NULL DEFAULT 0,
    file_mtime      INTEGER NOT NULL DEFAULT 0,
    doc_count       INTEGER NOT NULL DEFAULT 0,
    -- the exact content the last pass saw (Chat)
    fingerprint     TEXT NOT NULL DEFAULT '',
    -- a cheap aggregate witness compared before reading any text (Chat)
    stamp           TEXT NOT NULL DEFAULT '',
    state           TEXT NOT NULL DEFAULT 'pending'
        CHECK (state IN ('pending', 'active', 'unavailable', 'unsupported', 'no_transcript')),
    last_error_code TEXT NOT NULL DEFAULT '',
    updated_at      INTEGER NOT NULL
)`,
	`CREATE INDEX idx_session_search_sources_state ON session_search_sources (state, updated_at)`,
}

type searchAttachingConnector struct {
	dsn        string
	searchPath string
	driver     driver.Driver
}

func newSearchAttachingConnector(dataDir, dsn string) (*searchAttachingConnector, error) {
	probe, err := sql.Open("sqlite", "")
	if err != nil {
		return nil, fmt.Errorf("resolve sqlite driver: %w", err)
	}
	sqliteDriver := probe.Driver()
	_ = probe.Close()
	return &searchAttachingConnector{dsn: dsn, searchPath: searchDatabasePath(dataDir), driver: sqliteDriver}, nil
}

func (c *searchAttachingConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.driver.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	execer, ok := conn.(driver.ExecerContext)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("sqlite connection cannot attach the session search database")
	}
	if _, err := execer.ExecContext(ctx, `ATTACH DATABASE ? AS `+searchSchemaName,
		[]driver.NamedValue{{Ordinal: 1, Value: c.searchPath}}); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("attach session search database: %w", err)
	}
	return conn, nil
}

func (c *searchAttachingConnector) Driver() driver.Driver { return c.driver }

const maxSearchReaders = 4

func openSearchReaders(dataDir, dsn string) (*sql.DB, error) {
	connector, err := newSearchAttachingConnector(dataDir, dsn)
	if err != nil {
		return nil, err
	}
	readDB := sql.OpenDB(connector)
	readDB.SetMaxOpenConns(maxSearchReaders)
	readDB.SetMaxIdleConns(maxSearchReaders)
	return readDB, nil
}
