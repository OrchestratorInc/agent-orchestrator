//go:build linux

package persistenthost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

type namespaceLaunch struct {
	Version  int             `json:"version"`
	Session  string          `json:"session"`
	Identity string          `json:"identity"`
	Phase    string          `json:"phase"`
	Revision uint64          `json:"revision"`
	Owner    *namespaceOwner `json:"owner"`
}

var namespaceCommitBarrierForTest atomic.Pointer[func(string, namespaceLaunch) error]

func namespaceLaunchPath(dataDir, session, identity string) (string, error) {
	if _, err := namespacePermit(identity); err != nil {
		return "", err
	}
	dir, err := hostDir(dataDir, session)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "namespace-launches", identity+".json"), nil
}

func validNamespaceLaunch(record namespaceLaunch) bool {
	if record.Version != 1 {
		return false
	}
	if record.Phase == "prepared" || record.Phase == "cancelled" {
		return record.Owner == nil && (record.Phase == "prepared" && record.Revision == 1 || record.Phase == "cancelled" && record.Revision == 2)
	}
	if record.Owner == nil || !validNamespaceOwner(*record.Owner) {
		return false
	}
	switch record.Phase {
	case "ready":
		return record.Revision == 2
	case "permitted":
		return record.Revision == 3
	case "retiring":
		return record.Revision == 3 || record.Revision == 4
	case "retired":
		return record.Revision == 4 || record.Revision == 5
	default:
		return false
	}
}

func readNamespaceLaunch(dataDir, session, identity string) (namespaceLaunch, error) {
	var record namespaceLaunch
	path, err := namespaceLaunchPath(dataDir, session, identity)
	if err != nil {
		return record, err
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return record, errors.Join(ErrOwnershipInconclusive, err)
	}
	file := os.NewFile(uintptr(fd), path)
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return record, errors.Join(ErrOwnershipInconclusive, err)
	}
	raw, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(raw) > 4096 {
		return record, errors.Join(ErrOwnershipInconclusive, err)
	}
	fields, err := uniqueProviderOwnerFields(raw, []string{"version", "session", "identity", "phase", "revision", "owner"})
	if err != nil || len(fields) != 6 {
		return record, ErrOwnershipInconclusive
	}
	if !bytes.Equal(bytes.TrimSpace(fields["owner"]), []byte("null")) {
		ownerFields, err := uniqueProviderOwnerFields(fields["owner"], []string{"boot", "pid", "start", "namespace", "process"})
		if err != nil || len(ownerFields) != 5 {
			return record, ErrOwnershipInconclusive
		}
	}
	if err := json.Unmarshal(raw, &record); err != nil || record.Session != session || record.Identity != identity || !validNamespaceLaunch(record) {
		return record, errors.Join(ErrOwnershipInconclusive, err)
	}
	return record, nil
}

func writeNamespaceLaunch(dataDir string, record namespaceLaunch) error {
	if !validNamespaceLaunch(record) {
		return ErrOwnershipInconclusive
	}
	path, err := namespaceLaunchPath(dataDir, record.Session, record.Identity)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".namespace-launch-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	err = json.NewEncoder(file).Encode(record)
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	if barrier := namespaceCommitBarrierForTest.Load(); barrier != nil {
		if err := (*barrier)("before-rename", record); err != nil {
			return err
		}
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	if barrier := namespaceCommitBarrierForTest.Load(); barrier != nil {
		if err := (*barrier)("after-rename", record); err != nil {
			return err
		}
	}
	return syncProviderOwnerDirectory(filepath.Dir(path))
}

func withNamespaceLaunchLock(ctx context.Context, path string, work func() error) error {
	fd, err := unix.Open(path+".lock", unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0o600)
	if err != nil {
		return errors.Join(ErrOwnershipInconclusive, err)
	}
	defer func() { _ = unix.Close(fd) }()
	var info unix.Stat_t
	if err := unix.Fstat(fd, &info); err != nil || info.Mode&unix.S_IFMT != unix.S_IFREG {
		return errors.Join(ErrOwnershipInconclusive, err)
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			return err
		}
		if err := waitProviderOwnerObservation(ctx); err != nil {
			return err
		}
	}
	defer func() { _ = unix.Flock(fd, unix.LOCK_UN) }()
	return work()
}

