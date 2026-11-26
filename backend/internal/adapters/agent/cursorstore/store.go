// Package cursorstore reads and relocates Cursor Agent's native SQLite chats.
package cursorstore

import (
	"context"
	"crypto/md5" //nolint:gosec // Cursor's native directory key is MD5(cwd).
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite" // Register the native SQLite reader.
)

const (
	maxBlob  = 64 << 20
	maxStore = 1 << 30
	maxText  = 16 << 20
)

// Message is visible conversation text, excluding tool and internal state.
type Message struct {
	Role string
	Text string
	At   time.Time
}

// Archive describes one complete native conversation.
type Archive struct {
	ID       string
	CWD      string
	Title    string
	Updated  time.Time
	Messages []Message
}

type metadata struct {
	ID      string `json:"agentId"`
	Root    string `json:"latestRootBlobId"`
	Name    string `json:"name"`
	Created int64  `json:"createdAt"`
}

// SessionPath matches Cursor's md5(resolve(process.cwd())) directory layout.
func SessionPath(root, cwd, id string) (string, error) {
	if _, err := uuid.Parse(id); err != nil || filepath.Base(id) != id {
		return "", errors.New("invalid cursor session id")
	}
	root, err := providerRoot(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	sum := md5.Sum([]byte(resolved)) //nolint:gosec // Native compatibility, not cryptography.
	return filepath.Join(root, "chats", hex.EncodeToString(sum[:]), id, "store.db"), nil
}

// Layout selects where Cursor looks up a chat it resumes. The terminal CLI keys
// chats by the md5 of its launch directory; `cursor-agent acp` keeps them in
// acp-sessions/<id> and loads them by id, running tools in the requested cwd.
// Both read the same SQLite store format.
type Layout int

// The two places Cursor resumes a chat from.
const (
	// LayoutTerminal is the terminal CLI's chats/<md5 of cwd>/<id> layout.
	LayoutTerminal Layout = iota
	// LayoutACP is `cursor-agent acp`'s acp-sessions/<id> layout.
	LayoutACP
)

// ACPSessionPath matches `cursor-agent acp`'s acp-sessions/<id>/store.db layout.
func ACPSessionPath(root, id string) (string, error) {
	if _, err := uuid.Parse(id); err != nil || filepath.Base(id) != id {
		return "", errors.New("invalid cursor session id")
	}
	root, err := providerRoot(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "acp-sessions", id, "store.db"), nil
}

// SessionID is the id the copy of native chat id resumes under. The terminal
// layout keys copies by launch directory, so they keep the native id. ACP keys
// them by id alone, so each launch directory and source version gets its own
// stable id: a second import of the same chat, from this or another AO data
// directory, must not collide with the first copy, which holds that import's
// later turns.
func (l Layout) SessionID(id, cwd, expected string) string {
	if l != LayoutACP {
		return id
	}
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("ao-cursor-acp\x00"+id+"\x00"+cwd+"\x00"+expected)).String()
}

func (l Layout) path(root, cwd, id, expected string) (string, error) {
	if l == LayoutACP {
		return ACPSessionPath(root, l.SessionID(id, cwd, expected))
	}
	return SessionPath(root, cwd, id)
}

func providerRoot(root string) (string, error) {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}

func confined(root, path string) (string, error) {
	base, err := providerRoot(root)
	if err != nil {
		return "", err
	}
	actual, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	actual, err = filepath.Abs(actual)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(base, actual)
	if err != nil || !filepath.IsLocal(rel) || filepath.Base(actual) != "store.db" {
		return "", errors.New("cursor database is outside provider home")
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, statErr := os.Lstat(actual + suffix)
		if errors.Is(statErr, os.ErrNotExist) && suffix != "" {
			continue
		}
		if statErr != nil {
			return "", statErr
		}
		if !info.Mode().IsRegular() || info.Size() > maxStore {
			return "", errors.New("invalid cursor database file")
		}
	}
	return actual, nil
}

