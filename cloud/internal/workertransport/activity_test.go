package workertransport

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeProc writes a minimal /proc: system CPU (two cores), this process's own
// CPU, and processes with the given parents.
type fakeProc struct {
	t    *testing.T
	root string
	// cgroup is a cgroup mount that holds nothing until setCgroupV2 or
	// setCgroupV1 writes it, so /proc/stat is used by default.
	cgroup string
}

func newFakeProc(t *testing.T) *fakeProc {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "self"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &fakeProc{t: t, root: root, cgroup: filepath.Join(root, "cgroup")}
}

// setCgroupV2 writes the sandbox cgroup's cumulative CPU time (cgroup v2).
func (f *fakeProc) setCgroupV2(usage time.Duration) {
	f.t.Helper()
	if err := os.MkdirAll(f.cgroup, 0o755); err != nil {
		f.t.Fatal(err)
	}
	stat := "usage_usec " + itoa(uint64(usage/time.Microsecond)) + "\nuser_usec 0\nsystem_usec 0\n"
	if err := os.WriteFile(filepath.Join(f.cgroup, "cpu.stat"), []byte(stat), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// setCgroupV1 writes the sandbox cgroup's cumulative CPU time (cgroup v1).
func (f *fakeProc) setCgroupV1(usage time.Duration) {
	f.t.Helper()
	dir := filepath.Join(f.cgroup, "cpu,cpuacct")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cpuacct.usage"), []byte(itoa(uint64(usage))+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// setCPU writes cumulative ticks: busy work, idle, and the worker's own.
func (f *fakeProc) setCPU(busy, idle, self uint64) {
	f.t.Helper()
	stat := "cpu  " + itoa(busy) + " 0 0 " + itoa(idle) + " 0 0 0 0 0 0\ncpu0 0 0 0 0\ncpu1 0 0 0 0\n"
	if err := os.WriteFile(filepath.Join(f.root, "stat"), []byte(stat), 0o644); err != nil {
		f.t.Fatal(err)
	}
	selfStat := "1 (ao worker) S 0 0 0 0 0 0 0 0 0 0 " + itoa(self) + " 0 0 0\n"
	if err := os.WriteFile(filepath.Join(f.root, "self", "stat"), []byte(selfStat), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fakeProc) addProcess(pid, parent int) {
	f.t.Helper()
	dir := filepath.Join(f.root, itoa(uint64(pid)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	stat := itoa(uint64(pid)) + " (npm run dev) S " + itoa(uint64(parent)) + " 0 0\n"
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func itoa(value uint64) string { return string(appendUint(nil, value)) }

func appendUint(buffer []byte, value uint64) []byte {
	if value >= 10 {
		buffer = appendUint(buffer, value/10)
	}
	return append(buffer, byte('0'+value%10))
}

func TestQuietSandboxIsIdle(t *testing.T) {
	proc := newFakeProc(t)
	a := &activity{procRoot: proc.root, cgroupRoot: proc.cgroup}
	now := time.Now()
	proc.setCPU(100, 10_000, 50)
	a.busySince(now, nil) // baseline sample
	// 20s later: 2 cores x 2000 ticks elapsed, only 5 ticks of non-worker work.
	proc.setCPU(105, 13_995, 50)
	if a.busySince(now.Add(20*time.Second), nil) {
		t.Fatal("a quiet sandbox was reported busy")
	}
}

func TestCPUWorkKeepsTheSandboxBusyButTheWorkersOwnDoesNot(t *testing.T) {
	proc := newFakeProc(t)
	a := &activity{procRoot: proc.root, cgroupRoot: proc.cgroup}
	now := time.Now()
	proc.setCPU(100, 10_000, 50)
	a.busySince(now, nil)
	// The worker itself spent 300 ticks streaming: not activity.
	proc.setCPU(400, 13_700, 350)
	if a.busySince(now.Add(20*time.Second), nil) {
		t.Fatal("the worker's own CPU counted as activity")
	}
	// An agent or build used 300 ticks: activity.
	proc.setCPU(700, 17_400, 350)
	if !a.busySince(now.Add(40*time.Second), nil) {
		t.Fatal("CPU work outside the worker was not reported busy")
	}
}

// In a container (a Docker-based Coder workspace) /proc/stat is the host's:
// another tenant's heavy work must not keep this sandbox awake, and the
// sandbox's own cgroup CPU time decides instead.
func TestContainerCPUIsReadFromTheCgroupNotTheHost(t *testing.T) {
	for _, version := range []struct {
		name string
		set  func(*fakeProc, time.Duration)
	}{
		{"cgroup v2", (*fakeProc).setCgroupV2},
		{"cgroup v1", (*fakeProc).setCgroupV1},
	} {
		t.Run(version.name, func(t *testing.T) {
			proc := newFakeProc(t)
			a := &activity{procRoot: proc.root, cgroupRoot: proc.cgroup}
			now := time.Now()
			proc.setCPU(100, 10_000, 50)
			version.set(proc, time.Second)
			a.busySince(now, nil)

			// The host is saturated by other containers; this sandbox used
			// 100ms in 20s, all of it the worker's own 10 ticks.
			proc.setCPU(4_000, 10_100, 60)
			version.set(proc, time.Second+100*time.Millisecond)
			if a.busySince(now.Add(20*time.Second), nil) {
				t.Fatal("host-wide CPU from other tenants kept the container busy")
			}

			// The agent in this sandbox used 2s of CPU in 20s: busy.
			proc.setCPU(8_000, 10_200, 60)
			version.set(proc, 3*time.Second+100*time.Millisecond)
			if !a.busySince(now.Add(40*time.Second), nil) {
				t.Fatal("CPU work in the container's own cgroup was not reported busy")
			}
		})
	}
}

func TestAProcessUnderAWorkspaceShellKeepsTheSandboxBusy(t *testing.T) {
	proc := newFakeProc(t)
	proc.setCPU(100, 10_000, 50)
	a := &activity{procRoot: proc.root, cgroupRoot: proc.cgroup}
	proc.addProcess(4242, 999)
	if a.busySince(time.Now(), []int{1000}) {
		t.Fatal("a process under another parent kept the sandbox busy")
	}
	if !a.busySince(time.Now(), []int{999}) {
		t.Fatal("a dev server under a workspace shell did not keep the sandbox busy")
	}
}

func TestTerminalOutputSinceTheLastCheckIsBusy(t *testing.T) {
	proc := newFakeProc(t)
	proc.setCPU(100, 10_000, 50)
	a := &activity{procRoot: proc.root, cgroupRoot: proc.cgroup}
	start := time.Now()
	a.busySince(start, nil)
	a.noteOutput(start.Add(5 * time.Second))
	if !a.busySince(start.Add(20*time.Second), nil) {
		t.Fatal("output since the last check was not reported busy")
	}
	if a.busySince(start.Add(40*time.Second), nil) {
		t.Fatal("old output kept the sandbox busy")
	}
}
