package procmem

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
)

var ErrUnsupported = errors.New("procmem: process memory is not supported on " + runtime.GOOS)

type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

type Process struct {
	PID        int
	PPID       int
	RSSBytes   uint64
	CPUSeconds float64
	Command    string
}

type Tree struct {
	RSSBytes   uint64
	CPUSeconds float64
	Processes  []Process
}

type Table struct {
	byPID    map[int]Process
	children map[int][]int
}

const maxCommandLen = 200

func Parse(out string) (*Table, error) {
	t := &Table{byPID: map[int]Process{}, children: map[int][]int{}}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		rssKiB, err3 := strconv.ParseUint(fields[2], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			return nil, fmt.Errorf("procmem: malformed ps row %q", sc.Text())
		}
		p := Process{PID: pid, PPID: ppid, RSSBytes: rssKiB * 1024}
		rest := fields[3:]
		if len(rest) > 0 {
			if secs, ok := parseCPUTime(rest[0]); ok {
				p.CPUSeconds = secs
				rest = rest[1:]
			}
		}
		p.Command = strings.Join(rest, " ")
		if strings.Contains(p.Command, "<defunct>") {
			continue
		}
		if len(p.Command) > maxCommandLen {
			p.Command = p.Command[:maxCommandLen]
		}
		t.byPID[pid] = p
		t.children[ppid] = append(t.children[ppid], pid)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("procmem: scan ps output: %w", err)
	}
	return t, nil
}

func (t *Table) All() []Process {
	out := make([]Process, 0, len(t.byPID))
	for _, p := range t.byPID {
		out = append(out, p)
	}
	return out
}

func (t *Table) Tree(roots ...int) Tree {
	var tree Tree
	seen := map[int]bool{}
	var walk func(pid int)
	walk = func(pid int) {
		if seen[pid] {
			return
		}
		p, ok := t.byPID[pid]
		if !ok {
			return
		}
		seen[pid] = true
		tree.RSSBytes += p.RSSBytes
		tree.CPUSeconds += p.CPUSeconds
		tree.Processes = append(tree.Processes, p)
		for _, child := range t.children[pid] {
			walk(child)
		}
	}
	for _, root := range roots {
		walk(root)
	}
	return tree
}

func parseCPUTime(s string) (float64, bool) {
	days := 0.0
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.Atoi(d)
		if err != nil {
			return 0, false
		}
		days, s = float64(n), rest
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	var secs float64
	for _, part := range parts {
		n, err := strconv.ParseFloat(part, 64)
		if err != nil || n < 0 {
			return 0, false
		}
		secs = secs*60 + n
	}
	return days*86400 + secs, true
}

func CPUPercent(procs []Process, prev *Table, elapsed float64) float64 {
	if prev == nil || elapsed <= 0 {
		return 0
	}
	var delta float64
	for _, p := range procs {
		before := 0.0
		if q, ok := prev.byPID[p.PID]; ok && q.CPUSeconds <= p.CPUSeconds {
			before = q.CPUSeconds
		}
		delta += p.CPUSeconds - before
	}
	return delta / elapsed * 100
}
