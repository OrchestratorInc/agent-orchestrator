package process

import (
	"bytes"
	"cmp"
	"net"
	"path/filepath"
	"slices"
	"strings"
)

// Listener is a TCP port a process listens on at a loopback or unspecified address.
type Listener struct {
	Port    int
	PID     int
	Command string
}

func hasEnv(environ []byte, entry []byte) bool {
	for field := range bytes.SplitSeq(environ, []byte{0}) {
		if bytes.Equal(field, entry) {
			return true
		}
	}
	return false
}

func reachableFromLoopback(host string) bool {
	host = strings.Trim(host, "[]")
	if host == "*" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

func sameExecutable(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(a); err == nil {
		a = resolved
	}
	if resolved, err := filepath.EvalSymlinks(b); err == nil {
		b = resolved
	}
	return a == b
}

func uniqueByPort(listeners []Listener) []Listener {
	slices.SortFunc(listeners, func(a, b Listener) int { return cmp.Or(cmp.Compare(a.Port, b.Port), cmp.Compare(a.PID, b.PID)) })
	return slices.CompactFunc(listeners, func(a, b Listener) bool { return a.Port == b.Port })
}
