// Package procmem measures the resident memory of a process tree. It takes one
// snapshot of the whole process table so many sessions can be costed from a
// single `ps` run, then walks each root's descendants.
package procmem

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// ErrUnsupported is returned where the process table cannot be read.
var ErrUnsupported = errors.New("procmem: process memory is not supported on " + runtime.GOOS)

// Process is one row of the process table.
type Process struct {
	PID      int
	PPID     int
	RSSBytes uint64
	Command  string
}

// Tree is the memory reading for one root and all of its descendants.
type Tree struct {
	RSSBytes  uint64
	Processes []Process
}

// Table is a snapshot of every process, indexed for tree walks.
type Table struct {
	byPID    map[int]Process
	children map[int][]int
}

// Runner executes a command and returns its combined output. It matches the
// shape the runtime adapters already inject for tests.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// Snapshot reads the current process table. A nil runner uses exec.
func Snapshot(ctx context.Context, run Runner) (*Table, error) {
	if runtime.GOOS == "windows" {
		return nil, ErrUnsupported
	}
	if run == nil {
		run = execRunner
	}
	// rss= is KiB on both Linux and macOS ps.
	out, err := run(ctx, "ps", "-axo", "pid=,ppid=,rss=,comm=")
	if err != nil {
		return nil, fmt.Errorf("procmem: ps: %w", err)
	}
	return Parse(string(out))
}

// Parse builds a Table from `ps -axo pid=,ppid=,rss=,comm=` output.
func Parse(out string) (*Table, error) {
	t := &Table{byPID: map[int]Process{}, children: map[int][]int{}}
	sc := bufio.NewScanner(strings.NewReader(out))
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
		if len(fields) > 3 {
			p.Command = strings.Join(fields[3:], " ")
		}
		t.byPID[pid] = p
		t.children[ppid] = append(t.children[ppid], pid)
	}
	return t, nil
}

// Tree sums the root and every descendant. A root that no longer exists
// yields an empty tree. Each pid is counted once even if several roots share
// descendants.
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
