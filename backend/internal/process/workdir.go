package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// ErrWorkingDirUnsupported reports a platform where another process's working
// directory cannot be read.
var ErrWorkingDirUnsupported = errors.New("process working directory is unavailable on this platform")

// ErrOpenFilesUnsupported reports a platform where another process's open
// files cannot be listed.
var ErrOpenFilesUnsupported = errors.New("process open files are unavailable on this platform")

// WorkingDir returns the current working directory of the process pid.
func WorkingDir(ctx context.Context, pid int) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("process working directory: invalid pid %d", pid)
	}
	switch runtime.GOOS {
	case "linux":
		return os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
	case "darwin":
		out, err := CommandContext(ctx, "lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
		if err != nil {
			return "", fmt.Errorf("process working directory of %d: %w", pid, err)
		}
		for line := range strings.SplitSeq(string(out), "\n") {
			if dir, ok := strings.CutPrefix(line, "n"); ok && dir != "" {
				return dir, nil
			}
		}
		return "", fmt.Errorf("process working directory of %d: not reported", pid)
	default:
		return "", ErrWorkingDirUnsupported
	}
}

// GroupOpenFiles returns the paths of the files the processes in group pgid
// have open.
func GroupOpenFiles(ctx context.Context, pgid int) ([]string, error) {
	if pgid <= 0 {
		return nil, fmt.Errorf("process group open files: invalid group %d", pgid)
	}
	switch runtime.GOOS {
	case "linux":
		entries, err := os.ReadDir("/proc")
		if err != nil {
			return nil, fmt.Errorf("process group open files of %d: %w", pgid, err)
		}
		var paths []string
		for _, entry := range entries {
			if linuxProcessGroup(entry.Name()) != pgid {
				continue
			}
			dir := "/proc/" + entry.Name() + "/fd"
			fds, _ := os.ReadDir(dir)
			for _, fd := range fds {
				if path, err := os.Readlink(dir + "/" + fd.Name()); err == nil && strings.HasPrefix(path, "/") {
					paths = append(paths, path)
				}
			}
		}
		return paths, nil
	case "darwin":
		out, err := CommandContext(ctx, "lsof", "-n", "-P", "-a", "-g", strconv.Itoa(pgid), "-d", "0-65535", "-Fn").Output()
		if err != nil && len(out) == 0 {
			return nil, fmt.Errorf("process group open files of %d: %w", pgid, err)
		}
		var paths []string
		for line := range strings.SplitSeq(string(out), "\n") {
			if path, ok := strings.CutPrefix(line, "n"); ok && strings.HasPrefix(path, "/") {
				paths = append(paths, path)
			}
		}
		return paths, nil
	default:
		return nil, ErrOpenFilesUnsupported
	}
}

func linuxProcessGroup(pid string) int {
	if _, err := strconv.Atoi(pid); err != nil {
		return 0
	}
	stat, err := os.ReadFile("/proc/" + pid + "/stat")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(stat[bytes.LastIndexByte(stat, ')')+1:]))
	if len(fields) < 3 {
		return 0
	}
	pgrp, _ := strconv.Atoi(fields[2])
	return pgrp
}
