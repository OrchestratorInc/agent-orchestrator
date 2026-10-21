package local

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestStopDaemonKeepsElectronAndRejectsForeignRunFile(t *testing.T) {
	f := fixture(t)
	result, err := f.a.Control(context.Background(), f.s.target, "stop_daemon")
	if err != nil || result.Detail == "" {
		t.Fatal(result, err)
	}
	if len(f.signals) != 1 || f.signals[0] != 12 || f.pids[11].PID != 11 {
		t.Fatal("signalled outside target daemon", f.signals)
	}
	if err := f.a.Probe(context.Background(), f.s.target); err != nil {
		t.Fatal("down target cannot be observed", err)
	}
	if err := os.WriteFile(filepath.Join(f.s.root, "running.json"), []byte(`{"pid":99,"port":3001,"owner":"app","appRunId":"foreign"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.a.Probe(context.Background(), f.s.target); err == nil {
		t.Fatal("accepted foreign run file")
	}
}

func TestDaemonControlRejectsChangedIdentityAndUnprovedAbsence(t *testing.T) {
	for _, kind := range []string{"reused", "foreign", "unreadable", "listener", "port"} {
		t.Run(kind, func(t *testing.T) {
			f := fixture(t)
			target := f.s.target
			switch kind {
			case "reused":
				f.times[12] = time.Now().Add(time.Hour)
			case "foreign":
				target.DataDir = "/foreign"
			case "unreadable":
				f.a.ops.startTime = func(int) (time.Time, error) { return time.Time{}, syscall.EPERM }
			case "listener":
				f.listener = errors.New("listener remains")
				f.s.daemonDown = true
				delete(f.pids, 12)
			case "port":
				f.s.port = 3001
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := f.a.Control(ctx, target, "stop_daemon"); err == nil {
				t.Fatal("unsafe daemon control succeeded")
			}
			if len(f.signals) != 0 {
				t.Fatal("unsafe daemon was signalled", f.signals)
			}
		})
	}
}

func TestRestartAdmitsOnlyElectronDescendantAndKeepsLaunchIdentity(t *testing.T) {
	f := fixture(t)
	f.s.daemonDown = true
	delete(f.pids, 12)
	old := f.s.target
	f.pids[14] = processInfo{PID: 14, Parent: 11}
	f.times[14] = old.DaemonStartedAt.Add(time.Second)
	f.ready["pid"] = 14
	data, _ := json.Marshal(runfile.Info{PID: 14, Port: 32100, Owner: "app", AppRunID: "target-test", StartedAt: f.times[14]})
	if err := os.WriteFile(filepath.Join(f.s.root, "running.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.a.ready(context.Background(), f.s); err != nil {
		t.Fatal(err)
	}
	if f.s.target != old {
		t.Fatal("changed immutable launch identity")
	}
	f.times[14] = f.times[14].Add(time.Hour)
	if err := f.a.Probe(context.Background(), old); err == nil {
		t.Fatal("accepted reused replacement PID")
	}
}
