package docker

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox/cleanuphook"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox/cleanuphook/cleanuphooktest"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

const testAPIVersion = "1.44"

// fakeEngine is a Docker Engine serving one managed worker container and its
// workspace volume, with docker exec.
type fakeEngine struct {
	t         *testing.T
	mu        sync.Mutex
	id        string
	running   bool
	container bool
	volume    bool
	calls     []string
	execBody  map[string]any
	// output answers an exec, or outputFunc with the exec's command;
	// startStatus, when set, fails the exec start with it.
	output      string
	outputFunc  func(command string) string
	startStatus int
	// block, when set, holds every exec start until it is closed.
	block chan struct{}
}

func (f *fakeEngine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/v"+testAPIVersion)
	f.calls = append(f.calls, r.Method+" "+path)
	var body map[string]any
	if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
		_ = json.Unmarshal(raw, &body)
	}
	switch {
	case r.Method == http.MethodGet && path == "/containers/"+f.id+"/json":
		if !f.container {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		status := "exited"
		if f.running {
			status = "running"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Id": f.id, "Name": "/ao-test-s1",
			"State": map[string]any{"Status": status, "Running": f.running},
			"Config": map[string]any{"Labels": map[string]string{
				labelManaged: "true", labelProvider: sandbox.ProviderDocker, labelNamespace: "test",
				labelSessionID: "s1", labelWorkspace: "ao-test-workspace-s1",
			}},
		})
	case r.Method == http.MethodPost && path == "/containers/"+f.id+"/exec":
		f.execBody = body
		_ = json.NewEncoder(w).Encode(map[string]string{"Id": "exec-1"})
	case r.Method == http.MethodPost && path == "/exec/exec-1/start":
		if f.block != nil {
			f.mu.Unlock()
			select {
			case <-f.block:
			case <-r.Context().Done():
			}
			f.mu.Lock()
		}
		if f.startStatus != 0 {
			w.WriteHeader(f.startStatus)
			_, _ = io.WriteString(w, `{"message":"exec failed"}`)
			return
		}
		output := f.output
		if f.outputFunc != nil {
			command, _ := f.execBody["Cmd"].([]any)
			output = f.outputFunc(fmt.Sprint(command[len(command)-1]))
		}
		w.Header().Set("Content-Type", "application/vnd.docker.multiplexed-stream")
		// Split across a stdout and a stderr frame, as a real exec interleaves them.
		half := len(output) / 2
		writeFrame(w, 1, output[:half])
		writeFrame(w, 2, output[half:])
	case r.Method == http.MethodDelete && path == "/containers/"+f.id:
		f.container = false
	case r.Method == http.MethodDelete && path == "/volumes/ao-test-workspace-s1":
		f.volume = false
	default:
		f.t.Errorf("unexpected docker call %s %s", r.Method, path)
		w.WriteHeader(http.StatusNotImplemented)
	}
}

func writeFrame(w io.Writer, stream byte, data string) {
	header := make([]byte, 8)
	header[0] = stream
	binary.BigEndian.PutUint32(header[4:], uint32(len(data)))
	_, _ = w.Write(header)
	_, _ = io.WriteString(w, data)
}

func (f *fakeEngine) snapshot() (calls []string, execs int, removed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if strings.HasSuffix(call, "/exec") {
			execs++
		}
	}
	return append([]string(nil), f.calls...), execs, !f.container && !f.volume
}

// containerSequence makes every test container's id unique: Cleanup runs are
// remembered per container id across clients.
var containerSequence atomic.Int64