func snapshot(ctx context.Context, root, path string) (*sql.DB, *sql.Tx, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, "", err
	}
	path, err := confined(root, path)
	if err != nil {
		return nil, nil, "", err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	db, err := sql.Open("sqlite", u.String()+"?mode=ro&_pragma=busy_timeout(1000)")
	if err != nil {
		return nil, nil, "", err
	}
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		_ = db.Close()
		return nil, nil, "", err
	}
	return db, tx, path, nil
}

// Read follows only the active root's native turn graph.
func Read(ctx context.Context, root, path string) (Archive, error) {
	db, tx, actual, err := snapshot(ctx, root, path)
	if err != nil {
		return Archive{}, err
	}
	defer func() { _ = db.Close() }()
	defer func() { _ = tx.Rollback() }()
	return read(ctx, tx, actual)
}

func readMetadata(ctx context.Context, tx *sql.Tx, path string) (metadata, error) {
	var encoded string
	if err := tx.QueryRowContext(ctx, "SELECT CASE WHEN length(value)<=2097152 THEN value END FROM meta WHERE key='0'").Scan(&encoded); err != nil {
		return metadata{}, err
	}
	body, err := hex.DecodeString(encoded)
	if err != nil {
		return metadata{}, err
	}
	var meta metadata
	if err := json.Unmarshal(body, &meta); err != nil {
		return metadata{}, err
	}
	if meta.ID != filepath.Base(filepath.Dir(path)) || meta.Root == "" {
		return metadata{}, errors.New("cursor native identity or root is missing")
	}
	if _, err := uuid.Parse(meta.ID); err != nil {
		return metadata{}, err
	}
	return meta, nil
}

// Verify checks native backing state without requiring a supported display
// projection. Later native turns can use text/blob formats the importer omits.
func Verify(ctx context.Context, root, path, id string) error {
	db, tx, actual, err := snapshot(ctx, root, path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	defer func() { _ = tx.Rollback() }()
	meta, err := readMetadata(ctx, tx, actual)
	if err != nil {
		return err
	}
	if meta.ID != id {
		return errors.New("cursor native identity differs from the selected conversation")
	}
	rootID, err := hex.DecodeString(meta.Root)
	if err != nil {
		return err
	}
	reader := graphReader{ctx: ctx, tx: tx}
	_, err = reader.blob(rootID)
	return err
}

func read(ctx context.Context, tx *sql.Tx, path string) (Archive, error) {
	meta, err := readMetadata(ctx, tx, path)
	if err != nil {
		return Archive{}, err
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return Archive{}, err
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open("meta.json")
	if err != nil {
		return Archive{}, err
	}
	defer func() { _ = f.Close() }()
	var sidecar struct {
		CWD     string `json:"cwd"`
		Updated int64  `json:"updatedAtMs"`
	}
	if err := json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&sidecar); err != nil {
		return Archive{}, err
	}
	if !filepath.IsAbs(sidecar.CWD) {
		return Archive{}, errors.New("cursor native workspace is missing")
	}
	out := Archive{ID: meta.ID, CWD: sidecar.CWD, Title: meta.Name, Updated: time.UnixMilli(sidecar.Updated)}
	if sidecar.Updated == 0 {
		out.Updated = time.UnixMilli(meta.Created)
	}
	rootID, err := hex.DecodeString(meta.Root)
	if err != nil {
		return Archive{}, err
	}
	graph := graphReader{ctx: ctx, tx: tx}
	fields, err := graph.blob(rootID)
	if err != nil {
		return Archive{}, err
	}
	for _, turnID := range fields[8] {
		turn, err := graph.blob(turnID)
		if err != nil {
			return Archive{}, err
		}
		for _, agentBytes := range turn[1] {
			agent, err := decode(agentBytes)
			if err != nil {
				return Archive{}, err
			}
			for _, userID := range agent[1] {
				user, err := graph.blob(userID)
				if err != nil {
					return Archive{}, err
				}
				if len(user[5]) == 0 || len(user[5][0]) == 0 || user[5][0][0] == 0 {
					if len(user[18]) != 0 {
						return Archive{}, errors.New("cursor external text blobs are unsupported")
					}
					if err := graph.append(&out, "user", user[1]); err != nil {
						return Archive{}, err
					}
				}
			}
			for _, stepID := range agent[2] {
				step, err := graph.blob(stepID)
				if err != nil {
					return Archive{}, err
				}
				for _, assistantBytes := range step[1] {
					assistant, err := decode(assistantBytes)
					if err != nil {
						return Archive{}, err
					}
					if err := graph.append(&out, "assistant", assistant[1]); err != nil {
						return Archive{}, err
					}
				}
			}
		}
	}
	if len(out.Messages) == 0 {
		return Archive{}, errors.New("cursor conversation has no supported native messages")
	}
	return out, nil
}

// Find locates a complete conversation in any native cwd bucket.
func Find(ctx context.Context, root, id string) (string, bool, error) {
	if _, err := uuid.Parse(id); err != nil {
		return "", false, err
	}
	entries, err := os.ReadDir(filepath.Join(root, "chats"))
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	var found string
	var updated time.Time
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, "chats", entry.Name(), id, "store.db")
		archive, err := Read(ctx, root, path)
		if err == nil && archive.ID == id && (found == "" || archive.Updated.After(updated)) {
			found, updated = path, archive.Updated
		}
	}
	return found, found != "", nil
}

