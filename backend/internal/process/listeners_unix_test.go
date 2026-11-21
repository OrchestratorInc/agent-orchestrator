//go:build darwin || linux

package process

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestListenerHelperProcess(t *testing.T) {
	if os.Getenv("AO_LISTENER_HELPER") != "1" {
		t.Skip("helper process")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.Exit(2)
	}
	fmt.Println(ln.Addr().(*net.TCPAddr).Port)
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func TestListenersWithEnvFindsTheMarkedProcessesPorts(t *testing.T) {
	marker := make([]byte, 8)
	_, _ = rand.Read(marker)
	value := hex.EncodeToString(marker)
	cmd := exec.Command(os.Args[0], "-test.run=^TestListenerHelperProcess$")
	cmd.Env = append(os.Environ(), "AO_LISTENER_HELPER=1", "AO_LISTENER_MARK="+value)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Wait() })
	line, err := bufio.NewReader(stdout).ReadString('\n')
	port, convErr := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || convErr != nil {
		t.Fatalf("helper port: %q %v %v", line, err, convErr)
	}
	ports := func(value, skip string) []int {
		listeners, err := ListenersWithEnv(context.Background(), "AO_LISTENER_MARK", value, skip)
		if err != nil {
			t.Fatal(err)
		}
		var out []int
		for _, l := range listeners {
			if l.PID == cmd.Process.Pid {
				out = append(out, l.Port)
			}
		}
		return out
	}
	if got := ports(value, ""); !slices.Equal(got, []int{port}) {
		t.Fatalf("marked process listens on %d, found %v", port, got)
	}
	if got := ports(value+"x", ""); len(got) != 0 {
		t.Fatalf("another marker found %v", got)
	}
	if got := ports(value, os.Args[0]); len(got) != 0 {
		t.Fatalf("a skipped executable found %v", got)
	}
}

func TestSameExecutableMatchesAReplacedBinary(t *testing.T) {
	if !sameExecutable("/opt/ao/bin/ao (deleted)", "/opt/ao/bin/ao") {
		t.Fatal("a process still running a replaced ao binary is not skipped")
	}
	if sameExecutable("/usr/bin/node", "/opt/ao/bin/ao") {
		t.Fatal("another executable is skipped")
	}
}