func newCleanupEngine(t *testing.T, running bool) (*fakeEngine, *Client) {
	t.Helper()
	engine := &fakeEngine{
		t: t, id: fmt.Sprintf("ctr-cleanup-%d", containerSequence.Add(1)),
		running: running, container: true, volume: true,
		output: "\n" + cleanuphook.Marker + ":0\n",
	}
	server := httptest.NewServer(engine)
	t.Cleanup(server.Close)
	client, err := New(Config{
		Host: server.URL, WorkerImage: "ao-cloud-worker:test", Namespace: "test",
		HTTPClient: server.Client(), APIVersion: testAPIVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	client.cleanupInline = 5 * time.Second
	return engine, client
}

// Delete runs the project's current Cleanup in the running worker container
// with docker exec, as the container's own user, under the budget, appends
// each step to the session, and only then removes the container and volume.
func TestDeleteRunsCleanupInTheContainerBeforeRemovingIt(t *testing.T) {
	t.Parallel()
	engine, client := newCleanupEngine(t, true)
	commands := []string{"make down"}
	var script string
	engine.outputFunc = func(command string) string {
		var nonce string
		script, nonce = cleanuphooktest.Script(t, command)
		marker := "@@ao-step-" + nonce + " "
		return marker + "1 1 start 1792344586000 / make down\nstopped\n" + marker + "1 1 end 0 40\n\n" + cleanuphook.Marker + ":0\n"
	}
	recorder := &cleanuphooktest.Recorder{}
	ctx := recorder.Context(sandbox.WithCleanupCommands(context.Background(), commands))
	if err := client.Delete(ctx, sandbox.ID(engine.id)); err != nil {
		t.Fatalf("delete: %v", err)
	}
	calls, _, removed := engine.snapshot()
	want := fmt.Sprintf("GET /containers/%[1]s/json, POST /containers/%[1]s/exec, POST /exec/exec-1/start, "+
		"DELETE /containers/%[1]s, DELETE /volumes/ao-test-workspace-s1", engine.id)
	if got := strings.Join(calls, ", "); got != want || !removed {
		t.Fatalf("calls = %s; want %s", got, want)
	}
	cmd, _ := engine.execBody["Cmd"].([]any)
	if _, user := engine.execBody["User"]; user || len(cmd) != 3 || cmd[0] != "sh" || cmd[1] != "-c" {
		t.Fatalf("exec = %v; want sh -c as the container's user", engine.execBody)
	}
	command := fmt.Sprint(cmd[2])
	for _, want := range []string{
		"ao_hook='/workspace/.ao/worker/cleanup-hook.sh'",
		`elif ! [ -O "$ao_hook" ]; then ao_skip hook_owner`,
		`if [ -L "$ao_hook" ]; then ao_skip hook_link; fi`,
		// The run's budget: its step's limit and the step grace.
		"timeout -k 5 610 ",
		// The worker's agent is stopped before the hook runs.
		`ao_stop "$ao_who" TERM`,
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("hook command missing %q:\n%s", want, command)
		}
	}
	if !strings.Contains(script, "sh -c 'make down'") {
		t.Fatalf("the exec does not carry the current Cleanup: %q", script)
	}
	events := recorder.Events(t)
	if len(events) != 1 || !worker.ValidScriptStepEvent(events[0]) || events[0].Phase != worker.ScriptPhaseCleanup ||
		events[0].Status != worker.SetupSucceeded || events[0].Output != "stopped" || events[0].Command != "make down" {
		t.Fatalf("events = %+v", events)
	}
	if stage, ok := recorder.Stage(); !ok || stage.Err != nil {
		t.Fatalf("stage = %+v", stage)
	}
}

// No exec runs for a project without Cleanup now, a Delete that skips
// Cleanup, or a stopped worker; each still removes the worker.
func TestDeleteWithoutCleanupNeverExecs(t *testing.T) {
	t.Parallel()
	for name, running := range map[string]bool{"no Cleanup now": true, "without Cleanup": true, "stopped": false} {
		engine, client := newCleanupEngine(t, running)
		ctx := context.Background()
		switch name {
		case "no Cleanup now":
			ctx = sandbox.WithCleanupCommands(ctx, nil)
		case "without Cleanup":
			ctx = sandbox.WithoutCleanup(ctx)
		}
		if err := client.Delete(ctx, sandbox.ID(engine.id)); err != nil {
			t.Fatalf("%s: delete: %v", name, err)
		}
		if calls, execs, removed := engine.snapshot(); execs != 0 || !removed {
			t.Fatalf("%s: calls = %v", name, calls)
		}
	}
}

// A failed Cleanup, one its budget stopped, and an exec that failed outright
// are reported, and the worker is removed all the same.
func TestDeleteReportsAFailedCleanupAndStillRemoves(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		output      string
		startStatus int
		check       func(sandbox.StageReport, []worker.ScriptStepEvent) bool
	}{
		"failed": {
			output: "tearing down\ncleanup step 1 failed: exit status 4\n\n" + cleanuphook.Marker + ":4\n",
			check: func(stage sandbox.StageReport, events []worker.ScriptStepEvent) bool {
				return stage.Err != nil && strings.Contains(stage.Attrs["output"], "tearing down") &&
					len(events) == 1 && events[0].Status == worker.SetupFailed
			},
		},
		"timed out": {
			output: "@@ao-step-00000000000000aa 1 1 start 1792344586000 / make down\nwaiting\n\n" + cleanuphook.Marker + ":124\n",
			check: func(stage sandbox.StageReport, events []worker.ScriptStepEvent) bool {
				return stage.Err != nil && len(events) == 1 && events[0].Status == worker.SetupTimedOut
			},
		},
		"exec failed": {
			startStatus: http.StatusInternalServerError,
			check: func(stage sandbox.StageReport, events []worker.ScriptStepEvent) bool {
				return stage.Err != nil && strings.Contains(stage.Err.Error(), "did not report") && len(events) == 0
			},
		},
		"no hook": {
			output: "\n" + cleanuphook.Marker + ":" + cleanuphook.Skipped + "\n",
			check: func(stage sandbox.StageReport, events []worker.ScriptStepEvent) bool {
				return stage.Err == nil && stage.Attrs["result"] == cleanuphook.Skipped && len(events) == 0
			},
		},
	}
	for name, test := range cases {
		engine, client := newCleanupEngine(t, true)
		engine.output, engine.startStatus = test.output, test.startStatus
		recorder := &cleanuphooktest.Recorder{}
		if err := client.Delete(recorder.Context(context.Background()), sandbox.ID(engine.id)); err != nil {
			t.Fatalf("%s: delete: %v", name, err)
		}
		stage, _ := recorder.Stage()
		if events := recorder.Events(t); !test.check(stage, events) {
			t.Fatalf("%s: stage = %+v, events = %+v", name, stage, events)
		}
		if calls, _, removed := engine.snapshot(); !removed {
			t.Fatalf("%s: not removed: %v", name, calls)
		}
	}
}

