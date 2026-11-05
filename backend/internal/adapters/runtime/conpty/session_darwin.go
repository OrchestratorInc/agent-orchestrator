//go:build darwin

package conpty

import (
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// darwinSessionGroups returns the process groups of every process in the
// session led by leader, the leader's own group first. A shell with job
// control runs each foreground job in its own group, so signalling only the
// leader's group misses an agent the user started at the prompt. Real agents
// exit on the hangup that follows, but one that ignores SIGHUP would outlive
// its closed terminal. Linux gets the same coverage from /proc (see
// linuxFindSessionProcesses).
//
// A session id is its leader's pid, and no other session can take that id
// while the leader is alive, so the snapshot is taken before any signal.
func darwinSessionGroups(leader int) []int {
	groups := []int{leader}
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return groups
	}
	seen := map[int]bool{leader: true}
	for i := range procs {
		pid := int(procs[i].Proc.P_pid)
		pgid := int(procs[i].Eproc.Pgid)
		if pid <= 1 || pgid <= 1 || seen[pgid] {
			continue
		}
		if sid, err := unix.Getsid(pid); err == nil && sid == leader {
			seen[pgid] = true
			groups = append(groups, pgid)
		}
	}
	return groups
}

func signalDarwinGroups(groups []int, sig syscall.Signal) {
	for _, pgid := range groups {
		_ = syscall.Kill(-pgid, sig)
	}
}

func waitForDarwinGroupsExit(groups []int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		alive := false
		for _, pgid := range groups {
			if darwinProcessGroupAlive(pgid) {
				alive = true
				break
			}
		}
		if !alive {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// mergeGroups returns a plus any groups in b it does not already contain.
func mergeGroups(a, b []int) []int {
	out := append([]int(nil), a...)
	for _, g := range b {
		found := false
		for _, have := range out {
			if have == g {
				found = true
				break
			}
		}
		if !found {
			out = append(out, g)
		}
	}
	return out
}