func prepareNamespaceLaunch(ctx context.Context, dataDir, session, identity string) (namespaceLaunch, error) {
	record := namespaceLaunch{Version: 1, Session: session, Identity: identity, Phase: "prepared", Revision: 1}
	path, err := namespaceLaunchPath(dataDir, session, identity)
	if err != nil {
		return record, err
	}
	for _, dir := range []string{filepath.Join(dataDir, "chat-hosts"), filepath.Join(dataDir, "chat-hosts", session), filepath.Dir(path)} {
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return record, err
		}
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() {
			return record, errors.Join(ErrOwnershipInconclusive, err)
		}
		if err := syncProviderOwnerDirectory(filepath.Dir(dir)); err != nil {
			return record, err
		}
	}
	err = withNamespaceLaunchLock(ctx, path, func() error {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return errors.Join(os.ErrExist, err)
		}
		return writeNamespaceLaunch(dataDir, record)
	})
	return record, err
}

func updateNamespaceLaunch(ctx context.Context, dataDir string, expected namespaceLaunch, work func(*namespaceLaunch) error) (namespaceLaunch, error) {
	path, err := namespaceLaunchPath(dataDir, expected.Session, expected.Identity)
	if err != nil {
		return namespaceLaunch{}, err
	}
	var current namespaceLaunch
	err = withNamespaceLaunchLock(ctx, path, func() error {
		current, err = readNamespaceLaunch(dataDir, expected.Session, expected.Identity)
		if err != nil {
			return err
		}
		if current.Revision != expected.Revision || current.Phase != expected.Phase {
			return ErrOwnershipInconclusive
		}
		return work(&current)
	})
	return current, err
}

func publishNamespaceLaunch(ctx context.Context, dataDir string, expected namespaceLaunch, owner namespaceOwner) (namespaceLaunch, error) {
	return updateNamespaceLaunch(ctx, dataDir, expected, func(current *namespaceLaunch) error {
		if current.Phase != "prepared" {
			return ErrOwnershipInconclusive
		}
		fd, stopped, err := openNamespaceOwner(owner)
		if fd >= 0 {
			defer func() { _ = unix.Close(fd) }()
		}
		if err != nil || stopped {
			return errors.Join(ErrOwnershipInconclusive, err)
		}
		current.Owner, current.Phase, current.Revision = &owner, "ready", 2
		return writeNamespaceLaunch(dataDir, *current)
	})
}

func grantNamespaceLaunch(ctx context.Context, dataDir string, expected namespaceLaunch, pipe *os.File) (namespaceLaunch, error) {
	if pipe == nil {
		return namespaceLaunch{}, ErrOwnershipInconclusive
	}
	defer func() { _ = pipe.Close() }()
	return updateNamespaceLaunch(ctx, dataDir, expected, func(current *namespaceLaunch) error {
		if current.Phase != "ready" {
			return ErrOwnershipInconclusive
		}
		fd, stopped, err := openNamespaceOwner(*current.Owner)
		if fd >= 0 {
			defer func() { _ = unix.Close(fd) }()
		}
		if err != nil || stopped {
			return errors.Join(ErrOwnershipInconclusive, err)
		}
		if err := namespaceOwnsPermit(*current.Owner, pipe, fd); err != nil {
			return err
		}
		current.Phase, current.Revision = "permitted", 3
		if err := writeNamespaceLaunch(dataDir, *current); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		deadline := time.Now().Add(5 * time.Second)
		if requested, ok := ctx.Deadline(); ok && requested.Before(deadline) {
			deadline = requested
		}
		if err := pipe.SetWriteDeadline(deadline); err != nil {
			return err
		}
		message, err := namespacePermit(current.Identity)
		if err != nil {
			return err
		}
		if _, err := pipe.WriteString(message); err != nil {
			return err
		}
		return pipe.Close()
	})
}

func retireNamespaceLaunch(ctx context.Context, dataDir string, expected namespaceLaunch, cancelOnly bool) (namespaceLaunch, error) {
	return updateNamespaceLaunch(ctx, dataDir, expected, func(current *namespaceLaunch) error {
		if current.Phase == "cancelled" {
			return nil
		}
		if cancelOnly && current.Phase != "prepared" && current.Phase != "ready" {
			return ErrOwnershipInconclusive
		}
		if current.Phase == "prepared" {
			current.Phase, current.Revision = "cancelled", 2
			return writeNamespaceLaunch(dataDir, *current)
		}
		if current.Phase != "retiring" && current.Phase != "retired" {
			current.Phase = "retiring"
			current.Revision++
			if err := writeNamespaceLaunch(dataDir, *current); err != nil {
				return err
			}
		}
		if err := stopNamespaceOwner(ctx, *current.Owner); err != nil {
			return err
		}
		if current.Phase == "retired" {
			return nil
		}
		current.Phase = "retired"
		current.Revision++
		return writeNamespaceLaunch(dataDir, *current)
	})
}
