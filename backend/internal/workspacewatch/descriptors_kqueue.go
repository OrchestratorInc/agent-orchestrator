//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package workspacewatch

import (
	"math"
	"os"

	"golang.org/x/sys/unix"
)

func watchDescriptorCapacity() int {
	var limit unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &limit); err != nil {
		return math.MaxInt
	}
	soft := int(min(uint64(limit.Cur), 1<<30))
	return max(soft-max(soft/4, 1024), 0)
}

func directoryWatchCost(dir string, parentWatched bool) int {
	cost := 1
	if parentWatched {
		cost = 0
	}
	f, err := os.Open(dir)
	if err != nil {
		return cost
	}
	defer func() { _ = f.Close() }()
	names, _ := f.Readdirnames(-1)
	return cost + len(names)
}
