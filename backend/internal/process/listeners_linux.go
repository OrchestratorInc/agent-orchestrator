package process

import (
	"context"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ListenersWithEnv returns the TCP listeners of processes whose environment
// holds name=value, leaving out processes running skipExecutable.
func ListenersWithEnv(_ context.Context, name, value, skipExecutable string) ([]Listener, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	want := []byte(name + "=" + value)
	var pids []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		environ, err := os.ReadFile("/proc/" + entry.Name() + "/environ")
		if err != nil || !hasEnv(environ, want) {
			continue
		}
		if exe, _ := os.Readlink("/proc/" + entry.Name() + "/exe"); sameExecutable(exe, skipExecutable) {
			continue
		}
		pids = append(pids, pid)
	}
	if len(pids) == 0 {
		return nil, nil
	}
	ports := map[string]int{}
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		data, err := os.ReadFile(table)
		if err == nil {
			parseProcNetListeners(string(data), ports)
		}
	}
	var listeners []Listener
	for _, pid := range pids {
		dir := "/proc/" + strconv.Itoa(pid)
		fds, err := os.ReadDir(dir + "/fd")
		if err != nil {
			continue
		}
		command := processName(dir)
		for _, fd := range fds {
			link, err := os.Readlink(dir + "/fd/" + fd.Name())
			inode, ok := strings.CutPrefix(link, "socket:[")
			if err != nil || !ok {
				continue
			}
			if port, ok := ports[strings.TrimSuffix(inode, "]")]; ok {
				listeners = append(listeners, Listener{Port: port, PID: pid, Command: command})
			}
		}
	}
	return uniqueByPort(listeners), nil
}

func processName(dir string) string {
	if cmdline, err := os.ReadFile(dir + "/cmdline"); err == nil {
		if argv0, _, _ := strings.Cut(string(cmdline), "\x00"); argv0 != "" {
			return filepath.Base(argv0)
		}
	}
	comm, _ := os.ReadFile(dir + "/comm")
	return strings.TrimSpace(string(comm))
}

func parseProcNetListeners(table string, ports map[string]int) {
	for line := range strings.SplitSeq(table, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 || fields[3] != "0A" {
			continue
		}
		address, portHex, ok := strings.Cut(fields[1], ":")
		port, err := strconv.ParseUint(portHex, 16, 16)
		if !ok || err != nil || !reachableFromLoopback(procNetIP(address)) {
			continue
		}
		ports[fields[9]] = int(port)
	}
}

func procNetIP(word string) string {
	raw, err := hex.DecodeString(word)
	if err != nil || len(raw)%4 != 0 {
		return ""
	}
	for i := 0; i < len(raw); i += 4 {
		raw[i], raw[i+1], raw[i+2], raw[i+3] = raw[i+3], raw[i+2], raw[i+1], raw[i]
	}
	return net.IP(raw).String()
}
