package agentauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/deepseekharness"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

// DeepSeek Harness serializes writers of its credential store with an
// exclusive "<file>.lock" holding "<pid>\n" (@deepseek-ai/dsh-atomic-write).
// A lock whose holder has exited is taken over; any other is waited for.
const (
	dshLockWait       = 2 * time.Second
	dshLockRetryFirst = 20 * time.Millisecond
	dshLockRetryMax   = 200 * time.Millisecond
	deepseekKeyRef    = "DEEPSEEK_API_KEY"
)

var dshLockRecord = regexp.MustCompile(`^\d+\n$`)

// saveDeepSeekHarnessKey stores key as refs.DEEPSEEK_API_KEY in DeepSeek
// Harness's own credential store, the record its Models page writes. The
// running Harness reloads the file, and the key applies inside and outside AO.
// Comments and every other entry are kept.
func saveDeepSeekHarnessKey(ctx context.Context, _, key string) error {
	path, err := deepseekharness.CredentialsPath()
	if err != nil || !filepath.IsAbs(path) {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "DeepSeek Harness's credential store is unavailable.")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "DeepSeek Harness's home could not be prepared.")
	}
	unlock, err := lockDeepSeekStore(ctx, path)
	if err != nil {
		return err
	}
	defer unlock()
	var doc yaml.Node
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Size() > 1024*1024 {
			return apierr.Invalid("AGENT_AUTH_STORE_UNSAFE", "DeepSeek Harness's credential file is not a safe regular file.", nil)
		}
		data, err := os.ReadFile(path) //nolint:gosec // the Harness credential store, resolved above
		if err != nil {
			return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "DeepSeek Harness's credential file could not be read.")
		}
		if len(bytes.TrimSpace(data)) > 0 && yaml.Unmarshal(data, &doc) != nil {
			return apierr.Invalid("AGENT_AUTH_STORE_INVALID", "DeepSeek Harness's credential file is invalid; fix it in DeepSeek Harness first.", nil)
		}
	} else if !os.IsNotExist(err) {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "DeepSeek Harness's credential file could not be inspected.")
	}
	if err := setDeepSeekRef(&doc, key); err != nil {
		return err
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return apierr.Internal("AGENT_AUTH_STORE_UNAVAILABLE", "DeepSeek Harness's credentials could not be encoded.")
	}
	_ = encoder.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	return writeDeepSeekStore(path, out.Bytes())
}

// setDeepSeekRef sets refs.DEEPSEEK_API_KEY in a version 1 document, or
// starts one. Harness refuses unversioned layouts and unknown top-level keys,
// so AO only ever touches refs.
func setDeepSeekRef(doc *yaml.Node, key string) error {
	str := func(value string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value} }
	if doc.Kind == 0 {
		doc.Kind = yaml.DocumentNode
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Content: []*yaml.Node{
			str("version"), {Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"},
		}}}
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return apierr.Invalid("AGENT_AUTH_STORE_INVALID", "DeepSeek Harness's credential file has an unexpected shape; fix it in DeepSeek Harness first.", nil)
	}
	root := doc.Content[0]
	if version := mappingValue(root, "version"); version == nil || version.Value != "1" {
		return apierr.Invalid("AGENT_AUTH_STORE_INVALID", "Open DeepSeek Harness once so it upgrades its credential file, then save the key again.", nil)
	}
	refs := mappingValue(root, "refs")
	if refs == nil {
		refs = &yaml.Node{Kind: yaml.MappingNode}
		root.Content = append(root.Content, str("refs"), refs)
	}
	if refs.Kind == yaml.ScalarNode && refs.Tag == "!!null" {
		refs.Kind, refs.Tag, refs.Value = yaml.MappingNode, "", ""
	}
	if refs.Kind != yaml.MappingNode {
		return apierr.Invalid("AGENT_AUTH_STORE_INVALID", "DeepSeek Harness's saved keys have an unexpected shape; fix them in DeepSeek Harness first.", nil)
	}
	if value := mappingValue(refs, deepseekKeyRef); value != nil {
		comment := value.LineComment
		*value = *str(key)
		value.LineComment = comment
		return nil
	}
	refs.Content = append(refs.Content, str(deepseekKeyRef), str(key))
	return nil
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func lockDeepSeekStore(ctx context.Context, path string) (func(), error) {
	lock := path + ".lock"
	deadline := time.Now().Add(dshLockWait)
	delay := dshLockRetryFirst
	retriedPermission := false
	for {
		file, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // the Harness lock beside its store
		if err == nil {
			_, writeErr := file.WriteString(strconv.Itoa(os.Getpid()) + "\n")
			closeErr := file.Close()
			if writeErr != nil || closeErr != nil {
				_ = os.Remove(lock)
				return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "DeepSeek Harness's credential file could not be locked.")
			}
			return func() { _ = os.Remove(lock) }, nil
		}
		switch {
		case dshLockContention(err, lock):
			// Continue at once only when the exited holder's lock is gone.
			if dshTakeOverExitedLock(lock) {
				continue
			}
		case runtime.GOOS == "windows" && errors.Is(err, os.ErrPermission) && !retriedPermission:
			// A lock just removed can stay "delete pending" for a moment.
			retriedPermission = true
		default:
			return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "DeepSeek Harness's credential file could not be locked.")
		}
		if time.Now().After(deadline) {
			return nil, apierr.Conflict("AGENT_AUTH_STORE_BUSY", "DeepSeek Harness is saving its credentials. Try again in a moment.", nil)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		delay = min(delay*2, dshLockRetryMax)
	}
}

