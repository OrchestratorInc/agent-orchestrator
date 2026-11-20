package devin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // Native store snapshots use the existing SQLite driver.
)

const maxImportStoreBytes = 512 << 20

// importStore never opens the original database through SQLite. Even mode=ro
// may create/update shared-memory sidecars; only confined byte reads touch the
// original. SQLite sees a committed, in-memory snapshot instead.
type importStore struct {
	db          *sql.DB
	fingerprint string
}

func (s *importStore) Close() { _ = s.db.Close() }

type importContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r importContextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}

func sourceStoreFile(ctx context.Context, root *os.Root, name string, optional bool, digest hash.Hash, retain bool, remaining *int64) ([]byte, error) {
	info, err := root.Lstat(name)
	if optional && errors.Is(err, os.ErrNotExist) {
		_, _ = digest.Write([]byte("absent:" + name))
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > *remaining {
		return nil, errors.New("devin: native store is not a bounded regular file")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		return nil, errors.New("devin: native store changed while opening")
	}
	_, _ = digest.Write([]byte("present:" + name))
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(opened.Size()))
	_, _ = digest.Write(size[:])
	r := io.TeeReader(io.LimitReader(importContextReader{ctx, f}, *remaining+1), digest)
	var data []byte
	var n int64
	if retain {
		data, err = io.ReadAll(r)
		n = int64(len(data))
	} else {
		n, err = io.Copy(io.Discard, r)
	}
	if err != nil {
		return nil, err
	}
	if n != opened.Size() || n > *remaining {
		return nil, errors.New("devin: native store changed or exceeded import budget")
	}
	*remaining -= n
	return data, nil
}

func readSourceStore(ctx context.Context, root string) (*importStore, error) {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	info, err := r.Lstat("cli")
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("devin: native store directory is unavailable")
	}
	if journal, err := r.Lstat("cli/sessions.db-journal"); err == nil && journal.Size() != 0 {
		return nil, errors.New("devin: native rollback journal requires recovery outside import")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	digest := sha256.New()
	remaining := int64(maxImportStoreBytes)
	main, err := sourceStoreFile(ctx, r, "cli/sessions.db", false, digest, true, &remaining)
	if err != nil {
		return nil, err
	}
	wal, err := sourceStoreFile(ctx, r, "cli/sessions.db-wal", true, digest, true, &remaining)
	if err != nil {
		return nil, err
	}
	fingerprint := digest.Sum(nil)
	digest.Reset()
	remaining = maxImportStoreBytes
	for _, name := range []string{"cli/sessions.db", "cli/sessions.db-wal"} {
		if _, err := sourceStoreFile(ctx, r, name, name != "cli/sessions.db", digest, false, &remaining); err != nil {
			return nil, err
		}
	}
	if !bytes.Equal(fingerprint, digest.Sum(nil)) {
		return nil, errors.New("devin: native store changed; scan again")
	}
	if journal, err := r.Lstat("cli/sessions.db-journal"); err == nil && journal.Size() != 0 {
		return nil, errors.New("devin: source transaction started during import")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	merged, err := committedImportPages(ctx, main, wal)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	conn, err := db.Conn(ctx)
	if err == nil {
		err = conn.Raw(func(raw any) error {
			deserializer, ok := raw.(interface{ Deserialize([]byte) error })
			if !ok {
				return errors.New("devin: SQLite snapshot reader is unavailable")
			}
			return deserializer.Deserialize(merged)
		})
		_ = conn.Close()
	}
	if err == nil {
		_, err = db.ExecContext(ctx, "PRAGMA query_only=ON; PRAGMA trusted_schema=OFF")
	}
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &importStore{db: db, fingerprint: hex.EncodeToString(fingerprint)}, nil
}

// committedImportPages implements SQLite's documented WAL reader/checksums:
// https://sqlite.org/fileformat2.html#wal_file_format . Incomplete/uncommitted
// tail frames are ignored, as in SQLite, and only the last valid commit wins.
// Changing journal markers here affects only the ephemeral deserialized image.
func committedImportPages(ctx context.Context, main, wal []byte) ([]byte, error) {
	if len(main) < 100 || string(main[:16]) != "SQLite format 3\x00" {
		return nil, errors.New("devin: invalid native SQLite header")
	}
	pageSize := int(binary.BigEndian.Uint16(main[16:18]))
	if pageSize == 1 {
		pageSize = 65536
	}
	if pageSize < 512 || pageSize > 65536 || pageSize&(pageSize-1) != 0 || len(main)%pageSize != 0 || main[18] > 2 || main[19] > 2 {
		return nil, errors.New("devin: unsupported native SQLite format")
	}
	if len(wal) != 0 {
		if len(wal) < 32 || binary.BigEndian.Uint32(wal[4:8]) != 3007000 || int(binary.BigEndian.Uint32(wal[8:12])) != pageSize {
			return nil, errors.New("devin: invalid native WAL header")
		}
		magic := binary.BigEndian.Uint32(wal[:4])
		var order binary.ByteOrder = binary.LittleEndian
		if magic == 0x377f0683 {
			order = binary.BigEndian
		} else if magic != 0x377f0682 {
			return nil, errors.New("devin: unsupported native WAL format")
		}
		s0, s1 := importWALChecksum(wal[:24], order, 0, 0)
		if s0 != binary.BigEndian.Uint32(wal[24:28]) || s1 != binary.BigEndian.Uint32(wal[28:32]) {
			return nil, errors.New("devin: corrupt native WAL header")
		}
		end, pages := 32, 0
		for offset := 32; offset+24+pageSize <= len(wal); offset += 24 + pageSize {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			frame := wal[offset : offset+24+pageSize]
			if !bytes.Equal(frame[8:16], wal[16:24]) {
				break // stale frames from a previous WAL generation
			}
			s0, s1 = importWALChecksum(frame[:8], order, s0, s1)
			s0, s1 = importWALChecksum(frame[24:], order, s0, s1)
			if s0 != binary.BigEndian.Uint32(frame[16:20]) || s1 != binary.BigEndian.Uint32(frame[20:24]) {
				break
			}
			page := uint64(binary.BigEndian.Uint32(frame[:4]))
			count := uint64(binary.BigEndian.Uint32(frame[4:8]))
			if page < 1 || page > uint64(maxImportStoreBytes/pageSize) || count > uint64(maxImportStoreBytes/pageSize) {
				return nil, errors.New("devin: native WAL exceeds import budget")
			}
			if count != 0 {
				end, pages = offset+24+pageSize, int(count)
			}
		}
		if pages != 0 {
			image := make([]byte, pages*pageSize)
			copy(image, main)
			for offset := 32; offset < end; offset += 24 + pageSize {
				page := int(binary.BigEndian.Uint32(wal[offset : offset+4]))
				if page <= pages {
					copy(image[(page-1)*pageSize:page*pageSize], wal[offset+24:offset+24+pageSize])
				}
			}
			main = image
		}
	}
	main[18], main[19] = 1, 1
	return main, ctx.Err()
}

func importWALChecksum(b []byte, order binary.ByteOrder, s0, s1 uint32) (uint32, uint32) {
	for len(b) >= 8 {
		s0 += order.Uint32(b[:4]) + s1
		s1 += order.Uint32(b[4:8]) + s0
		b = b[8:]
	}
	return s0, s1
}