// A Cleanup still running hands the worker back to the reconciler instead of
// holding the Delete; re-issued Deletes wait for the same run.
func TestDeleteWaitsForARunningCleanupWithoutHoldingTheCaller(t *testing.T) {
	t.Parallel()
	engine, client := newCleanupEngine(t, true)
	client.cleanupInline = 50 * time.Millisecond
	engine.block = make(chan struct{})
	recorder := &cleanuphooktest.Recorder{}
	ctx := recorder.Context(context.Background())
	for range 2 {
		if err := client.Delete(ctx, sandbox.ID(engine.id)); err != nil {
			t.Fatalf("delete: %v", err)
		}
	}
	if _, _, removed := engine.snapshot(); removed {
		t.Fatal("removed while Cleanup ran")
	}
	close(engine.block)
	recorder.WaitStage(t)
	if err := client.Delete(ctx, sandbox.ID(engine.id)); err != nil {
		t.Fatalf("delete after cleanup: %v", err)
	}
	if calls, execs, removed := engine.snapshot(); execs != 1 || !removed {
		t.Fatalf("calls = %v", calls)
	}
}

func TestDemuxTailJoinsFramesAndKeepsTheTail(t *testing.T) {
	t.Parallel()
	var stream strings.Builder
	writeFrame(&stream, 1, "abc")
	writeFrame(&stream, 2, "def")
	if output, err := demuxTail(strings.NewReader(stream.String()), 100); err != nil || output != "abcdef" {
		t.Fatalf("output = %q, %v", output, err)
	}
	stream.Reset()
	for range 10 {
		writeFrame(&stream, 1, "0123456789")
	}
	output, err := demuxTail(strings.NewReader(stream.String()), 15)
	if err != nil || len(output) < 15 || len(output) > 30 || !strings.HasSuffix(output, "0123456789") {
		t.Fatalf("tail = %q, %v", output, err)
	}
	if _, err := demuxTail(strings.NewReader(stream.String()[:20]), 100); err == nil {
		t.Fatal("a truncated frame read as complete")
	}
}

// A stopped worker runs no Cleanup, but the session still shows each of the
// project's Cleanup steps as skipped, saying the sandbox was not running.
func TestDeleteOfAStoppedWorkerShowsItsCleanupSkipped(t *testing.T) {
	t.Parallel()
	engine, client := newCleanupEngine(t, false)
	recorder := &cleanuphooktest.Recorder{}
	commands := []string{"make down"}
	ctx := sandbox.WithCleanupCommands(recorder.Context(context.Background()), commands)
	if err := client.Delete(ctx, sandbox.ID(engine.id)); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if calls, execs, removed := engine.snapshot(); execs != 0 || !removed {
		t.Fatalf("calls = %v", calls)
	}
	stage, ok := recorder.Stage()
	events := recorder.Events(t)
	if !ok || stage.Err != nil || stage.Attrs["result"] != cleanuphook.Skipped || len(events) != 1 ||
		events[0].Status != worker.ScriptStepSkipped || events[0].Hint != cleanuphook.NotRunningReason {
		t.Fatalf("stage = %+v, events = %+v", stage, events)
	}
}
