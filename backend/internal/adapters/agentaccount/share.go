// Package agentaccount runs Claude Code and Codex under subscription accounts
// that are config folders of the CLIs themselves. Each folder keeps only its
// own login; everything a conversation needs to resume under another account
// is a link to the computer's own folder. Nothing here reads a token.
package agentaccount

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// linkShared makes home/name a symlink to shared/name. A missing target is
// created first (a directory, or an empty file when file is true), so the link
// never dangles; when only the account has the entry, its copy becomes the
// shared one instead, so nothing it holds stays split off. An existing link is
// left alone; so is a real entry, unless it is an empty directory or
// replaceable, which the CLI made before the link did. A failure is
// ErrAgentAccountUnshared: the folder would keep history of its own.
func linkShared(home, shared, name string, file, replaceable bool) (err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("%w: %w", ports.ErrAgentAccountUnshared, err)
		}
	}()
	target := filepath.Join(shared, name)
	link := filepath.Join(home, name)
	if _, err := os.Lstat(target); errors.Is(err, fs.ErrNotExist) {
		if err := createShared(target, link, file); err != nil {
			return fmt.Errorf("create shared %s: %w", name, err)
		}
	} else if err != nil {
		return err
	}
	info, err := os.Lstat(link)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return err
	case info.Mode()&os.ModeSymlink != 0:
		return nil
	case info.IsDir() && emptyDir(link), replaceable:
		if err := os.RemoveAll(link); err != nil {
			return fmt.Errorf("replace %s: %w", name, err)
		}
	default:
		return nil // the account's own copy; leave it rather than lose it
	}
	if err := os.Symlink(target, link); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("link %s: %w", name, err)
	}
	return nil
}

// createShared makes the missing shared target. When only the account has the
// entry, its copy is moved there; otherwise an empty file or directory is made.
func createShared(target, link string, file bool) error {
	if info, err := os.Lstat(link); err == nil && info.Mode()&os.ModeSymlink == 0 && os.Rename(link, target) == nil {
		return nil
	}
	if !file {
		return os.MkdirAll(target, 0o700)
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}

// pruneDangling removes links in home that point into shared at an entry that
// is gone, unless keep names it (linkShared recreates those). The user deleted
// the entry from the computer's own folder; the account should not keep a
// broken link to it.
func pruneDangling(home, shared string, keep map[string]bool) {
	entries, err := os.ReadDir(home)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink == 0 || keep[entry.Name()] {
			continue
		}
		link := filepath.Join(home, entry.Name())
		dest, err := os.Readlink(link)
		if err != nil || filepath.Dir(dest) != filepath.Clean(shared) {
			continue
		}
		if _, err := os.Stat(link); errors.Is(err, fs.ErrNotExist) {
			_ = os.Remove(link)
		}
	}
}

func emptyDir(path string) bool {
	entries, err := os.ReadDir(path)
	return err == nil && len(entries) == 0
}

// writeIfChanged atomically replaces path with data unless it already holds it.
func writeIfChanged(path string, data []byte) error {
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, data) {
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// neutralize returns key="" for each key set in the daemon's environment, so a
// key or token the computer has for its own login cannot override the account.
func neutralize(env map[string]string, keys ...string) {
	for _, key := range keys {
		if _, set := os.LookupEnv(key); set {
			env[key] = ""
		}
	}
}

// ledgerName records, for each JSON file AO shares into an account, every
// shared key's value as AO last wrote it. A key still holding that value
// follows the computer; any other value was changed on the account, and stays.
const ledgerName = ".ao-shared.json"

type ledger struct {
	path  string
	fresh bool // no record yet, so the account holds AO's earlier copy
	files map[string]map[string]json.RawMessage
}

// readLedger loads home's record. A missing or unreadable one starts fresh.
func readLedger(home string) *ledger {
	l := &ledger{path: filepath.Join(home, ledgerName), files: map[string]map[string]json.RawMessage{}}
	data, err := os.ReadFile(l.path)
	if err != nil || json.Unmarshal(data, &l.files) != nil {
		l.fresh, l.files = true, map[string]map[string]json.RawMessage{}
	}
	return l
}

// keys is file's record. A fresh one takes the account's values of the shared
// keys as AO's own, so a folder set up before the record keeps following.
func (l *ledger) keys(file string, account, source map[string]json.RawMessage) map[string]json.RawMessage {
	wrote := l.files[file]
	if wrote == nil {
		wrote = map[string]json.RawMessage{}
		if l.fresh {
			for key := range source {
				if value, ok := account[key]; ok {
					wrote[key] = value
				}
			}
		}
		l.files[file] = wrote
	}
	return wrote
}

func (l *ledger) save() error {
	out, err := json.MarshalIndent(l.files, "", "  ")
	if err != nil {
		return err
	}
	return writeIfChanged(l.path, append(out, '\n'))
}

// shareKeys brings source's keys into account and reports whether account
// changed. A key the account lacks, or still holds as AO last wrote it, takes
// source's value; any other value was changed on the account and stays. A key
// AO wrote that source no longer has is removed, unless the account changed
// it. Each entry of a nested key is shared the same way, on its own.
func shareKeys(account, source, wrote map[string]json.RawMessage, nested map[string]bool) bool {
	keys := map[string]bool{}
	for key := range source {
		keys[key] = true
	}
	for key := range wrote {
		keys[key] = true
	}
	changed := false
	for key := range keys {
		src, inSource := source[key]
		cur, has := account[key]
		if nested[key] {
			a, w := object(cur), object(wrote[key])
			if shareKeys(a, object(src), w, nil) {
				account[key], changed = encode(a), true
			}
			if len(w) == 0 {
				delete(wrote, key)
			} else {
				wrote[key] = encode(w)
			}
			continue
		}
		switch {
		case !inSource:
			if has && same(cur, wrote[key]) {
				delete(account, key)
				changed = true
			}
			delete(wrote, key)
		case !has || same(cur, wrote[key]):
			if !has || !same(cur, src) {
				account[key], changed = src, true
			}
			wrote[key] = src
		case same(cur, src):
			wrote[key] = src // changed back on the account: follows again
		}
	}
	return changed
}

// same compares two JSON values, ignoring formatting and key order.
func same(a, b json.RawMessage) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return bytes.Equal(a, b)
	}
	return reflect.DeepEqual(x, y)
}

func object(raw json.RawMessage) map[string]json.RawMessage {
	m := map[string]json.RawMessage{}
	_ = json.Unmarshal(raw, &m)
	if m == nil {
		m = map[string]json.RawMessage{}
	}
	return m
}

func encode(m map[string]json.RawMessage) json.RawMessage {
	out, _ := json.Marshal(m)
	return out
}

// readObject reads a JSON object file; a missing file is empty.
func readObject(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if m == nil {
		m = map[string]json.RawMessage{}
	}
	return m, nil
}

func writeObject(path string, m map[string]json.RawMessage) error {
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeIfChanged(path, append(out, '\n'))
}
