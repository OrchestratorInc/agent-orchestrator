package local

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	processutil "github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

func (s *launch) runtimeDaemon() (int, time.Time) {
	if s.currentDaemonPID != 0 {
		return s.currentDaemonPID, s.currentDaemonStartedAt
	}
	return s.target.DaemonPID, s.target.DaemonStartedAt
}

func (s *launch) requiredPID(pid int) bool {
	daemon, _ := s.runtimeDaemon()
	return pid == s.target.ElectronPID || !s.daemonDown && pid == daemon
}

// Control never accepts a process, endpoint or path from the investigator.
func (a *Adapter) Control(ctx context.Context, target domain.TestTargetIdentity, action string) (ports.TestingControlResult, error) {
	s, err := a.find(target)
	if err != nil {
		return ports.TestingControlResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || s.closing {
		return ports.TestingControlResult{}, errors.New("target has stopped")
	}
	if err := a.probe(ctx, s); err != nil {
		return ports.TestingControlResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	switch action {
	case "stop_daemon":
		if s.daemonDown {
			return ports.TestingControlResult{}, errors.New("target daemon is already stopped")
		}
		if s.port == 3001 || !beneath(s.root, s.target.DataDir) {
			return ports.TestingControlResult{}, errors.New("target daemon is not private")
		}
		pid, _ := s.runtimeDaemon()
		if err := a.signalOwned(ctx, s, pid, syscall.SIGTERM); err != nil {
			return ports.TestingControlResult{}, err
		}
		// Retain the explicit down state even if waiting is interrupted. Future
		// observations still need positive process and listener absence.
		s.daemonDown = true
		if err := waitControl(ctx, func() error { return a.probeDaemonDown(ctx, s) }); err != nil {
			return ports.TestingControlResult{}, err
		}
		return ports.TestingControlResult{Detail: "Owned target daemon stopped; Electron remains live"}, nil
	case "restart_daemon":
		if !s.daemonDown {
			return ports.TestingControlResult{}, errors.New("target daemon is already running")
		}
		id, err := randomID()
		if err != nil {
			return ports.TestingControlResult{}, err
		}
		dir := filepath.Join(s.root, "control")
		request, _ := json.Marshal(map[string]string{"id": id, "action": action})
		if err := os.WriteFile(filepath.Join(dir, "request.tmp"), request, 0o600); err != nil {
			return ports.TestingControlResult{}, err
		}
		if err := os.Rename(filepath.Join(dir, "request.tmp"), filepath.Join(dir, "request.json")); err != nil {
			return ports.TestingControlResult{}, err
		}
		err = waitControl(ctx, func() error {
			data, err := os.ReadFile(filepath.Join(dir, id+".json"))
			if err != nil {
				return err
			}
			var reply struct {
				OK bool `json:"ok"`
			}
			if json.Unmarshal(data, &reply) != nil || !reply.OK {
				return errors.New("invalid controller start receipt")
			}
			return nil
		})
		if err != nil {
			return ports.TestingControlResult{}, err
		}
		// ready requires a new daemon descended from the same live Electron,
		// the exact private run file, executable, cwd and data directory.
		if err := a.waitReady(ctx, s, time.Now().Add(30*time.Second)); err != nil {
			return ports.TestingControlResult{}, err
		}
		return ports.TestingControlResult{Detail: "Owned target daemon restarted and passed readiness"}, a.checkLiveDaemon(s.liveGuard)
	default:
		return a.terminalControl(ctx, s, action)
	}
}

func waitControl(ctx context.Context, check func() error) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := check()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), err)
		case <-ticker.C:
		}
	}
}

func (a *Adapter) probeDaemonDown(ctx context.Context, s *launch) error {
	started, err := a.ops.startTime(s.target.ElectronPID)
	if err != nil || !started.Equal(s.target.ElectronStartedAt) {
		return errors.Join(errors.New("target Electron identity changed"), err)
	}
	pid, _ := s.runtimeDaemon()
	_, err = a.ops.startTime(pid)
	if !errors.Is(err, processutil.ErrNotRunning) {
		if !errors.Is(err, syscall.ESRCH) {
			return errors.Join(errors.New("target daemon absence not proved"), err)
		}
		processes, checkErr := a.ops.processes(ctx)
		if checkErr != nil {
			return checkErr
		}
		for _, p := range processes {
			if p.PID == pid {
				return errors.New("target daemon remains in process inventory")
			}
		}
	}
	if err := a.ops.listenerGone(ctx, s.port); err != nil {
		return err
	}
	info, err := runfile.Read(filepath.Join(s.root, "running.json"))
	if err != nil {
		return err
	}
	if info != nil && (info.PID != pid || info.Port != s.port || info.Owner != "app" || info.AppRunID != s.target.LaunchID) {
		return errors.New("foreign target run file while daemon is down")
	}
	return a.captureTree(ctx, s)
}
