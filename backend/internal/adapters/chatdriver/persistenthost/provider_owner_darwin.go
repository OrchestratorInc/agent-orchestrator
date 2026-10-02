//go:build darwin

package persistenthost

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const providerOwnerProofSupported = true

func providerBootID() (string, error) {
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil || strings.TrimSpace(boot) == "" {
		return "", errors.Join(ErrOwnershipInconclusive, err)
	}
	return strings.TrimSpace(boot), nil
}

func providerDarwinIdentity(process unix.ExternProc) providerProcessIdentity {
	return providerProcessIdentity{PID: int(process.P_pid), Start: uint64(process.P_starttime.Sec)*1_000_000 + uint64(process.P_starttime.Usec)}
}

func captureProviderOwner(owner *providerOwner, pid int) error {
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || int(process.Eproc.Pgid) != pid {
		return errors.Join(ErrOwnershipInconclusive, err)
	}
	session, err := unix.Getsid(pid)
	if err != nil {
		return errors.Join(ErrOwnershipInconclusive, err)
	}
	boot, err := providerBootID()
	if err != nil {
		return err
	}
	owner.Boot, owner.Group, owner.SessionID = boot, pid, session
	owner.Members = []providerProcessIdentity{providerDarwinIdentity(process.Proc)}
	owner.State = "active"
	if !validProviderOwnerProof(*owner) {
		return ErrOwnershipInconclusive
	}
	return nil
}

func providerGroupStopped(owner providerOwner) (bool, error) {
	if !validProviderOwnerProof(owner) || owner.Proof != "" {
		return false, ErrOwnershipInconclusive
	}
	boot, err := providerBootID()
	if err != nil {
		return false, err
	}
	if boot != owner.Boot {
		return true, nil
	}
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return false, errors.Join(ErrOwnershipInconclusive, err)
	}
	for _, process := range processes {
		identity := providerDarwinIdentity(process.Proc)
		for _, known := range owner.Members {
			if identity != known {
				continue
			}
			session, err := unix.Getsid(identity.PID)
			if errors.Is(err, unix.ESRCH) {
				continue
			}
			if err != nil || int(process.Eproc.Pgid) != owner.Group || session != owner.SessionID {
				return false, errors.Join(ErrOwnershipInconclusive, err)
			}
		}
	}
	// A child can leave the recorded group without appearing in this census.
	return false, errors.Join(ErrOwnershipInconclusive, errProviderContainmentRequired)
}

func stopProviderOwner(ctx context.Context, dataDir string, owner providerOwner) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	stopped, err := providerGroupStopped(owner)
	if err != nil || !stopped {
		// A reusable PID/group cannot safely target a cold orphan on this platform.
		return errors.Join(ErrOwnershipInconclusive, err)
	}
	owner.State = "stopped"
	return writeProviderOwner(dataDir, owner)
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
			return errors.Join(ErrOwnershipInconclusive, err)
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
