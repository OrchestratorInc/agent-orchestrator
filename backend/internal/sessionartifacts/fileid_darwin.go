//go:build darwin

package sessionartifacts

import (
	"io/fs"
	"syscall"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// quickIdentity is the device, inode and birth time in info; macOS reports
// all three from one stat.
func quickIdentity(info fs.FileInfo) domain.FileIdentity {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return domain.FileIdentity{}
	}
	return domain.FileIdentity{
		Dev:   uint64(uint32(st.Dev)), //nolint:gosec // G115: the device id is opaque; keep its bits.
		Ino:   st.Ino,
		Birth: st.Birthtimespec.Sec*1_000_000_000 + st.Birthtimespec.Nsec,
	}
}

func fullIdentity(_ string, info fs.FileInfo) domain.FileIdentity {
	return quickIdentity(info)
}