// CopySelectedIn publishes a private native snapshot once, where layout's
// resume looks for it, leaving the source unchanged. It verifies the discovery
// evidence before snapshotting and before publishing the completed copy; empty
// evidence supports old imports.
func CopySelectedIn(ctx context.Context, layout Layout, root, source, launchCWD, expected string) (string, error) {
	return copyIn(ctx, layout, root, source, launchCWD, expected, nil)
}

func copyIn(ctx context.Context, layout Layout, root, source, launchCWD, expected string, publish func(string, string) error) (string, error) {
	root, err := providerRoot(root)
	if err != nil {
		return "", err
	}
	if target, found, err := PreparedCopyIn(ctx, layout, root, source, launchCWD, filepath.Base(filepath.Dir(source)), expected); err != nil || found {
		return target, err
	}
	if err := checkFingerprint(ctx, root, source, expected); err != nil {
		return "", err
	}
	db, tx, actual, err := snapshot(ctx, root, source)
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }()
	defer func() { _ = tx.Rollback() }()
	archive, err := read(ctx, tx, actual)
	if err != nil {
		return "", err
	}
	targetID := layout.SessionID(archive.ID, launchCWD, expected)
	target, err := layout.path(root, launchCWD, archive.ID, expected)
	if err != nil {
		return "", err
	}
	launchCWD, err = filepath.EvalSymlinks(launchCWD)
	if err != nil {
		return "", err
	}
	launchCWD, err = filepath.Abs(launchCWD)
	if err != nil {
		return "", err
	}
	if existing, err := Read(ctx, root, target); err == nil {
		if existing.ID != targetID || existing.CWD != launchCWD {
			return "", errors.New("cursor target identity differs")
		}
		return target, nil
	}
	stage, err := os.MkdirTemp(root, ".cursor-import-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	staged := filepath.Join(stage, "store.db")
	file, err := os.OpenFile(staged, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := copyRows(ctx, tx, staged, targetID); err != nil {
		return "", err
	}
	provider, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer func() { _ = provider.Close() }()
	relSource, err := filepath.Rel(root, actual)
	if err != nil || !filepath.IsLocal(relSource) {
		return "", errors.New("cursor source is outside provider home")
	}
	metaFile, err := provider.Open(filepath.Join(filepath.Dir(relSource), "meta.json"))
	if err != nil {
		return "", err
	}
	var sidecar map[string]json.RawMessage
	err = json.NewDecoder(io.LimitReader(metaFile, 1<<20)).Decode(&sidecar)
	_ = metaFile.Close()
	if err != nil {
		return "", err
	}
	sidecar["cwd"], err = json.Marshal(launchCWD)
	if err != nil {
		return "", err
	}
	metaBytes, err := json.Marshal(sidecar)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(stage, "meta.json"), metaBytes, 0o600); err != nil {
		return "", err
	}
	if expected != "" {
		record, err := preparationFor(root, actual, launchCWD, archive.ID, expected)
		if err != nil {
			return "", err
		}
		body, err := json.Marshal(record)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(stage, preparationFile), body, 0o600); err != nil {
			return "", err
		}
	}
	relTarget, err := filepath.Rel(root, target)
	if err != nil || !filepath.IsLocal(relTarget) {
		return "", errors.New("cursor target is outside provider home")
	}
	if err := provider.MkdirAll(filepath.Dir(filepath.Dir(relTarget)), 0o700); err != nil {
		return "", err
	}
	// The complete directory appears in one no-replace operation. A failure or
	// interruption before publication leaves no reservation blocking a retry.
	if publish != nil {
		if err := publish(stage, filepath.Dir(target)); err != nil {
			return "", err
		}
	}
	if err := checkFingerprint(ctx, root, actual, expected); err != nil {
		return "", err
	}
	if err := publishDirectory(stage, filepath.Dir(target)); err != nil {
		return "", fmt.Errorf("publish cursor target: %w", err)
	}
	return target, nil
}

