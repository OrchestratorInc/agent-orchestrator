//go:build linux && e2e

package persistenthost

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestNamespaceProcessFixture(t *testing.T) {
	mode := os.Getenv("AO_NAMESPACE_FIXTURE")
	if mode == "" {
		return
	}
	if mode == "host" {
		runNamespaceHostFixture(t)
		return
	}
	if mode == "native" {
		fmt.Println("native-ready")
		var request [1]byte
		if _, err := io.ReadFull(os.Stdin, request[:]); err != nil {
			t.Fatal(err)
		}
		return
	}
	if mode == "init" {
		if os.Getpid() != 1 {
			t.Fatal("trusted helper is not namespace init")
		}
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		fmt.Println("init-ready")
		permit := os.NewFile(3, "execution-permit")
		defer permit.Close()
		if err := awaitNamespacePermit(ctx, os.Getenv("AO_NAMESPACE_IDENTITY"), permit); err != nil {
			return
		}
		child := exec.Command("/fixture", "-test.run=^TestNamespaceProcessFixture$")
		child.Env = []string{"AO_NAMESPACE_FIXTURE=parent", "GORACE=atexit_sleep_ms=0"}
		child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		waitNamespaceFixtureFile(t, "/state/parent-ready")
		fmt.Println("provider-started")
		if err := child.Wait(); err != nil {
			t.Fatal(err)
		}
		fmt.Println("late-descendant-ready")
		for {
			time.Sleep(time.Second)
		}
	}
	if mode == "parent" {
		if err := os.WriteFile("/state/parent-ready", nil, 0o600); err != nil {
			t.Fatal(err)
		}
		waitNamespaceFixtureFile(t, "/state/fork")
		child := exec.Command("/fixture", "-test.run=^TestNamespaceProcessFixture$")
		child.Env = []string{"AO_NAMESPACE_FIXTURE=grandchild", "GORACE=atexit_sleep_ms=0"}
		child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		waitNamespaceFixtureFile(t, "/state/grandchild-ready")
		return
	}
	if mode != "grandchild" {
		t.Fatal("unknown fixture mode")
	}
	if err := os.WriteFile("/state/grandchild-ready", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for {
		time.Sleep(time.Second)
	}
}

func waitNamespaceFixtureFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("fixture barrier not reached", filepath.Base(path))
}

type namespaceProcessFixture struct {
	owner  namespaceOwner
	permit *os.File
	output *bufio.Reader
	state  string
	wait   func() error
}

func startNamespaceProcessFixture(t *testing.T, identity string) namespaceProcessFixture {
	t.Helper()
	return startNamespaceProcessFixtureInState(t, identity, t.TempDir())
}

func startNamespaceProcessFixtureInState(t *testing.T, identity, state string) namespaceProcessFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	t.Cleanup(cancel)
	permitReader, permitWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = permitReader.Close(); _ = permitWriter.Close() })
	infoReader, infoWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = infoReader.Close(); _ = infoWriter.Close() })
	command := exec.CommandContext(ctx, "/usr/bin/bwrap", "--unshare-all", "--unshare-user", "--unshare-cgroup", "--disable-userns",
		"--die-with-parent", "--new-session", "--as-pid-1", "--info-fd", "4",
		"--ro-bind", "/usr", "/usr", "--symlink", "usr/bin", "/bin", "--symlink", "usr/lib", "/lib", "--symlink", "usr/lib", "/lib64",
		"--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp", "--dir", "/run", "--dir", "/sys", "--bind", state, "/state",
		"--ro-bind", os.Args[0], "/fixture", "--clearenv", "--setenv", "AO_NAMESPACE_FIXTURE", "init",
		"--setenv", "AO_NAMESPACE_IDENTITY", identity, "--setenv", "GORACE", "atexit_sleep_ms=0", "--", "/fixture", "-test.run=^TestNamespaceProcessFixture$")
	command.ExtraFiles = []*os.File{permitReader, infoWriter}
	var diagnostics bytes.Buffer
	command.Stderr = &diagnostics
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	var waitErr error
	wait := func() error { once.Do(func() { waitErr = command.Wait() }); return waitErr }
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = wait()
		if diagnostics.Len() != 0 {
			t.Log(diagnostics.String())
		}
	})
	_ = permitReader.Close()
	_ = infoWriter.Close()
	if err := infoReader.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var info struct {
		PID int `json:"child-pid"`
	}
	if err := json.NewDecoder(infoReader).Decode(&info); err != nil {
		t.Fatal("namespace tool did not publish identity", err)
	}
	reader := bufio.NewReader(output)
	if line, err := reader.ReadString('\n'); err != nil || line != "init-ready\n" {
		t.Fatal("trusted init did not reach the blocked permit", line, err)
	}
	owner, err := captureNamespaceOwner(info.PID)
	if err != nil {
		t.Fatal("exact namespace-init identity not captured", err)
	}
	return namespaceProcessFixture{owner: owner, permit: permitWriter, output: reader, state: state, wait: wait}
}

