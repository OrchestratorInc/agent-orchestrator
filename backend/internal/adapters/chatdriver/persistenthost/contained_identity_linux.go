//go:build linux

package persistenthost

import (
	"context"
	"errors"
	"math"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

type namespaceOwner struct {
	Boot      string `json:"boot"`
	PID       int    `json:"pid"`
	Start     uint64 `json:"start"`
	Namespace uint64 `json:"namespace"`
	Process   uint64 `json:"process"`
}

func validNamespaceOwner(owner namespaceOwner) bool {
	boot, err := uuid.Parse(owner.Boot)
	return err == nil && boot != uuid.Nil && boot.String() == owner.Boot && owner.PID > 1 && owner.Start > 0 && owner.Namespace > 0 && owner.Process > 1
}

func namespaceProcessID(fd int) (uint64, error) {
	var filesystem unix.Statfs_t
	if err := unix.Fstatfs(fd, &filesystem); err != nil || filesystem.Type != unix.PID_FS_MAGIC || strconv.IntSize != 64 {
		return 0, errors.Join(ErrOwnershipInconclusive, err)
	}
	var info unix.Stat_t
	if err := unix.Fstat(fd, &info); err != nil || info.Ino <= 1 {
		return 0, errors.Join(ErrOwnershipInconclusive, err)
	}
	return info.Ino, nil
}

func namespaceInode(pid string) (uint64, error) {
	info, err := os.Stat("/proc/" + pid + "/ns/pid")
	if err != nil {
		return 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Ino == 0 {
		return 0, ErrOwnershipInconclusive
	}
	return stat.Ino, nil
}

func captureNamespaceOwner(pid int) (namespaceOwner, error) {
	var owner namespaceOwner
	if pid <= 1 {
		return owner, ErrOwnershipInconclusive
	}
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return owner, errors.Join(ErrOwnershipInconclusive, err)
	}
	defer func() { _ = unix.Close(fd) }()
	processID, err := namespaceProcessID(fd)
	if err != nil {
		return owner, err
	}
	process, err := readProviderProc(pid)
	if err != nil || process.Start == 0 {
		return owner, errors.Join(ErrOwnershipInconclusive, err)
	}
	status, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return owner, errors.Join(ErrOwnershipInconclusive, err)
	}
	init := false
	for line := range strings.SplitSeq(string(status), "\n") {
		if strings.HasPrefix(line, "NSpid:") {
			ids := strings.Fields(strings.TrimPrefix(line, "NSpid:"))
			init = len(ids) > 1 && ids[0] == strconv.Itoa(pid) && ids[len(ids)-1] == "1"
		}
	}
	if !init {
		return owner, ErrOwnershipInconclusive
	}
	boot, err := providerBootID()
	if err != nil {
		return owner, err
	}
	namespace, err := namespaceInode(strconv.Itoa(pid))
	if err != nil {
		return owner, errors.Join(ErrOwnershipInconclusive, err)
	}
	parentNamespace, err := namespaceInode("self")
	if err != nil || namespace == parentNamespace {
		return owner, errors.Join(ErrOwnershipInconclusive, err)
	}
	owner = namespaceOwner{Boot: boot, PID: pid, Start: process.Start, Namespace: namespace, Process: processID}
	stopped, err := namespaceDescriptorStopped(fd)
	if err != nil || stopped || !validNamespaceOwner(owner) {
		return namespaceOwner{}, errors.Join(ErrOwnershipInconclusive, err)
	}
	return owner, nil
}

func namespaceDescriptorStopped(fd int) (bool, error) {
	if fd < 0 || fd > math.MaxInt32 {
		return false, ErrOwnershipInconclusive
	}
	observations := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	if _, err := unix.Poll(observations, 0); err != nil {
		return false, errors.Join(ErrOwnershipInconclusive, err)
	}
	if observations[0].Revents&(unix.POLLERR|unix.POLLNVAL) != 0 {
		return false, ErrOwnershipInconclusive
	}
	return observations[0].Revents&unix.POLLIN != 0, nil
}

func namespaceOwnsPermit(owner namespaceOwner, pipe *os.File, fd int) error {
	writer, err := pipe.Stat()
	if err != nil || writer.Mode()&os.ModeNamedPipe == 0 {
		return errors.Join(ErrOwnershipInconclusive, err)
	}
	flags, err := unix.FcntlInt(pipe.Fd(), unix.F_GETFL, 0)
	if err != nil || flags&unix.O_ACCMODE != unix.O_WRONLY {
		return errors.Join(ErrOwnershipInconclusive, err)
	}
	reader, err := os.Stat("/proc/" + strconv.Itoa(owner.PID) + "/fd/3")
	if err != nil || !os.SameFile(writer, reader) {
		return errors.Join(ErrOwnershipInconclusive, err)
	}
	info, err := os.ReadFile("/proc/" + strconv.Itoa(owner.PID) + "/fdinfo/3")
	if err != nil {
		return errors.Join(ErrOwnershipInconclusive, err)
	}
	readOnly := false
	for line := range strings.SplitSeq(string(info), "\n") {
		if strings.HasPrefix(line, "flags:") {
			flags, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "flags:")), 8, 64)
			readOnly = err == nil && flags&unix.O_ACCMODE == unix.O_RDONLY
		}
	}
	stopped, err := namespaceDescriptorStopped(fd)
	if !readOnly || err != nil || stopped {
		return errors.Join(ErrOwnershipInconclusive, err)
	}
	return nil
}

func openNamespaceOwner(owner namespaceOwner) (int, bool, error) {
	if !validNamespaceOwner(owner) {
		return -1, false, ErrOwnershipInconclusive
	}
	boot, err := providerBootID()
	if err != nil {
		return -1, false, err
	}
	if boot != owner.Boot {
		return -1, true, nil
	}
	fd, err := unix.PidfdOpen(owner.PID, 0)
	if errors.Is(err, unix.ESRCH) {
		return -1, true, nil
	}
	if err != nil {
		return -1, false, errors.Join(ErrOwnershipInconclusive, err)
	}
	processID, err := namespaceProcessID(fd)
	if err != nil {
		_ = unix.Close(fd)
		return -1, false, err
	}
	if processID != owner.Process {
		_ = unix.Close(fd)
		return -1, true, nil
	}
	current, err := captureNamespaceOwner(owner.PID)
	if err == nil && current == owner {
		return fd, false, nil
	}
	// A pidfd opened before the observation cannot signal a later PID occupant.
	stopped, probeErr := namespaceDescriptorStopped(fd)
	_ = unix.Close(fd)
	if probeErr == nil && stopped {
		return -1, true, nil
	}
	return -1, false, errors.Join(ErrOwnershipInconclusive, err, probeErr)
}

func stopNamespaceOwner(ctx context.Context, owner namespaceOwner) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fd, stopped, err := openNamespaceOwner(owner)
	if err != nil || stopped {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0); err != nil && !errors.Is(err, unix.ESRCH) {
		return errors.Join(ErrOwnershipInconclusive, err)
	}
	for {
		stopped, err := namespaceDescriptorStopped(fd)
		if err != nil || stopped {
			return err
		}
		if err := waitProviderOwnerObservation(ctx); err != nil {
			return err
		}
	}
}