// copyRows copies the store, recording id as the chat's own agentId.
func copyRows(ctx context.Context, source *sql.Tx, path, id string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, "CREATE TABLE blobs (id TEXT PRIMARY KEY, data BLOB); CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT)"); err != nil {
		return err
	}
	target, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = target.Rollback() }()
	var total int64
	if err := copyTableRows(ctx, source, target, true, id, &total); err != nil {
		return err
	}
	if err := copyTableRows(ctx, source, target, false, id, &total); err != nil {
		return err
	}
	return target.Commit()
}

func copyTableRows(ctx context.Context, source, target *sql.Tx, metadata bool, id string, total *int64) error {
	selectSQL := "SELECT id, CASE WHEN length(data)<=67108864 THEN data END FROM blobs"
	insertSQL := "INSERT INTO blobs VALUES (?,?)"
	if metadata {
		selectSQL = "SELECT key, CASE WHEN length(value)<=67108864 THEN value END FROM meta"
		insertSQL = "INSERT INTO meta VALUES (?,?)"
	}
	rows, err := source.QueryContext(ctx, selectSQL)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key string
		var data []byte
		if err := rows.Scan(&key, &data); err != nil {
			return err
		}
		*total += int64(len(key) + len(data))
		if data == nil || *total > maxStore {
			return errors.New("cursor native snapshot exceeds import limit")
		}
		var value any = data
		if metadata {
			text := string(data)
			if key == "0" {
				var err error
				if text, err = withAgentID(text, id); err != nil {
					return err
				}
			}
			value = text
		}
		if _, err := target.ExecContext(ctx, insertSQL, key, value); err != nil {
			return err
		}
	}
	return rows.Err()
}

// withAgentID rewrites the hex-encoded chat record's agentId and keeps every
// other field Cursor stored.
func withAgentID(encoded, id string) (string, error) {
	body, err := hex.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(body, &record); err != nil {
		return "", err
	}
	var current string
	if err := json.Unmarshal(record["agentId"], &current); err != nil {
		return "", errors.New("cursor native identity is missing")
	}
	if current == id {
		return encoded, nil
	}
	if record["agentId"], err = json.Marshal(id); err != nil {
		return "", err
	}
	body, err = json.Marshal(record)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(body), nil
}
