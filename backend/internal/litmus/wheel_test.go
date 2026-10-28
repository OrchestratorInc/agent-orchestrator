package litmus

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func wheelSocket(t *testing.T, e *Engine, response string) <-chan map[string]any {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix IPC fixture")
	}
	dir, err := os.MkdirTemp("/tmp", "lit-wheel-")
	is(t, "")(nil, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	e.SocketDir = dir
	path := filepath.Join(dir, "namespaces", e.Namespace, "run", e.Session+".sock")
	is(t, "")(nil, os.MkdirAll(filepath.Dir(path), 0o700))
	listener, err := net.Listen("unix", path)
	is(t, "")(nil, err)
	requests := make(chan map[string]any, 10)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			var request map[string]any
			if json.NewDecoder(conn).Decode(&request) == nil {
				requests <- request
				_, _ = conn.Write([]byte(response + "\n"))
			}
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); <-done })
	return requests
}

func TestWheelUsesNativePointerAndKeepsEvidence(t *testing.T) {
	svc, f, _, _ := driveSvc(t)
	run, err := svc.load("s1")
	is(t, "")(nil, err)
	e := svc.engineFor(run.ID, run.Legs[0])
	requests := wheelSocket(t, e, `{"success":true,"data":{"dispatched":"mouseWheel"}}`)
	is(t, "")(svc.Drive(ctx, "s1", "browser", []string{"mouse", "move", "700", "350"}))
	out, err := svc.Drive(ctx, "s1", "browser", []string{"mouse", "wheel", "0", "-160"})
	is(t, "")(nil, err)
	request := <-requests
	if request["action"] != "input_mouse" || request["type"] != "mouseWheel" || request["deltaX"] != float64(-160) || request["deltaY"] != float64(0) || request["x"] != nil || request["y"] != nil {
		t.Fatalf("must reuse native pointer state: %v", request)
	}
	if got := f.find("mouse"); !reflect.DeepEqual(got, []string{"mouse", "move", "700", "350"}) {
		t.Fatalf("mouse move changed: %v", got)
	}
	if rec, ok := out["receipt"].(actionRec); !ok || rec.Action != "mouse" || rec.After == "" || !reflect.DeepEqual(rec.Args, []string{"wheel", "0", "-160"}) {
		t.Fatalf("wheel lost screenshot receipt: %v", out)
	}
}

func TestWheelBatchOrderErrorsAndDefaults(t *testing.T) {
	for _, bail := range []bool{false, true} {
		t.Run(map[bool]string{false: "continue", true: "bail"}[bail], func(t *testing.T) {
			f := &fakeEngine{out: map[string]string{"batch": `[{"success":true,"result":{}}]`}, fail: map[string]error{}}
			e := newEngine(EngineInfo{Binary: "fake", RunDir: t.TempDir(), Session: "s", Namespace: "s"}, f.exec)
			defer e.stop()
			requests := wheelSocket(t, e, `{"success":true,"data":{"dispatched":"mouseWheel"}}`)
			f.hook = func(args []string) {
				if len(args) == 3 && args[2] == "click #missing" {
					f.out["--json"] = `[{"success":false,"error":"missing"}]`
					f.fail["--json"] = errors.New("missing")
				} else {
					f.out["--json"] = `[{"success":true,"result":{}}]`
					delete(f.fail, "--json")
				}
			}
			args := []string{"--json", "batch", "mouse move 700 350", `mouse wheel '0' "-160"`, "click #missing", "mouse wheel"}
			if bail {
				args = append(args, "--bail")
			}
			out, err := e.Run(ctx, time.Second, args...)
			if err == nil || !strings.Contains(err.Error(), "missing") {
				t.Fatalf("batch lost failure: %s, %v", out, err)
			}
			var results []map[string]any
			is(t, "")(nil, json.Unmarshal([]byte(out), &results))
			want := 4
			if bail {
				want = 3
			}
			if len(results) != want || results[1]["success"] != true || results[2]["success"] != false {
				t.Fatalf("batch results: %s", out)
			}
			first := <-requests
			if first["deltaX"] != float64(-160) || first["deltaY"] != float64(0) {
				t.Fatalf("batch wheel changed: %v", first)
			}
			if !bail {
				last := <-requests
				if last["deltaY"] != float64(100) || last["deltaX"] != float64(0) {
					t.Fatalf("wheel defaults: %v", last)
				}
			}
		})
	}
}

func TestWheelHelpTextAndNativeErrors(t *testing.T) {
	f := &fakeEngine{out: map[string]string{"mouse": "wheel help", "batch": "Moved"}}
	e := newEngine(EngineInfo{Binary: "fake", RunDir: t.TempDir(), Session: "s", Namespace: "s"}, f.exec)
	defer e.stop()
	for _, flag := range []string{"--help", "-h", "--version", "-V"} {
		out, err := e.Run(ctx, time.Second, "mouse", "wheel", flag)
		is(t, "")(nil, err)
		if out != "wheel help" {
			t.Fatalf("help ran wheel: %q", out)
		}
	}
	requests := wheelSocket(t, e, `{"success":true,"data":{"dispatched":"mouseWheel"}}`)
	out, err := e.Run(ctx, time.Second, "batch", "mouse move 700 350", "mouse wheel 30")
	is(t, "")(nil, err)
	if out != "Moved\nScrolled\n" || (<-requests)["deltaY"] != float64(30) {
		t.Fatalf("text batch changed: %q", out)
	}
	// Failed native wheel calls return errors, rather than falling back to (0,0).
	wheelSocket(t, e, `{"success":false,"error":"target closed"}`)
	out, err = e.Run(ctx, time.Second, "mouse", "wheel", "30", "--json")
	if err == nil || !strings.Contains(err.Error(), "target closed") || !strings.Contains(out, `"success":false`) {
		t.Fatalf("wheel failure: %s, %v", out, err)
	}
}
