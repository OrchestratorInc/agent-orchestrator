//go:build linux

package sessionartifacts

import (
	"io/fs"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// fsIOCGetVersion is FS_IOC_GETVERSION, _IOR('v', 1, long): the inode's
// generation number, which ext4, btrfs and XFS change whenever they reuse an
// inode number for a new file.
const fsIOCGetVersion = 2<<30 | uint(unsafe.Sizeof(int(0)))<<16 | 'v'<<8 | 1

// quickIdentity is the device, inode and modification time in info. The
// birth time and generation take a statx and an open, which fullIdentity adds.
func quickIdentity(info fs.FileInfo) domain.FileIdentity {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return domain.FileIdentity{}
	}
	return domain.FileIdentity{Dev: uint64(st.Dev), Ino: st.Ino, ModTime: info.ModTime().UnixNano()} //nolint:unconvert // Dev is not uint64 on every Linux architecture
}

// fullIdentity adds the birth time and generation of the file at path, where
// its file system records them. Linux birth times are only as fine as the
// kernel's coarse clock, so a file written right after another was deleted can
// share its inode and birth time: the generation tells them apart. Where there
// is no generation, the modification time stays in the identity instead, so a
// file renamed and then modified before the next scan is a new file.
func fullIdentity(path string, info fs.FileInfo) domain.FileIdentity {
	id := quickIdentity(info)
	if id.Ino == 0 {
		return id
	}
	var stx unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BTIME|unix.STATX_INO, &stx); err != nil ||
		stx.Mask&unix.STATX_BTIME == 0 || stx.Ino != id.Ino {
		return id
	}
	id.Birth = stx.Btime.Sec*1_000_000_000 + int64(stx.Btime.Nsec)
	if generation, ok := inodeGeneration(path, id.Ino); ok {
		id.Generation, id.ModTime = uint64(generation), 0
	}
	return id
}

func inodeGeneration(path string, ino uint64) (uint32, bool) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOCTTY, 0)
	if err != nil {
		return 0, false
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Ino != ino {
		return 0, false
	}
	generation, err := unix.IoctlGetUint32(fd, fsIOCGetVersion)
	return generation, err == nil && generation != 0
}
