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

// The session search index lives in its own database file, search.db, beside
// ao.db.
//
// SQLite allows one writer per database file. Indexing writes a lot, and FTS5
// merges its segments inside whichever commit crosses a threshold, so in ao.db
// those transactions would make the daemon's other writes (chat streaming,
// session state) wait. In its own file the index holds only its own write
// lock, which nothing else takes.
//
// Search queries run on their own reader pool, whose connections attach
// search.db so a query can filter documents by the sessions they belong to.
// The app's other readers never attach it: search is optional, and a
// search.db that cannot be opened or attached turns search off (Open records
// why) without touching anything else.
//
// The index is derived data. A schema change bumps searchSchemaVersion, which
// clears the file and lets the indexer rebuild it; ao.db migrations never
// touch it. search.db also records which ao.db it indexes
// (session_search_owner's token): beside a restored or swapped ao.db it is
// cleared the same way.
const (
	searchDatabaseFile = "search.db"
	// searchSchemaName is the name search.db is attached under.
	searchSchemaName    = "searchidx"
	searchSchemaVersion = 1
)

// searchPragmas mirror ao.db's: WAL so readers never wait for the indexer,
// NORMAL sync, and a cap on the size the -wal file is cut back to.
const searchPragmas = "?_pragma=journal_mode(WAL)" +
	"&_pragma=busy_timeout(5000)" +
	"&_pragma=synchronous(NORMAL)" +
	"&_pragma=journal_size_limit(33554432)"

// sessionSearchSwitch is the environment variable that turns session search
// off. With AO_SESSION_SEARCH=off the daemon never opens search.db and search
// answers "unavailable"; it is a support lever for a machine where the index
// misbehaves, not a user preference.
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

// openSearchWriter opens search.db's single writer connection and brings its
// schema to searchSchemaVersion for the ao.db that appWriter writes.
//
// The index is derived data: a search.db SQLite reports as corrupt or not a
// database is removed and rebuilt by the indexer. Any other failure (a locked
// file, a full disk, a permission) is returned, and Open turns search off
// rather than fail.
//
// It also returns reset, which empties the index in place (resetSearchSchema)
// for `ao search --reindex`.
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

// searchFileDamaged reports whether err says search.db's contents are unusable.
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

// removeSearchFiles deletes search.db and its WAL files.
func removeSearchFiles(dataDir string) error {
	path := searchDatabasePath(dataDir)
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove damaged session search database: %w", err)
		}
	}
	return nil
}

// searchIndexOwner returns ao.db's search index token, creating it once.
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

// ensureSearchSchema creates the index's tables, or clears and recreates them
// when the file holds another version's or indexes another ao.db.
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

// resetSearchSchema drops every index table and creates them empty, for the
// ao.db that owner names. The indexer then rebuilds the index from scratch:
// it is how a schema change, a swapped ao.db and `ao search --reindex` start
// over.
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

// dropSearchSchema removes every object any version of the index created.
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
	// owner: the session_search_owner token of the ao.db this file indexes.
	`CREATE TABLE session_search_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
)`,
	// One row per indexable unit: a message (or one chunk of a long one), an
	// activity, a transcript record, a session title, a path list.
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
	// The text, compressed (searchtext.Encode), apart from the metadata that
	// ranking scans; hash skips rewriting an unchanged document's index row.
	`CREATE TABLE session_search_text (
    id     INTEGER PRIMARY KEY,
    title  TEXT NOT NULL DEFAULT '',
    paths  TEXT NOT NULL DEFAULT '',
    body   BLOB,
    hash   INTEGER NOT NULL DEFAULT 0
)`,
	// Contentless: postings only. A row is removed with FTS5's 'delete'
	// command and the exact text it was indexed with (contentless_delete's
	// tombstones would skew bm25 until segments merge).
	`CREATE VIRTUAL TABLE session_search_fts USING fts5(
    title,
    body,
    paths,
    content='',
    tokenize='unicode61 remove_diacritics 2'
)`,
	// Deleting a document removes its index row and its text. ao_search_body()
	// is registered for every connection (search_functions.go).
	`CREATE TRIGGER session_search_docs_ad AFTER DELETE ON session_search_docs BEGIN
    INSERT INTO session_search_fts (session_search_fts, rowid, title, body, paths)
    SELECT 'delete', t.id, t.title, ao_search_body(t.body), t.paths
    FROM session_search_text t WHERE t.id = old.id;
    DELETE FROM session_search_text WHERE id = old.id;
END`,
	// One cursor row per source the indexer has seen.
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

// searchAttachingConnector opens reader connections to ao.db with search.db
// attached, so queries can join the index to sessions.
type searchAttachingConnector struct {
	dsn        string
	searchPath string
	driver     driver.Driver
}

func newSearchAttachingConnector(dataDir, dsn string) (*searchAttachingConnector, error) {
	// The registered "sqlite" driver, with ao_search_body and the other
	// functions registered on it.
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

// maxSearchReaders bounds the search reader pool. Searches are short and
// interactive (the palette, ao search); the pool opens connections lazily, so
// an idle daemon holds none beyond the first search.
const maxSearchReaders = 4

// openSearchReaders opens the search-only reader pool: connections to ao.db
// with search.db attached, so index queries can join documents to sessions.
// It is separate from the app's reader pool on purpose: if search.db cannot be
// attached, only these connections fail.
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
