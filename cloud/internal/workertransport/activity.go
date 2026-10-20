package workertransport

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// busyCPUFraction is the share of one core, outside the worker itself, above
// which the sandbox counts as working: an agent thinking or a build or test it
// started. An agent waiting at its prompt, and an idle shell, stay well below.
const busyCPUFraction = 0.02

// activity tracks whether anything happened in the sandbox since the last
// check, for idle pause: terminal output, a process running under a workspace
// shell, or CPU use beyond the worker's own. A session is idle only when none
// of these happened for the whole idle threshold.
type activity struct {
	mu         sync.Mutex
	lastOutput time.Time
	lastCheck  time.Time
	cpu        cpuSample
	procRoot   string
	// cgroupRoot is where the cgroup filesystem is mounted (/sys/fs/cgroup by
	// default). Its root group is the sandbox's own when the worker runs in a
	// container, which /proc/stat is not.
	cgroupRoot string
}

type cpuSample struct {
	total, idle, self uint64
	// cgroupUsage is the cumulative CPU time of the sandbox's cgroup, taken
	// at at; cgroup reports whether it was read (and the sample uses it).
	cgroupUsage time.Duration
	at          time.Time
	cgroup      bool
	ok          bool
}

// clockTick is the kernel's USER_HZ tick that /proc reports CPU time in. It is
// 100 on every Linux architecture AO's workers run on.
const clockTick = 10 * time.Millisecond

func (a *activity) noteOutput(at time.Time) {
	a.mu.Lock()
	if at.After(a.lastOutput) {
		a.lastOutput = at
	}
	a.mu.Unlock()
}

// busySince reports whether the sandbox did anything since the previous call.
// shellPIDs are the workspace terminals' shells: any process under one of
// them (a dev server, a test run) keeps the session awake.
func (a *activity) busySince(now time.Time, shellPIDs []int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	previous := a.lastCheck
	a.lastCheck = now
	outputBusy := !previous.IsZero() && a.lastOutput.After(previous)

	root := a.procRoot
	if root == "" {
		root = "/proc"
	}
	cgroupRoot := a.cgroupRoot
	if cgroupRoot == "" {
		cgroupRoot = "/sys/fs/cgroup"
	}
	sample, ok := readCPU(root)
	if ok {
		// Prefer the cgroup's own CPU time: in a container (a Docker-based
		// Coder workspace) /proc/stat is the whole host's, so other tenants'
		// work would keep this sandbox from ever counting as idle.
		if usage, found := readCgroupCPU(cgroupRoot); found {
			sample.cgroupUsage, sample.cgroup, sample.at = usage, true, now
		}
	}
	cpuBusy := false
	switch {
	case ok && a.cpu.ok && sample.cgroup && a.cpu.cgroup && now.After(a.cpu.at):
		wall := float64(now.Sub(a.cpu.at))
		self := float64(time.Duration(sample.self-a.cpu.self) * clockTick)
		busy := float64(sample.cgroupUsage-a.cpu.cgroupUsage) - self
		// Cgroup usage is CPU time across cores; over wall time it is cores
		// in use.
		cpuBusy = busy/wall > busyCPUFraction
	case ok && a.cpu.ok && !sample.cgroup && !a.cpu.cgroup && sample.total > a.cpu.total:
		elapsed := float64(sample.total - a.cpu.total)
		busy := elapsed - float64(sample.idle-a.cpu.idle) - float64(sample.self-a.cpu.self)
		// /proc/stat sums every core, so normalize to "cores in use" before
		// comparing against a fraction of one core.
		cores := float64(cpuCount(root))
		if cores < 1 {
			cores = 1
		}
		cpuBusy = busy/elapsed*cores > busyCPUFraction
	}
	if ok {
		a.cpu = sample
	}
	return outputBusy || cpuBusy || hasChildren(root, shellPIDs)
}

// readCgroupCPU returns the cumulative CPU time of the cgroup mounted at root:
// cgroup v2's cpu.stat usage_usec, or cgroup v1's cpuacct.usage. It reports
// false when neither is readable (not Linux, or no cgroup filesystem), and the
// caller falls back to /proc/stat.
func readCgroupCPU(root string) (time.Duration, bool) {
	if stat, err := os.ReadFile(filepath.Join(root, "cpu.stat")); err == nil {
		for _, line := range strings.Split(string(stat), "\n") {
			key, value, found := strings.Cut(strings.TrimSpace(line), " ")
			if !found || key != "usage_usec" {
				continue
			}
			usec, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
			if err != nil {
				return 0, false
			}
			return time.Duration(usec) * time.Microsecond, true
		}
	}
	for _, controller := range []string{"cpuacct", "cpu,cpuacct", "cpuacct,cpu"} {
		usage, err := os.ReadFile(filepath.Join(root, controller, "cpuacct.usage"))
		if err != nil {
			continue
		}
		nanoseconds, err := strconv.ParseUint(strings.TrimSpace(string(usage)), 10, 64)
		if err != nil {
			return 0, false
		}
		return time.Duration(nanoseconds), true
	}
	return 0, false
}

// readCPU returns system-wide CPU time (all cores) and this process's own,
// in clock ticks. It reports false off Linux, where idle pause still works
// from output and interaction alone.
func readCPU(root string) (cpuSample, bool) {
	stat, err := os.ReadFile(filepath.Join(root, "stat"))
	if err != nil {
		return cpuSample{}, false
	}
	line, _, _ := strings.Cut(string(stat), "\n")
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuSample{}, false
	}
	var sample cpuSample
	for index, field := range fields[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return cpuSample{}, false
		}
		sample.total += value
		// idle and iowait are not work.
		if index == 3 || index == 4 {
			sample.idle += value
		}
	}
	self, err := os.ReadFile(filepath.Join(root, "self", "stat"))
	if err != nil {
		return cpuSample{}, false
	}
	// The command name (field 2) may contain spaces; utime and stime are the
	// 14th and 15th fields overall, the 12th and 13th after the closing paren.
	text := string(self)
	if closing := strings.LastIndexByte(text, ')'); closing >= 0 {
		rest := strings.Fields(text[closing+1:])
		if len(rest) >= 13 {
			utime, err1 := strconv.ParseUint(rest[11], 10, 64)
			stime, err2 := strconv.ParseUint(rest[12], 10, 64)
			if err1 == nil && err2 == nil {
				sample.self = utime + stime
			}
		}
	}
	sample.ok = true
	return sample, true
}

func cpuCount(root string) int {
	stat, err := os.ReadFile(filepath.Join(root, "stat"))
	if err != nil {
		return 1
	}
	count := 0
	for _, line := range strings.Split(string(stat), "\n") {
		if strings.HasPrefix(line, "cpu") && len(line) > 3 && line[3] >= '0' && line[3] <= '9' {
			count++
		}
	}
	return count
}

// hasChildren reports whether any process's parent is one of pids.
func hasChildren(root string, pids []int) bool {
	if len(pids) == 0 {
		return false
	}
	parents := make(map[int]bool, len(pids))
	for _, pid := range pids {
		if pid > 0 {
			parents[pid] = true
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join(root, entry.Name(), "stat"))
		if err != nil {
			continue
		}
		text := string(stat)
		closing := strings.LastIndexByte(text, ')')
		if closing < 0 {
			continue
		}
		rest := strings.Fields(text[closing+1:])
		// rest[0] is the state, rest[1] the parent pid.
		if len(rest) < 2 {
			continue
		}
		parent, err := strconv.Atoi(rest[1])
		if err == nil && parents[parent] {
			return true
		}
	}
	return false
}
