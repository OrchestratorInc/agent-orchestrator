//go:build windows

package persistenthost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestProviderOwnerWindowsCrashHelper(t *testing.T) {
	dir := os.Getenv("AO_PROVIDER_JOB_CRASH_DIR")
	if dir == "" {
		return
	}
	owner, err := beginProviderOwner(dir, "job-crash", strings.Repeat("9", 64))
	if err != nil {
		t.Fatal(err)
	}
	barrier := func(pid int) {
		process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
		if err != nil {
			t.Fatal(err)
		}
		var created, exited, kernel, user windows.Filetime
		if err := windows.GetProcessTimes(process, &created, &exited, &kernel, &user); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(providerProcessIdentity{PID: pid, Start: uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "created.json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
		os.Exit(0)
	}
	cut := os.Getenv("AO_PROVIDER_JOB_CRASH_CUT")
	if cut == "" || cut == "birth" {
		providerChildBirthForTest.Store(&barrier)
	} else {
		phaseBarrier := func(phase string, pid int) {
			if phase == cut {
				barrier(pid)
			}
		}
		providerChildPhaseForTest.Store(&phaseBarrier)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestProviderHelper$")
	command.Dir, command.Env = dir, append(os.Environ(), "AO_CHAT_HOST_PROVIDER_HELPER=1")
	configureProviderProcess(command)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	command.Stderr = io.Discard
	if _, err := startProviderChild(context.Background(), command, dir, &owner); err != nil {
		t.Fatal(err)
	}
	t.Fatal("crash hook was not reached")
}

func TestProviderOwnerWindowsContainedAtBirth(t *testing.T) {
	assertProviderOwnerWindowsCrashCut(t, "birth")
}

func TestProviderOwnerWindowsPublicationCrashCuts(t *testing.T) {
	for _, cut := range []string{"before-publication", "after-publication", "after-resume"} {
		t.Run(cut, func(t *testing.T) { assertProviderOwnerWindowsCrashCut(t, cut) })
	}
}

func assertProviderOwnerWindowsCrashCut(t *testing.T, cut string) {
	t.Helper()
	replacement, _, _ := startWindowsContainedControl(t)
	unrelated := exec.Command(os.Args[0], "-test.run=^TestProviderHelper$")
	unrelated.Env = append(os.Environ(), "AO_CHAT_HOST_PROVIDER_HELPER=1")
	input, err := unrelated.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if err := unrelated.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unrelated.Process.Kill(); _ = unrelated.Wait() })
	defer func() {
		if state, err := windows.WaitForSingleObject(replacement.process, 0); err != nil || state != uint32(windows.WAIT_TIMEOUT) {
			t.Error("crash recovery stopped the replacement", state, err)
		}
		var state uint32
		var probeErr error
		err := unrelated.Process.WithHandle(func(handle uintptr) { state, probeErr = windows.WaitForSingleObject(windows.Handle(handle), 0) })
		if err != nil || probeErr != nil || state != uint32(windows.WAIT_TIMEOUT) {
			t.Error("crash recovery stopped the unrelated process", state, err, probeErr)
		}
	}()
	dir := t.TempDir()
	command := exec.Command(os.Args[0], "-test.run=^TestProviderOwnerWindowsCrashHelper$")
	command.Env = append(os.Environ(), "AO_PROVIDER_JOB_CRASH_DIR="+dir, "AO_PROVIDER_JOB_CRASH_CUT="+cut)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("crash helper did not reach the birth cut: %v: %s", err, output)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "created.json"))
	var identity providerProcessIdentity
	if err != nil || json.Unmarshal(raw, &identity) != nil || identity.PID <= 1 || identity.Start == 0 {
		t.Fatal("missing exact child identity at creation cut", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	retireErr := ShutdownExact(ctx, dir, "job-crash", strings.Repeat("9", 64))
	if cut == "birth" || cut == "before-publication" {
		if !errors.Is(retireErr, ErrOwnershipInconclusive) {
			t.Error("starting record authorized retirement", retireErr)
		}
	} else if retireErr != nil {
		proof, readErr := readProviderOwner(dir, "job-crash", strings.Repeat("9", 64))
		t.Logf("recovery proof state=%q kind=%q group=%d members=%v read=%v", proof.State, proof.Proof, proof.Group, proof.Members, readErr)
		job, jobErr := openProviderJob(proof, 0x0004)
		t.Logf("recovery job=%d error=%v", job, jobErr)
		if job != 0 {
			_ = windows.CloseHandle(job)
		}
		t.Error("contained published launch could not recover", retireErr)
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(identity.PID))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(process)
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(process, &created, &exited, &kernel, &user); err != nil {
		t.Fatal(err)
	}
	if uint64(created.HighDateTime)<<32|uint64(created.LowDateTime) != identity.Start {
		return
	}
	defer windows.TerminateProcess(process, 1)
	state, err := windows.WaitForSingleObject(process, 1000)
	if err != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatal("host death left an uncontained suspended child alive", state, err)
	}
}

