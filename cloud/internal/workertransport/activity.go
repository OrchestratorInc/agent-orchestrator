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
}

type cpuSample struct {
	total, idle, self uint64
	ok                bool
}

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
	sample, ok := readCPU(root)
	cpuBusy := false
	if ok && a.cpu.ok && sample.total > a.cpu.total {
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
