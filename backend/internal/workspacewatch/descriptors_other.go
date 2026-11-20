//go:build !(darwin || dragonfly || freebsd || netbsd || openbsd)

package workspacewatch

import "math"

func watchDescriptorCapacity() int { return math.MaxInt }

func directoryWatchCost(string, bool) int { return 0 }
