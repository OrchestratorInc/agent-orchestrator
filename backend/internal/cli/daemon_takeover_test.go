package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

func TestDaemonTakeoverSleeper(t *testing.T) {
	if os.Getenv("AO_TEST_DAEMON_TAKEOVER_SLEEPER") != "1" {
		return
	}
	time.Sleep(2 * time.Minute)
	os.Exit(0)
}

func TestDaemonTakeoverChecksSavedOwnerBeforeSignaling(t *testing.T) {
	for _, mode := range []string{"matching", "reused", "missing", "unavailable", "successor"} {
		t.Run(mode, func(t *testing.T) {
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			child := exec.Command(executable, "-test.run=^TestDaemonTakeoverSleeper$")
			child.Env = append(os.Environ(), "AO_TEST_DAEMON_TAKEOVER_SLEEPER=1")
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() {
				_ = child.Wait()
				close(done)
			}()
			t.Cleanup(func() {
				_ = child.Process.Kill()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("sleeper did not exit during cleanup")
				}
			})
			identity := processalive.Identify(context.Background(), child.Process.Pid)
			if identity.StartID == "" {
				t.Fatal("test sleeper has no observable process start identity")
			}
			startID := identity.StartID
			switch mode {
			case "reused":
				startID += "-previous-process"
			case "missing":
				startID = ""
			}
			runFile := filepath.Join(t.TempDir(), "running.json")
			owner := map[string]any{
				"pid": child.Process.Pid, "port": 3001,
				"processStartId": startID, "startedAt": time.Now().UTC(),
			}
			if mode == "missing" {
				delete(owner, "processStartId")
			}
			contents, err := json.Marshal(owner)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(runFile, contents, 0o600); err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(contents)
			if mode == "successor" {
				var successor map[string]any
				if err := json.Unmarshal(contents, &successor); err != nil {
					t.Fatal(err)
				}
				successor["appRunId"] = "successor-launch"
				contents, err = json.Marshal(successor)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(runFile, contents, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			command := NewRootCommand(Deps{Out: io.Discard, Err: io.Discard})
			command.SetArgs([]string{"daemon-takeover", runFile, "--expected-sha256", hex.EncodeToString(digest[:])})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "unavailable" {
				cancel()
			}
			err = command.ExecuteContext(ctx)
			if err != nil && strings.Contains(err.Error(), "unknown command") {
				t.Fatalf("desktop takeover boundary is unavailable: %v", err)
			}
			blocked := mode == "missing" || mode == "unavailable" || mode == "successor"
			if blocked {
				if err == nil {
					t.Fatal("unverifiable owner allowed replacement")
				}
			} else if err != nil {
				t.Fatalf("takeover failed: %v", err)
			}
			if mode == "matching" {
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("matching daemon process survived takeover")
				}
			} else if !processalive.Alive(child.Process.Pid) || processalive.Identify(context.Background(), child.Process.Pid).StartID != identity.StartID {
				t.Fatal("unrelated or unverifiable process was signaled")
			}
			_, statErr := os.Stat(runFile)
			if blocked && statErr != nil {
				t.Fatalf("unverifiable owner metadata was removed: %v", statErr)
			}
			if !blocked && !os.IsNotExist(statErr) {
				t.Fatalf("stale owner metadata remains: %v", statErr)
			}
			if blocked {
				remaining, err := os.ReadFile(runFile)
				if err != nil || string(remaining) != string(contents) {
					t.Fatalf("blocked takeover changed owner metadata: %s; %v", remaining, err)
				}
			}
		})
	}
}

func TestDaemonTakeoverReclaimsGoneOwner(t *testing.T) {
	runFile := filepath.Join(t.TempDir(), "running.json")
	contents := []byte(`{"pid":2147483647,"port":3001}`)
	if err := os.WriteFile(runFile, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(contents)
	command := NewRootCommand(Deps{Out: io.Discard, Err: io.Discard})
	command.SetArgs([]string{"daemon-takeover", runFile, "--expected-sha256", hex.EncodeToString(digest[:])})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(runFile); !os.IsNotExist(err) {
		t.Fatalf("confirmed-gone owner record remains: %v", err)
	}
}
