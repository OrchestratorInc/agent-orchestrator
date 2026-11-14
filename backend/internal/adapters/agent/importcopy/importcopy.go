// Package importcopy copies an imported provider session to the place the
// provider will look for it from AO's workspace. It serves adapters whose
// provider finds a session by the directory it runs in (see
// ports.AgentImportedSessionRelocator).
package importcopy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// RewriteLine returns the line to write in place of one JSON Lines record.
// The line has no trailing newline. Blank lines are copied without a call.
type RewriteLine func(line []byte) ([]byte, error)

// Check runs after the last record and before the copy is put in place. An
// adapter uses it to require what its provider needs, such as exactly one
// session record having been rewritten. An error leaves nothing at dst.
type Check func() error

// CopyOnce copies the JSON Lines file src to dst, passing each record through
// rewrite, unless dst already exists. check may be nil. dst's directory is
// created (0700) when missing.
//
// The source is only read. dst appears whole or not at all: the copy is
// written to a private temporary file in dst's directory and then linked into
// place, which also refuses to replace a dst created concurrently. An existing
// dst is kept as it is, because after the first resume it holds the provider's
// later turns.
func CopyOnce(src, dst string, rewrite RewriteLine, check Check) error {
	if _, err := os.Lstat(dst); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".importcopy-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := copyLines(in, tmp, rewrite); err != nil {
		_ = tmp.Close()
		return err
	}
	if check != nil {
		if err := check(); err != nil {
			_ = tmp.Close()
			return err
		}
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Link(tmpName, dst); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return nil
}

// WriteOnce writes body to name inside root, which never follows a link out
// of it. As with CopyOnce, name appears whole or not at all: body is written
// to a private temporary file beside it and then linked into place, so an
// existing name is never replaced; that case reports os.ErrExist.
func WriteOnce(ctx context.Context, root *os.Root, name string, body []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(name), ".importcopy-"+rand.Text())
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(tmp) }()
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return root.Link(tmp, name)
}

func copyLines(in io.Reader, out io.Writer, rewrite RewriteLine) error {
	reader := bufio.NewReader(in)
	writer := bufio.NewWriter(out)
	for n := 1; ; n++ {
		line, readErr := reader.ReadBytes('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		if len(line) == 0 {
			break
		}
		body, newline := bytes.CutSuffix(line, []byte("\n"))
		if len(bytes.TrimSpace(body)) > 0 {
			rewritten, err := rewrite(body)
			if err != nil {
				return fmt.Errorf("line %d: %w", n, err)
			}
			body = rewritten
		}
		if _, err := writer.Write(body); err != nil {
			return err
		}
		if newline {
			if err := writer.WriteByte('\n'); err != nil {
				return err
			}
		}
		if readErr != nil {
			break
		}
	}
	return writer.Flush()
}

// StringField returns a top-level string field of one JSON record.
func StringField(line []byte, field string) (string, bool) {
	var record map[string]json.RawMessage
	if json.Unmarshal(line, &record) != nil {
		return "", false
	}
	var value string
	if raw, ok := record[field]; !ok || json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

// SetStringField returns the JSON record with a top-level string field set to
// value. Every other field keeps its exact value. A record without the field
// gains it, so callers decide which records to change.
func SetStringField(line []byte, field, value string) ([]byte, error) {
	var record map[string]json.RawMessage
	if err := json.Unmarshal(line, &record); err != nil {
		return nil, err
	}
	if record == nil {
		return nil, errors.New("record is not a JSON object")
	}
	encoded, err := marshal(value)
	if err != nil {
		return nil, err
	}
	record[field] = encoded
	return marshal(record)
}

// marshal encodes without HTML escaping, so untouched text keeps its bytes.
func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