func startNamespaceNativeControl(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 25*time.Second)
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNamespaceProcessFixture$")
	command.Env = []string{"AO_NAMESPACE_FIXTURE=native", "GORACE=atexit_sleep_ms=0"}
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, writeErr := io.WriteString(input, "x")
		_ = input.Close()
		if err := command.Wait(); err != nil || writeErr != nil {
			t.Error("unrelated native control did not survive", err, writeErr)
		}
	})
	if line, err := bufio.NewReader(output).ReadString('\n'); err != nil || line != "native-ready\n" {
		t.Fatal("native control not ready", err)
	}
}

func namespaceFixtureMembers(t *testing.T, inode uint64) map[int]providerProc {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[int]providerProc)
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		namespace, err := namespaceInode(entry.Name())
		if err != nil || namespace != inode {
			continue
		}
		process, err := readProviderProc(pid)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if process.state != "Z" && process.state != "X" {
			result[pid] = process
		}
	}
	return result
}

func TestNamespaceLaunchProcessRetirementAfterLateFork(t *testing.T) {
	for _, mode := range []string{"exact-stop", "keeper-death"} {
		t.Run(mode, func(t *testing.T) {
			startNamespaceNativeControl(t)
			dataDir := t.TempDir()
			original, err := prepareNamespaceLaunch(t.Context(), dataDir, "session-a", strings.Repeat("a", 64))
			if err != nil {
				t.Fatal(err)
			}
			process := startNamespaceProcessFixture(t, original.Identity)
			original, err = publishNamespaceLaunch(t.Context(), dataDir, original, process.owner)
			if err != nil {
				t.Fatal(err)
			}
			original, err = grantNamespaceLaunch(t.Context(), dataDir, original, process.permit)
			if err != nil {
				t.Fatal(err)
			}
			if line, err := process.output.ReadString('\n'); err != nil || line != "provider-started\n" {
				t.Fatal("provider not executing", line, err)
			}
			before := namespaceFixtureMembers(t, process.owner.Namespace)
			if len(before) < 2 {
				t.Fatal("init and original descendant were not observed")
			}
			if err := os.WriteFile(filepath.Join(process.state, "fork"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if line, err := process.output.ReadString('\n'); err != nil || line != "late-descendant-ready\n" {
				t.Fatal("late descendant did not replace original parent", line, err)
			}
			late := namespaceFixtureMembers(t, process.owner.Namespace)
			foundLate := false
			for pid, process := range late {
				_, existed := before[pid]
				foundLate = foundLate || !existed && process.group == pid && process.session == pid
			}
			if !foundLate {
				t.Fatal("late descendant did not leave the original census and session")
			}
			replacement := startNamespaceProcessFixture(t, strings.Repeat("b", 64))
			if mode == "keeper-death" {
				fd, stopped, err := openNamespaceOwner(process.owner)
				if err != nil || stopped {
					t.Fatal("keeper not alive before crash", err)
				}
				err = unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0)
				_ = unix.Close(fd)
				if err != nil {
					t.Fatal(err)
				}
				_ = process.wait()
			}
			reopened, err := readNamespaceLaunch(dataDir, original.Session, original.Identity)
			if err != nil {
				t.Fatal(err)
			}
			retired, err := retireNamespaceLaunch(t.Context(), dataDir, reopened, false)
			if err != nil || retired.Phase != "retired" {
				t.Fatal("cold exact retirement failed", retired, err)
			}
			if remaining := namespaceFixtureMembers(t, process.owner.Namespace); len(remaining) != 0 {
				t.Fatal("retirement acknowledged while descendants survive", remaining)
			}
			fd, stopped, err := openNamespaceOwner(replacement.owner)
			if fd >= 0 {
				_ = unix.Close(fd)
			}
			if err != nil || stopped {
				t.Fatal("exact retirement affected replacement", err)
			}
			repeated, err := retireNamespaceLaunch(t.Context(), dataDir, retired, false)
			if err != nil || repeated.Revision != retired.Revision {
				t.Fatal("repeat retirement changed the receipt", err)
			}
		})
	}
}

func TestNamespaceLaunchProcessCancellationAndLostPermit(t *testing.T) {
	for _, mode := range []string{"cancel-wins", "lost-permit", "wrong-identity", "missing-owner"} {
		t.Run(mode, func(t *testing.T) {
			dataDir := t.TempDir()
			record, err := prepareNamespaceLaunch(t.Context(), dataDir, "session-a", strings.Repeat("c", 64))
			if err != nil {
				t.Fatal(err)
			}
			process := startNamespaceProcessFixture(t, record.Identity)
			record, err = publishNamespaceLaunch(t.Context(), dataDir, record, process.owner)
			if err != nil {
				t.Fatal(err)
			}
			stale := record
			switch mode {
			case "cancel-wins":
				record, err = retireNamespaceLaunch(t.Context(), dataDir, record, true)
				if err != nil {
					t.Fatal(err)
				}
			case "lost-permit":
				_ = process.permit.Close()
			case "wrong-identity":
				record.Owner.Namespace++
				if err := writeNamespaceLaunch(dataDir, record); err != nil {
					t.Fatal(err)
				}
			case "missing-owner":
				path, err := namespaceLaunchPath(dataDir, record.Session, record.Identity)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(path, path+".interrupted"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := grantNamespaceLaunch(t.Context(), dataDir, stale, process.permit); err == nil {
				t.Fatal("unsafe or stale admission granted")
			}
			_ = process.wait()
			if _, err := os.Stat(filepath.Join(process.state, "parent-ready")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("denied launch still executed provider code", err)
			}
			if mode == "cancel-wins" {
				reopened, err := readNamespaceLaunch(dataDir, record.Session, record.Identity)
				if err != nil || reopened.Phase != "retired" || reopened.Revision != record.Revision {
					t.Fatal("stale grant replaced winning cancellation", reopened, err)
				}
			}
		})
	}
}

func TestNamespaceLaunchRejectsAnotherInitPermitPipe(t *testing.T) {
	dataDir := t.TempDir()
	record, err := prepareNamespaceLaunch(t.Context(), dataDir, "session-a", strings.Repeat("d", 64))
	if err != nil {
		t.Fatal(err)
	}
	original := startNamespaceProcessFixture(t, record.Identity)
	replacement := startNamespaceProcessFixture(t, strings.Repeat("e", 64))
	record, err = publishNamespaceLaunch(t.Context(), dataDir, record, replacement.owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := grantNamespaceLaunch(t.Context(), dataDir, record, original.permit); err == nil {
		t.Fatal("permit for A was sent while durable ownership named replacement B")
	}
	reopened, err := readNamespaceLaunch(dataDir, record.Session, record.Identity)
	if err != nil || reopened.Phase != "ready" || reopened.Revision != record.Revision {
		t.Fatal("wrong-init pipe advanced durable execution state", reopened, err)
	}
}

type namespaceHostSnapshot struct {
	Owner   namespaceOwner
	Members map[int]providerProc
}

func assertNamespaceFixtureMembersStopped(t *testing.T, members map[int]providerProc) {
	t.Helper()
	for pid, expected := range members {
		current, err := readProviderProc(pid)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if current.providerProcessIdentity == expected.providerProcessIdentity && current.state != "Z" && current.state != "X" {
			t.Fatal("retirement acknowledged while an original descendant survives", current)
		}
	}
}

func runNamespaceHostFixture(t *testing.T) {
	t.Helper()
	dataDir := os.Getenv("AO_NAMESPACE_JOURNAL")
	phase := os.Getenv("AO_NAMESPACE_CUT")
	record, err := prepareNamespaceLaunch(t.Context(), dataDir, "session-a", strings.Repeat("f", 64))
	if err != nil {
		t.Fatal(err)
	}
	process := startNamespaceProcessFixtureInState(t, record.Identity, os.Getenv("AO_NAMESPACE_STATE"))
	snapshot := namespaceHostSnapshot{Owner: process.owner, Members: namespaceFixtureMembers(t, process.owner.Namespace)}
	if phase != "prepared" {
		record, err = publishNamespaceLaunch(t.Context(), dataDir, record, process.owner)
		if err != nil {
			t.Fatal(err)
		}
	}
	if phase == "permitted-before-write" {
		record.Phase, record.Revision = "permitted", 3
		if err := writeNamespaceLaunch(dataDir, record); err != nil {
			t.Fatal(err)
		}
	}
	if phase == "active" || phase == "retiring" || phase == "stopped-before-receipt" {
		record, err = grantNamespaceLaunch(t.Context(), dataDir, record, process.permit)
		if err != nil {
			t.Fatal(err)
		}
		if line, err := process.output.ReadString('\n'); err != nil || line != "provider-started\n" {
			t.Fatal("host fixture provider did not start", line, err)
		}
		if err := os.WriteFile(filepath.Join(process.state, "fork"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if line, err := process.output.ReadString('\n'); err != nil || line != "late-descendant-ready\n" {
			t.Fatal("host fixture late child did not survive its parent", line, err)
		}
		snapshot.Members = namespaceFixtureMembers(t, process.owner.Namespace)
		if phase != "active" {
			record.Phase, record.Revision = "retiring", 4
			if err := writeNamespaceLaunch(dataDir, record); err != nil {
				t.Fatal(err)
			}
		}
		if phase == "stopped-before-receipt" {
			if err := stopNamespaceOwner(t.Context(), process.owner); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(snapshot); err != nil {
		t.Fatal(err)
	}
	var input [1]byte
	_, _ = io.ReadFull(os.Stdin, input[:])
}

func TestNamespaceLaunchProcessHostCrashReopen(t *testing.T) {
	for _, phase := range []string{"prepared", "ready", "permitted-before-write", "active", "retiring", "stopped-before-receipt"} {
		t.Run(phase, func(t *testing.T) {
			startNamespaceNativeControl(t)
			dataDir, state := t.TempDir(), t.TempDir()
			ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 20*time.Second)
			t.Cleanup(cancel)
			host := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNamespaceProcessFixture$")
			host.Env = []string{"AO_NAMESPACE_FIXTURE=host", "AO_NAMESPACE_JOURNAL=" + dataDir, "AO_NAMESPACE_STATE=" + state,
				"AO_NAMESPACE_CUT=" + phase, "GORACE=atexit_sleep_ms=0"}
			input, err := host.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			output, err := host.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var diagnostics bytes.Buffer
			host.Stderr = &diagnostics
			if err := host.Start(); err != nil {
				t.Fatal(err)
			}
			var once sync.Once
			var waitErr error
			wait := func() error { once.Do(func() { waitErr = host.Wait() }); return waitErr }
			t.Cleanup(func() { _ = input.Close(); _ = host.Process.Kill(); _ = wait() })
			var snapshot namespaceHostSnapshot
			if err := json.NewDecoder(output).Decode(&snapshot); err != nil {
				_ = host.Process.Kill()
				_ = wait()
				t.Fatal("host cut not reached", err, diagnostics.String())
			}
			replacement := startNamespaceProcessFixture(t, strings.Repeat("b", 64))
			if err := host.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if err := wait(); err == nil {
				t.Fatal("host did not die at the requested cut")
			}
			deadline := time.Now().Add(3 * time.Second)
			var retired namespaceLaunch
			for {
				reopened, err := readNamespaceLaunch(dataDir, "session-a", strings.Repeat("f", 64))
				if err != nil {
					t.Fatal("host crash lost durable state", err)
				}
				retired, err = retireNamespaceLaunch(t.Context(), dataDir, reopened, false)
				if err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("host crash recovery did not retire", retired, err)
				}
				time.Sleep(time.Millisecond)
			}
			if retired.Phase != "retired" && retired.Phase != "cancelled" {
				t.Fatal("host crash recovery did not persist its outcome", retired)
			}
			if phase != "prepared" {
				assertNamespaceFixtureMembersStopped(t, snapshot.Members)
			}
			if phase == "prepared" || phase == "ready" || phase == "permitted-before-write" {
				if _, err := os.Stat(filepath.Join(state, "parent-ready")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("crash before permit delivery executed provider code", err)
				}
			}
			fd, stopped, err := openNamespaceOwner(replacement.owner)
			if fd >= 0 {
				_ = unix.Close(fd)
			}
			if err != nil || stopped {
				t.Fatal("crashed-host recovery affected replacement", err)
			}
		})
	}
}

func TestNamespaceLaunchProcessPermitCommitFailure(t *testing.T) {
	for _, cut := range []string{"before-rename", "after-rename"} {
		t.Run(cut, func(t *testing.T) {
			dataDir := t.TempDir()
			record, err := prepareNamespaceLaunch(t.Context(), dataDir, "session-a", strings.Repeat("a", 64))
			if err != nil {
				t.Fatal(err)
			}
			process := startNamespaceProcessFixture(t, record.Identity)
			record, err = publishNamespaceLaunch(t.Context(), dataDir, record, process.owner)
			if err != nil {
				t.Fatal(err)
			}
			interrupted := errors.New("fixture interrupted journal commit")
			barrier := func(step string, record namespaceLaunch) error {
				if step == cut && record.Phase == "permitted" {
					return interrupted
				}
				return nil
			}
			namespaceCommitBarrierForTest.Store(&barrier)
			t.Cleanup(func() { namespaceCommitBarrierForTest.Store(nil) })
			if _, err := grantNamespaceLaunch(t.Context(), dataDir, record, process.permit); !errors.Is(err, interrupted) {
				t.Fatal("commit failure not exercised", err)
			}
			namespaceCommitBarrierForTest.Store(nil)
			_ = process.wait()
			if _, err := os.Stat(filepath.Join(process.state, "parent-ready")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("uncertain durability still executed provider code", err)
			}
			reopened, err := readNamespaceLaunch(dataDir, record.Session, record.Identity)
			if err != nil {
				t.Fatal(err)
			}
			want := "ready"
			if cut == "after-rename" {
				want = "permitted"
			}
			if reopened.Phase != want {
				t.Fatal("unexpected interrupted phase", reopened.Phase)
			}
			replacement := startNamespaceProcessFixture(t, record.Identity)
			if _, err := grantNamespaceLaunch(t.Context(), dataDir, reopened, replacement.permit); err == nil {
				t.Fatal("uncertain original permit was replayed into a replacement")
			}
			_ = replacement.wait()
			if _, err := os.Stat(filepath.Join(replacement.state, "parent-ready")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("replacement reused old execution admission", err)
			}
		})
	}
}

func TestNamespaceLaunchProcessCancellationCommitFailure(t *testing.T) {
	for _, cut := range []string{"before-rename", "after-rename"} {
		t.Run(cut, func(t *testing.T) {
			dataDir := t.TempDir()
			record, err := prepareNamespaceLaunch(t.Context(), dataDir, "session-a", strings.Repeat("b", 64))
			if err != nil {
				t.Fatal(err)
			}
			process := startNamespaceProcessFixture(t, record.Identity)
			record, err = publishNamespaceLaunch(t.Context(), dataDir, record, process.owner)
			if err != nil {
				t.Fatal(err)
			}
			interrupted := errors.New("fixture interrupted cancellation response")
			barrier := func(step string, record namespaceLaunch) error {
				if step == cut && record.Phase == "retiring" {
					return interrupted
				}
				return nil
			}
			namespaceCommitBarrierForTest.Store(&barrier)
			t.Cleanup(func() { namespaceCommitBarrierForTest.Store(nil) })
			if _, err := retireNamespaceLaunch(t.Context(), dataDir, record, true); !errors.Is(err, interrupted) {
				t.Fatal("cancellation cut not exercised", err)
			}
			namespaceCommitBarrierForTest.Store(nil)
			fd, stopped, err := openNamespaceOwner(process.owner)
			if fd >= 0 {
				_ = unix.Close(fd)
			}
			if err != nil || stopped {
				t.Fatal("failed cancellation commit performed teardown", err)
			}
			if cut == "after-rename" {
				if _, err := grantNamespaceLaunch(t.Context(), dataDir, record, process.permit); err == nil {
					t.Fatal("stale admission survived a committed fence with a lost response")
				}
			}
			reopened, err := readNamespaceLaunch(dataDir, record.Session, record.Identity)
			if err != nil {
				t.Fatal(err)
			}
			retired, err := retireNamespaceLaunch(t.Context(), dataDir, reopened, false)
			if err != nil || retired.Phase != "retired" {
				t.Fatal("interrupted cancellation did not recover", retired, err)
			}
			if _, err := os.Stat(filepath.Join(process.state, "parent-ready")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("cancelled provider executed", err)
			}
		})
	}
}

func TestNamespaceOwnerRejectsUncontainedAndChangedIdentity(t *testing.T) {
	if _, err := captureNamespaceOwner(os.Getpid()); err == nil {
		t.Fatal("ordinary native process certified as a namespace init")
	}
	process := startNamespaceProcessFixture(t, strings.Repeat("c", 64))
	for _, edit := range []func(*namespaceOwner){
		func(owner *namespaceOwner) { owner.Start++ },
		func(owner *namespaceOwner) { owner.Namespace++ },
		func(owner *namespaceOwner) { owner.Boot = "" },
		func(owner *namespaceOwner) { owner.PID = 1 },
	} {
		wrong := process.owner
		edit(&wrong)
		if err := stopNamespaceOwner(t.Context(), wrong); err == nil {
			t.Fatal("uncertain identity accepted for retirement")
		}
		fd, stopped, err := openNamespaceOwner(process.owner)
		if fd >= 0 {
			_ = unix.Close(fd)
		}
		if err != nil || stopped {
			t.Fatal("mismatched identity signalled replacement", err)
		}
	}
}

func TestNamespaceOwnerPersistsKernelProcessIdentity(t *testing.T) {
	process := startNamespaceProcessFixture(t, strings.Repeat("a", 64))
	raw, err := json.Marshal(process.owner)
	if err != nil {
		t.Fatal(err)
	}
	var proof map[string]json.RawMessage
	if err := json.Unmarshal(raw, &proof); err != nil {
		t.Fatal(err)
	}
	var identity uint64
	if err := json.Unmarshal(proof["process"], &identity); err != nil || identity <= 1 {
		t.Fatal("durable proof lacks a stable kernel process identity; namespace inode and PID can be reused")
	}
	reopened, err := captureNamespaceOwner(process.owner.PID)
	if err != nil || reopened != process.owner {
		t.Fatal("kernel process identity changed after closing and reopening descriptors", reopened, err)
	}
	replacement := startNamespaceProcessFixture(t, strings.Repeat("b", 64))
	if replacement.owner.Process == process.owner.Process {
		t.Fatal("kernel identity collided across live launches")
	}
	if err := stopNamespaceOwner(t.Context(), process.owner); err != nil {
		t.Fatal(err)
	}
	_ = process.wait()
	reused := process.owner
	reused.PID, reused.Start, reused.Namespace = replacement.owner.PID, replacement.owner.Start, replacement.owner.Namespace
	if err := stopNamespaceOwner(t.Context(), reused); err != nil {
		t.Fatal("retired kernel owner could not reconcile reused coordinates", err)
	}
	fd, stopped, err := openNamespaceOwner(replacement.owner)
	if fd >= 0 {
		_ = unix.Close(fd)
	}
	if err != nil || stopped {
		t.Fatal("reused coordinates redirected teardown to replacement", err)
	}
}

func TestNamespaceLaunchProcessCancellationWinsWaitingGrant(t *testing.T) {
	dataDir := t.TempDir()
	record, err := prepareNamespaceLaunch(t.Context(), dataDir, "session-a", strings.Repeat("d", 64))
	if err != nil {
		t.Fatal(err)
	}
	process := startNamespaceProcessFixture(t, record.Identity)
	record, err = publishNamespaceLaunch(t.Context(), dataDir, record, process.owner)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	committed, release := make(chan struct{}), make(chan struct{})
	barrier := func(step string, record namespaceLaunch) error {
		if step != "after-rename" || record.Phase != "retiring" {
			return nil
		}
		close(committed)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	namespaceCommitBarrierForTest.Store(&barrier)
	defer namespaceCommitBarrierForTest.Store(nil)
	type outcome struct {
		record namespaceLaunch
		err    error
	}
	cancelled := make(chan outcome, 1)
	go func() {
		record, err := retireNamespaceLaunch(ctx, dataDir, record, true)
		cancelled <- outcome{record, err}
	}()
	select {
	case <-committed:
	case <-ctx.Done():
		<-cancelled
		t.Fatal("cancellation did not reach its durable boundary")
	}
	started, granted := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		_, err := grantNamespaceLaunch(ctx, dataDir, record, process.permit)
		granted <- err
	}()
	<-started
	close(release)
	result := <-cancelled
	if grantErr := <-granted; grantErr == nil || result.err != nil || result.record.Phase != "retired" {
		t.Fatal("waiting grant defeated winning cancellation", grantErr, result)
	}
	reopened, err := readNamespaceLaunch(dataDir, record.Session, record.Identity)
	if err != nil || reopened.Revision != result.record.Revision || reopened.Phase != "retired" {
		t.Fatal("waiting grant rotated the cancelled generation", reopened, err)
	}
	if _, err := os.Stat(filepath.Join(process.state, "parent-ready")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("waiting grant started a cancelled provider", err)
	}
}
