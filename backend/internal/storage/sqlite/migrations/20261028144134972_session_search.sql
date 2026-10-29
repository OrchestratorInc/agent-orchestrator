-- +goose Up
-- Session search: a local, incrementally maintained full-text index over past
-- AO sessions. It is READ-ONLY toward session data: it owns only the tables
-- below and never writes sessions, conversations, transcripts, or harness
-- files.
--
-- session_search_docs is the durable, queryable projection (one row per
-- indexable unit) and session_search_text holds each unit's text.
-- session_search_fts is the FTS5 index over that text, maintained by the
-- triggers in this migration so the index cannot drift from the rows it owns.
-- session_search_sources records the incremental cursor per source so a
-- restart resumes instead of re-reading every transcript.

-- +goose StatementBegin
CREATE TABLE session_search_docs (
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
    -- machine is the host that owns the session. Only the local daemon's index
    -- is searched today; the column exists so the machine: operator filters
    -- rather than silently ignoring its argument.
    machine     TEXT NOT NULL DEFAULT '',
    -- kind is what the row is. The ranking weighs bm25 by kind, so this column
    -- is load-bearing: a session name counts for more than a user message, which
    -- counts for more than an agent reply, then tool and command output.
    kind        TEXT NOT NULL CHECK (kind IN (
        'title', 'user', 'assistant', 'tool', 'command', 'path'
    )),
    -- message_id is the Chat timeline anchor a result opens at. Empty for
    -- native transcripts and for activity rows that have no message bubble.
    message_id  TEXT NOT NULL DEFAULT '',
    -- seq is the conversation sequence (Chat) or the transcript byte offset
    -- (native). Incremental passes rewrite a source and prune by indexed_at, so
    -- an edited, streamed, or rolled-back row cannot leave stale text behind.
    seq         INTEGER NOT NULL DEFAULT 0,
    -- created_at and indexed_at are Unix milliseconds rather than SQLite
    -- timestamp text: range filters compare them numerically, and indexed_at is
    -- the witness for "did this pass rewrite this document".
    created_at  INTEGER NOT NULL,
    indexed_at  INTEGER NOT NULL,
    UNIQUE (source_key, doc_key, kind)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_session_search_docs_session ON session_search_docs (session_id);
CREATE INDEX idx_session_search_docs_project ON session_search_docs (project_id, created_at DESC);
CREATE INDEX idx_session_search_docs_source ON session_search_docs (source_key);
-- +goose StatementEnd

-- +goose StatementBegin
-- The text of each document lives apart from its metadata. Ranking reads the
-- metadata of every candidate (session, kind, time), and a common word in a
-- real archive has thousands of candidates. With an 8 KiB body in the same
-- row, each of those reads walked the body's overflow pages: on a 22,000
-- document archive one keystroke read tens of megabytes and took over a
-- second. Narrow metadata rows make the same scan a few hundred pages.
CREATE TABLE session_search_text (
    -- id is the document's id in session_search_docs.
    id     INTEGER PRIMARY KEY,
    title  TEXT NOT NULL DEFAULT '',
    body   TEXT NOT NULL DEFAULT '',
    paths  TEXT NOT NULL DEFAULT ''
);
-- +goose StatementEnd

-- +goose StatementBegin
-- External-content FTS5: the index stores no copy of the text, it references
-- session_search_text by rowid. bm25() weights title highest, then paths, then
-- body; the tokenizer folds diacritics so "café"/"cafe" match. Identifiers and
-- paths are split before they reach the body, so "handleSubmit" is findable as
-- both "handleSubmit" and "submit".
CREATE VIRTUAL TABLE session_search_fts USING fts5(
    title,
    body,
    paths,
    content='session_search_text',
    content_rowid='id',
    tokenize='unicode61 remove_diacritics 2'
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER session_search_text_ai AFTER INSERT ON session_search_text BEGIN
    INSERT INTO session_search_fts (rowid, title, body, paths)
    VALUES (new.id, new.title, new.body, new.paths);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER session_search_text_ad AFTER DELETE ON session_search_text BEGIN
    INSERT INTO session_search_fts (session_search_fts, rowid, title, body, paths)
    VALUES ('delete', old.id, old.title, old.body, old.paths);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER session_search_text_au AFTER UPDATE ON session_search_text BEGIN
    INSERT INTO session_search_fts (session_search_fts, rowid, title, body, paths)
    VALUES ('delete', old.id, old.title, old.body, old.paths);
    INSERT INTO session_search_fts (rowid, title, body, paths)
    VALUES (new.id, new.title, new.body, new.paths);
END;
-- +goose StatementEnd

-- +goose StatementBegin
-- A document's text goes with it, and its text's trigger removes it from the
-- FTS index, so deleting a document can never leave a searchable orphan.
CREATE TRIGGER session_search_docs_ad AFTER DELETE ON session_search_docs BEGIN
    DELETE FROM session_search_text WHERE id = old.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
-- One cursor row per source the indexer has seen. Chat sources advance by
-- conversation sequence; native sources advance by byte offset into a
-- transcript whose identity (size/mtime) is recorded so a rewritten or
-- truncated file resets cleanly instead of mis-reading from a stale offset.
CREATE TABLE session_search_sources (
    source_key      TEXT PRIMARY KEY,
    -- kind is 'chat' or 'native'.
    kind            TEXT NOT NULL CHECK (kind IN ('chat', 'native')),
    session_id      TEXT NOT NULL DEFAULT '',
    project_id      TEXT NOT NULL DEFAULT '',
    harness         TEXT NOT NULL DEFAULT '',
    path            TEXT NOT NULL DEFAULT '',
    -- cursor_value is the conversation sequence (chat) or byte offset (native).
    cursor_value    INTEGER NOT NULL DEFAULT 0,
    file_size       INTEGER NOT NULL DEFAULT 0,
    file_mtime      INTEGER NOT NULL DEFAULT 0,
    doc_count       INTEGER NOT NULL DEFAULT 0,
    -- fingerprint identifies the exact set of source content the last pass saw.
    -- For Chat it hashes the visible message ids and activity revisions, which
    -- is what makes a rollback or an edit re-index even though the sequence
    -- head did not move. Native sources leave it empty and use the byte offset.
    fingerprint     TEXT NOT NULL DEFAULT '',
    -- stamp is a cheap aggregate witness of a Chat conversation: its row counts,
    -- highest sequence in each timeline table, and newest row time. The sweep
    -- compares it before reading any text, so an idle pass over an unchanged
    -- conversation costs one grouped query for the whole archive instead of a
    -- full timeline read per conversation.
    stamp           TEXT NOT NULL DEFAULT '',
    -- state is 'pending', 'active', 'unavailable', 'unsupported', or
    -- 'no_transcript'. no_transcript is a session of an indexed harness with no
    -- resolvable transcript (a Chat session, or a TUI session whose workspace was
    -- cleaned up); it is deliberately distinct from unsupported so coverage does
    -- not claim the whole harness is unindexed.
    state           TEXT NOT NULL DEFAULT 'pending'
        CHECK (state IN ('pending', 'active', 'unavailable', 'unsupported', 'no_transcript')),
    last_error_code TEXT NOT NULL DEFAULT '',
    -- updated_at is Unix milliseconds, matching the document timestamps so a
    -- pass timestamp can be compared against the source that produced it.
    updated_at      INTEGER NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_session_search_sources_state ON session_search_sources (state, updated_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS session_search_sources;
DROP TRIGGER IF EXISTS session_search_docs_ad;
DROP TRIGGER IF EXISTS session_search_text_au;
DROP TRIGGER IF EXISTS session_search_text_ad;
DROP TRIGGER IF EXISTS session_search_text_ai;
DROP TABLE IF EXISTS session_search_fts;
DROP TABLE IF EXISTS session_search_text;
DROP TABLE IF EXISTS session_search_docs;
-- +goose StatementEnd