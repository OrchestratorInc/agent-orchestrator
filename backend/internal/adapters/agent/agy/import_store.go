package agy

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
	"modernc.org/sqlite/vfs"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const maxImportDB = 256 << 20
const maxImportText = 32 << 20

var importID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var errImportSourceChanged = errors.New("agy: source changed while reading; scan again")

// ImportArchive is the visible projection of Agy 1.5.13's schema-1 native DB.
// Preparation copies the selected database, including tool history, separately.
type ImportArchive struct {
	ID, CWD, Fingerprint string
	Messages             []domain.ImportedMessage
	Shortened            bool
}

func importPath(root, path string) (string, string, error) {
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", err
	}
	base, err = filepath.Abs(base)
	if err != nil {
		return "", "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", "", err
	}
	if !info.Mode().IsRegular() {
		return "", "", errors.New("agy: native database must be a regular file")
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", "", err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(base, path)
	if err != nil || !filepath.IsLocal(rel) {
		return "", "", errors.New("agy: conversation outside native root")
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 3 || (parts[0] != CLIStorageDir && parts[0] != ACPStorageDir) || parts[1] != "conversations" || !strings.HasSuffix(parts[2], ".db") || !importID.MatchString(strings.TrimSuffix(parts[2], ".db")) {
		return "", "", errors.New("agy: invalid native conversation path")
	}
	r, err := os.OpenRoot(base)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = r.Close() }()
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		info, err := r.Lstat(rel + suffix)
		if errors.Is(err, os.ErrNotExist) && suffix != "" {
			continue
		}
		if err != nil {
			return "", "", err
		}
		if !info.Mode().IsRegular() || info.Size() > maxImportDB {
			return "", "", errors.New("agy: native input is not a bounded regular file")
		}
	}
	return base, rel, nil
}

