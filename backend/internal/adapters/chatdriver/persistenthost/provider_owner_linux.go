//go:build linux

package persistenthost

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

const providerOwnerProofSupported = true

var providerCensusBarrierForTest atomic.Pointer[func(providerOwner)]

type providerProc struct {
	providerProcessIdentity
	group   int
	session int
	state   string
}

func readProviderProc(pid int) (providerProc, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return providerProc{}, err
	}
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return providerProc{}, ErrOwnershipInconclusive
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 20 {
		return providerProc{}, ErrOwnershipInconclusive
	}
	group, groupErr := strconv.Atoi(fields[2])
	session, sessionErr := strconv.Atoi(fields[3])
	start, startErr := strconv.ParseUint(fields[19], 10, 64)
	if groupErr != nil || sessionErr != nil || startErr != nil {
		return providerProc{}, ErrOwnershipInconclusive
	}
	return providerProc{providerProcessIdentity: providerProcessIdentity{PID: pid, Start: start}, group: group, session: session, state: fields[0]}, nil
}

func providerBootID() (string, error) {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil || strings.TrimSpace(string(data)) == "" {
		return "", errors.Join(ErrOwnershipInconclusive, err)
	}
	return strings.TrimSpace(string(data)), nil
}

func captureProviderOwner(owner *providerOwner, pid int) error {
	process, err := readProviderProc(pid)
	if err != nil || process.group != pid || process.Start == 0 {
		return errors.Join(ErrOwnershipInconclusive, err)
	}
	boot, err := providerBootID()
	if err != nil {
		return err
	}
	owner.Boot, owner.Group, owner.SessionID = boot, process.group, process.session
	owner.Members = []providerProcessIdentity{process.providerProcessIdentity}
	owner.State = "active"
	return nil
}

func providerGroupMembers(owner providerOwner) ([]providerProc, error) {
	if owner.Boot == "" || owner.Group <= 0 || owner.SessionID <= 0 || len(owner.Members) == 0 {
		return nil, ErrOwnershipInconclusive
	}
	boot, err := providerBootID()
	if err != nil {
		return nil, err
	}
	if boot != owner.Boot {
		return nil, nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	if barrier := providerCensusBarrierForTest.Load(); barrier != nil {
		(*barrier)(owner)
	}
	var members []providerProc
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || !entry.IsDir() {
			continue
		}
		process, err := readProviderProc(pid)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, errors.Join(ErrOwnershipInconclusive, err)
		}
		if process.state == "Z" || process.state == "X" {
			continue
		}
		for _, known := range owner.Members {
			if known.PID <= 0 || known.Start == 0 {
				return nil, ErrOwnershipInconclusive
			}
			if process.providerProcessIdentity == known && (process.group != owner.Group || process.session != owner.SessionID) {
				return nil, ErrOwnershipInconclusive
			}
		}
		if process.group == owner.Group {
			if process.session != owner.SessionID {
				return nil, ErrOwnershipInconclusive
			}
			members = append(members, process)
		}
	}
	return members, nil
}

func providerGroupStopped(owner providerOwner) (bool, error) {
	members, err := providerGroupMembers(owner)
	if err != nil || len(members) != 0 {
		return false, err
	}
	return providerGroupAbsent(owner)
}

func providerGroupAbsent(owner providerOwner) (bool, error) {
	boot, err := providerBootID()
	if err != nil {
		return false, err
	}
	if boot != owner.Boot {
		return true, nil
	}
	// An unobserved descendant can leave the group before its parent exits.
	// Group absence cannot prove retirement of this uncontained launch.
	return false, errors.Join(ErrOwnershipInconclusive, errProviderContainmentRequired)
}

func stopProviderOwner(ctx context.Context, dataDir string, owner providerOwner) error {
	for {
		if err := ctx.Err(); err != nil {
			return errors.Join(ErrOwnershipInconclusive, err)
		}
		members, err := providerGroupMembers(owner)
		if err != nil {
			return err
		}
		if len(members) == 0 {
			absent, err := providerGroupAbsent(owner)
			if err != nil {
				return err
			}
			if absent {
				owner.State = "stopped"
				return writeProviderOwner(dataDir, owner)
			}
			if err := waitProviderOwnerObservation(ctx); err != nil {
				return err
			}
			continue
		}
		anchored := false
		for _, member := range members {
			for _, known := range owner.Members {
				anchored = anchored || member.providerProcessIdentity == known
			}
		}
		if !anchored {
			return ErrOwnershipInconclusive
		}
		// Persist proven members before signaling, so cold retry still identifies
		// descendants after the leader exits or the deleting daemon crashes.
		for _, member := range members {
			known := false
			for _, previous := range owner.Members {
				known = known || member.providerProcessIdentity == previous
			}
			if !known {
				owner.Members = append(owner.Members, member.providerProcessIdentity)
			}
		}
		if err := writeProviderOwner(dataDir, owner); err != nil {
			return err
		}
		for _, member := range members {
			if err := signalProviderIdentity(ctx, member); err != nil {
				return err
			}
		}
		if err := waitProviderOwnerObservation(ctx); err != nil {
			return err
		}
	}
}

func waitProviderOwnerObservation(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return errors.Join(ErrOwnershipInconclusive, ctx.Err())
	case <-time.After(10 * time.Millisecond):
		return nil
	}
}

func signalProviderIdentity(ctx context.Context, expected providerProc) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fd, err := unix.PidfdOpen(expected.PID, 0)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	if err != nil {
		return errors.Join(ErrOwnershipInconclusive, err)
	}
	defer func() { _ = unix.Close(fd) }()
	current, err := readProviderProc(expected.PID)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || current.providerProcessIdentity != expected.providerProcessIdentity || current.group != expected.group || current.session != expected.session {
		return errors.Join(ErrOwnershipInconclusive, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err = unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	return err
}

func withProviderOwnerLock(ctx context.Context, dataDir, sessionID, identity string, work func() error) error {
	path, err := providerOwnerPath(dataDir, sessionID, identity)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return errors.Join(ErrOwnershipInconclusive, err)
	}
	defer func() { _ = file.Close() }()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer func() { _ = unix.Flock(int(file.Fd()), unix.LOCK_UN) }()
	return work()
}