// dshLockContention reports that err means another writer holds lock, as
// Harness's isLockContention: EEXIST, or EPERM while the lock exists.
func dshLockContention(err error, lock string) bool {
	if errors.Is(err, os.ErrExist) {
		return true
	}
	if errors.Is(err, os.ErrPermission) {
		_, statErr := os.Lstat(lock)
		return statErr == nil
	}
	return false
}

// dshHolderExited reports that record names a holder proven gone, as
// Harness's holderExited: a well-formed record for a PID other than 0 and this
// process, and signal 0 reporting no such process. A partial record, a live
// or zombie process, or one owned by another user keeps the lock.
func dshHolderExited(record []byte) bool {
	if !dshLockRecord.Match(record) {
		return false
	}
	pid, err := strconv.Atoi(string(bytes.TrimSpace(record)))
	if err != nil || pid == 0 || pid > 2147483647 || pid == os.Getpid() {
		return false
	}
	return processalive.Exited(pid)
}

// dshTakeOverExitedLock removes lock when its holder has exited, as Harness's
// takeOverExitedLock: contenders that read the same record race for one
// exclusive "<lock>.takeover-<sha256(record)[:16]>" claim, and the winner
// removes the lock only if it still holds that record and the holder is
// still gone. It reports whether the lock was removed.
func dshTakeOverExitedLock(lock string) bool {
	record, err := os.ReadFile(lock) //nolint:gosec // the Harness lock beside its store
	if err != nil || !dshHolderExited(record) {
		return false
	}
	sum := sha256.Sum256(record)
	claim := lock + ".takeover-" + hex.EncodeToString(sum[:])[:16]
	file, err := os.OpenFile(claim, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // the takeover claim beside the Harness lock
	if err != nil {
		return false
	}
	_, _ = file.WriteString(strconv.Itoa(os.Getpid()) + "\n")
	_ = file.Close()
	defer func() { _ = os.Remove(claim) }()
	current, err := os.ReadFile(lock) //nolint:gosec // the Harness lock beside its store
	if err != nil || !bytes.Equal(current, record) || !dshHolderExited(record) {
		return false
	}
	if err := os.Remove(lock); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false
	}
	return true
}

// dshRename is os.Rename with Harness's Windows retry: a file another process
// has open (a scanner, an editor) can refuse the rename for a moment.
func dshRename(from, to string) error {
	delay := dshLockRetryFirst
	for attempt := 0; ; attempt++ {
		err := os.Rename(from, to)
		if err == nil || runtime.GOOS != "windows" || attempt == 8 || !dshTransientWindowsError(err) {
			return err
		}
		time.Sleep(delay)
		delay = min(delay*2, dshLockRetryMax)
	}
}

// writeDeepSeekStore commits through a "<file>.<hex>.tmp" sibling at 0600 and
// a rename, as Harness does; Harness refuses a store others can read.
func writeDeepSeekStore(path string, data []byte) error {
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return apierr.Internal("AGENT_AUTH_STORE_UNAVAILABLE", "DeepSeek Harness's credential file could not be prepared.")
	}
	tmp := path + "." + hex.EncodeToString(suffix) + ".tmp"
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // temp sibling of the Harness store
	if err != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "DeepSeek Harness's credential file could not be prepared.")
	}
	defer func() { _ = os.Remove(tmp) }()
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if closeErr := file.Close(); writeErr != nil || closeErr != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "DeepSeek Harness's credential file could not be written.")
	}
	if err := dshRename(tmp, path); err != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "DeepSeek Harness's credential file could not be replaced.")
	}
	return nil
}

// dshTransientWindowsError matches the rename failures Harness retries on
// Windows: EPERM and EACCES (access denied) and EBUSY (sharing or lock
// violation).
func dshTransientWindowsError(err error) bool {
	if errors.Is(err, os.ErrPermission) {
		return true
	}
	var errno syscall.Errno
	return errors.As(err, &errno) && (errno == 32 || errno == 33) // ERROR_SHARING_VIOLATION, ERROR_LOCK_VIOLATION
}