// ImportFingerprint reads only closed databases. immutable=1 never creates a
// source WAL/SHM, but ignores a WAL, so a nonempty WAL or hot journal must fail.
func ImportFingerprint(ctx context.Context, root, path string) (string, error) {
	root, rel, err := importPath(root, path)
	if err != nil {
		return "", err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer func() { _ = r.Close() }()
	for _, suffix := range []string{"-wal", "-journal"} {
		info, err := r.Stat(rel + suffix)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if err == nil && info.Size() > 0 {
			return "", errors.New("agy: close the source conversation before importing (native journal is active)")
		}
	}
	f, err := r.Open(rel)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	buf := make([]byte, 64<<10)
	total := int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(buf)
		total += int64(n)
		if total > maxImportDB {
			return "", errors.New("agy: native database exceeds import budget")
		}
		_, _ = h.Write(buf[:n])
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ReadImportArchive never starts Agy, writes native state, or reads auth/config.
func ReadImportArchive(ctx context.Context, root, path string) (ImportArchive, error) {
	fingerprint, err := ImportFingerprint(ctx, root, path)
	if err != nil {
		return ImportArchive{}, err
	}
	a, err := readImportArchive(ctx, root, path, true)
	if err != nil {
		return a, err
	}
	after, err := ImportFingerprint(ctx, root, path)
	if err != nil {
		return a, err
	}
	if after != fingerprint {
		return a, errImportSourceChanged
	}
	a.Fingerprint = fingerprint
	return a, nil
}

func readImportArchive(ctx context.Context, root, path string, immutable bool) (ImportArchive, error) {
	a := ImportArchive{}
	root, rel, err := importPath(root, path)
	if err != nil {
		return a, err
	}
	a.ID = strings.TrimSuffix(filepath.Base(rel), ".db")
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(root, rel))}
	dsn := u.String() + "?mode=ro&_pragma=busy_timeout(2000)"
	if immutable {
		provider, err := os.OpenRoot(root)
		if err != nil {
			return a, err
		}
		defer func() { _ = provider.Close() }()
		name, filesystem, err := vfs.New(provider.FS())
		if err != nil {
			return a, err
		}
		defer func() { _ = filesystem.Close() }()
		dsn = "file:" + filepath.ToSlash(rel) + "?mode=ro&immutable=1&vfs=" + name
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return a, err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return a, err
	}
	if version != 1 {
		return a, errors.New("agy: unsupported native schema version")
	}
	// Native tables and the two native indexes are the audited schema. In
	// particular, a trigger must never run when the PRIVATE metadata is remapped.
	var totalObjects, knownTables int
	if err := db.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(type='table' AND name IN ('trajectory_meta','steps','gen_metadata','executor_metadata','parent_references','trajectory_metadata_blob','battle_mode_infos')),0) FROM sqlite_master WHERE name NOT GLOB 'sqlite_*' AND NOT(type='index' AND name IN ('idx_steps_status','idx_steps_step_type'))`).Scan(&totalObjects, &knownTables); err != nil {
		return a, err
	}
	if totalObjects != 7 || knownTables != 7 {
		return a, errors.New("agy: unsupported or incomplete native table layout")
	}
	var count int
	var id string
	var kind int
	if err := db.QueryRowContext(ctx, "SELECT count(*),coalesce(min(cascade_id),''),coalesce(min(trajectory_type),0) FROM trajectory_meta").Scan(&count, &id, &kind); err != nil {
		return a, err
	}
	if count != 1 || id != a.ID || kind != 4 {
		return a, errors.New("agy: native conversation identity/type mismatch")
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM parent_references").Scan(&count); err != nil {
		return a, err
	}
	if count != 0 {
		return a, errors.New("agy: externally linked native trajectories are unsupported")
	}
	var meta []byte
	if err := db.QueryRowContext(ctx, "SELECT data FROM trajectory_metadata_blob WHERE id='main' AND length(data)<=8388608").Scan(&meta); err != nil {
		return a, err
	}
	if a.CWD, err = importMetadataCWD(meta); err != nil {
		return a, err
	}
	rows, err := db.QueryContext(ctx, "SELECT step_type,step_format,has_subtrajectory,coalesce(metadata,x''),CASE WHEN length(step_payload)<=67108864 THEN step_payload END FROM steps ORDER BY idx")
	if err != nil {
		return a, err
	}
	defer func() { _ = rows.Close() }()
	user, held, drop := false, 0, 1
	var fallback time.Time
	if info, err := os.Stat(filepath.Join(root, rel)); err == nil {
		fallback = info.ModTime()
	}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return a, err
		}
		var typ, format, child int
		var metadata, payload []byte
		if err := rows.Scan(&typ, &format, &child, &metadata, &payload); err != nil {
			return a, err
		}
		if format != 0 || child != 0 || payload == nil {
			return a, errors.New("agy: unsupported native step format, subtrajectory or oversized payload")
		}
		var field protowire.Number
		role := domain.MessageRoleAssistant
		switch typ {
		case 14:
			field = 19
			role = domain.MessageRoleUser
		case 15:
			field = 20
		default:
			continue
		}
		text, err := importStepText(payload, field)
		if err != nil {
			return a, err
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		if role == domain.MessageRoleAssistant && !user {
			continue
		}
		if len(text) > maxImportText {
			return a, errors.New("agy: visible message exceeds import budget")
		}
		at := importStepTime(metadata)
		if at.IsZero() {
			at = fallback
		}
		a.Messages = append(a.Messages, domain.ImportedMessage{Role: role, Text: text, At: at})
		held += len(text)
		user = user || role == domain.MessageRoleUser
		for held > maxImportText && drop < len(a.Messages) {
			held -= len(a.Messages[drop].Text)
			a.Messages[drop].Text = ""
			drop++
			a.Shortened = true
		}
	}
	if err := rows.Err(); err != nil {
		return a, err
	}
	if !user {
		return a, errors.New("agy: no native user history")
	}
	kept := a.Messages[:0]
	for _, m := range a.Messages {
		if m.Text != "" {
			kept = append(kept, m)
		}
	}
	a.Messages = kept
	return a, nil
}

type importWireField struct {
	number    protowire.Number
	kind      protowire.Type
	data, raw []byte
}

func importFields(data []byte) ([]importWireField, error) {
	var out []importWireField
	for len(data) > 0 {
		if len(out) >= 65536 {
			return nil, errors.New("agy: native protobuf exceeds field budget")
		}
		num, kind, n := protowire.ConsumeTag(data)
		if n < 0 || num < 1 {
			return nil, errors.New("agy: malformed native protobuf")
		}
		m := protowire.ConsumeFieldValue(num, kind, data[n:])
		if m < 0 {
			return nil, errors.New("agy: malformed native protobuf field")
		}
		f := importWireField{number: num, kind: kind, raw: data[:n+m]}
		if kind == protowire.BytesType {
			f.data, _ = protowire.ConsumeBytes(data[n:])
		}
		out = append(out, f)
		data = data[n+m:]
	}
	return out, nil
}

func importMetadataCWD(data []byte) (string, error) {
	fields, err := importFields(data)
	if err != nil {
		return "", err
	}
	workspace, uri := "", ""
	workspaces := 0
	for _, f := range fields {
		if f.number == 5 && len(f.data) > 0 {
			return "", errors.New("agy: subagent conversations are unsupported")
		}
		if f.number == 1 {
			workspaces++
			child, err := importFields(f.data)
			if err != nil {
				return "", err
			}
			for _, c := range child {
				if c.number == 1 {
					workspace = string(c.data)
				}
			}
		}
		if f.number == 7 {
			if uri != "" {
				return "", errors.New("agy: multi-root conversations are unsupported")
			}
			uri = string(f.data)
		}
	}
	if workspaces != 1 || workspace == "" || uri != workspace {
		return "", errors.New("agy: native workspace metadata is incomplete")
	}
	u, err := url.Parse(workspace)
	if err != nil || u.Scheme != "file" || u.Host != "" {
		return "", errors.New("agy: unsupported workspace URI")
	}
	cwd := filepath.FromSlash(u.Path)
	if !filepath.IsAbs(cwd) && len(cwd) > 1 && filepath.VolumeName(cwd[1:]) != "" {
		cwd = cwd[1:]
	}
	if !filepath.IsAbs(cwd) {
		return "", errors.New("agy: native workspace must be absolute")
	}
	return filepath.Clean(cwd), nil
}

func importWorkspaceURI(cwd string) string {
	path := filepath.ToSlash(cwd)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func importStepText(payload []byte, number protowire.Number) (string, error) {
	fields, err := importFields(payload)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	var query, response string
	for _, f := range fields {
		if f.number != number {
			continue
		}
		body, err := importFields(f.data)
		if err != nil {
			return "", err
		}
		for _, b := range body {
			if (b.number == 1 || (number == 19 && b.number == 2)) && b.kind == protowire.BytesType {
				if !utf8.Valid(b.data) {
					return "", errors.New("agy: invalid message text")
				}
				if number == 19 {
					if b.number == 1 {
						query = string(b.data)
					} else {
						response = string(b.data)
					}
				} else {
					text.Write(b.data)
				}
			}
			if number == 19 && b.number == 3 {
				items, err := importFields(b.data)
				if err != nil {
					return "", err
				}
				for _, item := range items {
					if item.number == 1 && item.kind == protowire.BytesType {
						if !utf8.Valid(item.data) {
							return "", errors.New("agy: invalid user text")
						}
						if text.Len() > 0 {
							text.WriteByte('\n')
						}
						text.Write(item.data)
					}
				}
			}
		}
	}
	if number == 19 && text.Len() == 0 {
		if response != "" {
			return response, nil
		}
		return query, nil
	}
	return text.String(), nil
}

func importStepTime(data []byte) time.Time {
	fields, err := importFields(data)
	if err != nil {
		return time.Time{}
	}
	for _, f := range fields {
		if f.number != 1 || f.kind != protowire.BytesType {
			continue
		}
		parts, err := importFields(f.data)
		if err != nil {
			return time.Time{}
		}
		var sec, nano uint64
		for _, p := range parts {
			_, _, n := protowire.ConsumeTag(p.raw)
			v, _ := protowire.ConsumeVarint(p.raw[n:])
			if p.number == 1 {
				sec = v
			}
			if p.number == 2 {
				nano = v
			}
		}
		if sec > 0 && nano < 1e9 {
			return time.Unix(int64(sec), int64(nano))
		}
	}
	return time.Time{}
}

func rewriteImportWorkspace(data []byte, cwd string) ([]byte, error) {
	if _, err := importMetadataCWD(data); err != nil {
		return nil, err
	}
	fields, err := importFields(data)
	if err != nil {
		return nil, err
	}
	var out []byte
	uri := []byte(importWorkspaceURI(cwd))
	for _, f := range fields {
		if f.number == 7 {
			out = append(out, protowire.AppendBytes(protowire.AppendTag(nil, 7, protowire.BytesType), uri)...)
			continue
		}
		if f.number == 1 {
			children, err := importFields(f.data)
			if err != nil {
				return nil, err
			}
			var workspace []byte
			for _, c := range children {
				if c.number == 1 || c.number == 2 {
					workspace = protowire.AppendBytes(protowire.AppendTag(workspace, c.number, protowire.BytesType), uri)
				} else {
					workspace = append(workspace, c.raw...)
				}
			}
			out = protowire.AppendBytes(protowire.AppendTag(out, 1, protowire.BytesType), workspace)
			continue
		}
		out = append(out, f.raw...)
	}
	return out, nil
}

func importUnavailable(err error) error {
	return fmt.Errorf("agy: imported native history unavailable: %w", err)
}
