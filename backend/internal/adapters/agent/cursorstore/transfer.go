package cursorstore

import (
	"archive/tar"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// Pack writes the chat whose store is source, under root, as a tar holding a
// consistent copy of its store.db and its meta.json, and returns the chat.
func Pack(ctx context.Context, root, source string, w io.Writer) (Archive, error) {
	db, tx, actual, err := snapshot(ctx, root, source)
	if err != nil {
		return Archive{}, err
	}
	defer func() { _ = db.Close() }()
	defer func() { _ = tx.Rollback() }()
	archive, err := read(ctx, tx, actual)
	if err != nil {
		return Archive{}, err
	}
	stage, err := os.MkdirTemp("", "ao-cursor-pack-")
	if err != nil {
		return Archive{}, err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	staged := filepath.Join(stage, "store.db")
	if err := copyRows(ctx, tx, staged, archive.ID); err != nil {
		return Archive{}, err
	}
	tw := tar.NewWriter(w)
	for name, path := range map[string]string{"store.db": staged, "meta.json": filepath.Join(filepath.Dir(actual), "meta.json")} {
		data, err := os.ReadFile(path)
		if err != nil {
			return Archive{}, err
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			return Archive{}, err
		}
		if _, err := tw.Write(data); err != nil {
			return Archive{}, err
		}
	}
	return archive, tw.Close()
}

// chatFiles are the files a packed chat may hold.
var chatFiles = map[string]string{"store.db": "store.db", "store.db-wal": "store.db-wal", "meta.json": "meta.json"}

// Unpack places a chat packed by Pack, or by AO Cloud's worker (which may add
// store.db-wal), where the terminal CLI resumes it from cwd, under root, and
// returns its id.
func Unpack(ctx context.Context, root string, r io.Reader, cwd string) (string, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	root, err := providerRoot(root)
	if err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(root, ".cursor-move-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	tr := tar.NewReader(r)
	seen := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read cursor chat: %w", err)
		}
		name, ok := chatFiles[hdr.Name]
		if !ok {
			return "", fmt.Errorf("cursor chat entry %q is not a chat file", hdr.Name)
		}
		if hdr.Typeflag != tar.TypeReg || hdr.Size > maxStore || seen[name] {
			return "", fmt.Errorf("cursor chat entry %q is invalid", hdr.Name)
		}
		seen[name] = true
		f, err := os.OpenFile(filepath.Join(stage, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return "", err
		}
		_, err = io.Copy(f, io.LimitReader(tr, hdr.Size))
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return "", err
		}
	}
	if !seen["store.db"] || !seen["meta.json"] {
		return "", errors.New("cursor chat lacks store.db or meta.json")
	}
	id, err := stagedID(ctx, filepath.Join(stage, "store.db"))
	if err != nil {
		return "", err
	}
	// Cursor names a chat's folder by its id, and the copy checks that.
	source := filepath.Join(stage, id)
	if err := os.Mkdir(source, 0o700); err != nil {
		return "", err
	}
	for _, name := range []string{"store.db", "meta.json"} {
		if err := os.Rename(filepath.Join(stage, name), filepath.Join(source, name)); err != nil {
			return "", err
		}
	}
	if _, err := CopySelectedIn(ctx, LayoutTerminal, root, filepath.Join(source, "store.db"), cwd, ""); err != nil {
		return "", err
	}
	return id, nil
}

// stagedID folds a staged store's WAL into it, since the copy reads a
// read-only snapshot, and returns the chat id its metadata names.
func stagedID(ctx context.Context, path string) (string, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return "", err
	}
	var encoded string
	if err := db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key='0'").Scan(&encoded); err != nil {
		return "", err
	}
	body, err := hex.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	var meta metadata
	if err := json.Unmarshal(body, &meta); err != nil {
		return "", err
	}
	if _, err := uuid.Parse(meta.ID); err != nil || filepath.Base(meta.ID) != meta.ID {
		return "", errors.New("cursor chat has no valid id")
	}
	return meta.ID, nil
}