func startWindowsContainedControl(t *testing.T) (*providerChild, string, providerOwner) {
	t.Helper()
	dir := t.TempDir()
	token, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	owner, err := beginProviderOwner(dir, "job-replacement", descriptorIdentity(Descriptor{Token: token}))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestProviderHelper$")
	command.Dir, command.Env = dir, append(os.Environ(), "AO_CHAT_HOST_PROVIDER_HELPER=1")
	configureProviderProcess(command)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close() })
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdout.Close() })
	child, err := startProviderChild(t.Context(), command, dir, &owner)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.stop(context.Background()); _ = child.wait(); child.close() })
	return child, dir, owner
}

func TestProviderOwnerWindowsJobProof(t *testing.T) {
	child, _, _ := startWindowsContainedControl(t)
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	err := windows.QueryInformationJobObject(child.job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)), nil)
	if err != nil || limits.BasicLimitInformation.LimitFlags&windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE == 0 {
		t.Fatalf("owned job lost kill-on-close proof: flags=%x err=%v", limits.BasicLimitInformation.LimitFlags, err)
	}
	if empty, err := providerJobEmpty(child.job); err != nil || empty {
		t.Fatal("running job reported empty", empty, err)
	}
}

func TestProviderOwnerWindowsCollisionPreservesOwner(t *testing.T) {
	child, dir, owner := startWindowsContainedControl(t)
	command := exec.Command(os.Args[0], "-test.run=^TestProviderHelper$")
	command.Dir = dir
	if replacement, err := startProviderChild(t.Context(), command, dir, &owner); !errors.Is(err, ErrOwnershipInconclusive) || replacement != nil {
		t.Fatal("existing job admitted a replacement", replacement, err)
	}
	if state, err := windows.WaitForSingleObject(child.process, 0); err != nil || state != uint32(windows.WAIT_TIMEOUT) {
		t.Fatal("collision disturbed the original owner", state, err)
	}
	stored, err := readProviderOwner(dir, owner.Session, owner.Identity)
	if err != nil || stored.State != "active" || stored.Group != owner.Group {
		t.Fatal("collision changed the active receipt", err)
	}
}

func TestProviderOwnerWindowsRejectsWrappedPID(t *testing.T) {
	child, _, owner := startWindowsContainedControl(t)
	if err := confirmProviderJobLeader(child.job, owner); err != nil {
		t.Error("same-owner positive control could not establish ownership", err)
	}
	owner.Group += 1 << 32
	owner.Members[0].PID = owner.Group
	if err := confirmProviderJobLeader(child.job, owner); !errors.Is(err, ErrOwnershipInconclusive) {
		t.Fatal("wrapped PID authorized an ownership probe", err)
	}
	if state, err := windows.WaitForSingleObject(child.process, 0); err != nil || state != uint32(windows.WAIT_TIMEOUT) {
		t.Fatal("invalid proof disturbed the original owner", state, err)
	}
}
