package piacp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"runtime"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
)

// fakeExtension answers steers the way AO's Pi extension does.
func fakeExtension(t *testing.T, dir string, answer func(map[string]string) map[string]string) <-chan map[string]string {
	t.Helper()
	listener, err := net.Listen("unix", steerSocketPath(dir))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	got := make(chan map[string]string, 4)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			line, err := bufio.NewReader(conn).ReadBytes('\n')
			if err != nil {
				// Ready's probe connects and sends nothing.
				_ = conn.Close()
				continue
			}
			var request map[string]string
			_ = json.Unmarshal(line, &request)
			got <- request
			reply, _ := json.Marshal(answer(request))
			_, _ = conn.Write(append(reply, '\n'))
			_ = conn.Close()
		}
	}()
	return got
}

// shortDir keeps Unix socket paths under macOS's 104-byte limit.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "pisteer")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestSteerChannelMapsTheExtensionsAnswers(t *testing.T) {
	dir := shortDir(t)
	channel := steerChannel{dir: dir}
	if channel.Ready("s-1") {
		t.Fatal("ready before the extension listens")
	}
	// A socket file with nobody listening, as a Pi process that died leaves.
	stale, err := net.Listen("unix", steerSocketPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = stale.Close()
	if channel.Ready("s-1") {
		t.Fatal("ready on a stale socket")
	}
	_ = os.Remove(steerSocketPath(dir))
	var outcome string
	got := fakeExtension(t, dir, func(map[string]string) map[string]string {
		return map[string]string{"outcome": outcome, "reason": "busy elsewhere"}
	})
	if !channel.Ready("s-1") {
		t.Fatal("not ready once the extension listens")
	}

	outcome = "injected"
	if result, err := channel.Steer(context.Background(), "s-1", "use staging"); err != nil || result != acpdriver.SteerInjected {
		t.Fatalf("injected answer = %v, %v", result, err)
	}
	if request := <-got; request["sessionId"] != "s-1" || request["text"] != "use staging" {
		t.Fatalf("request = %v", request)
	}

	outcome = "promptRequired"
	if result, err := channel.Steer(context.Background(), "s-1", "too late"); err != nil || result != acpdriver.SteerNoRunningTurn {
		t.Fatalf("promptRequired answer = %v, %v", result, err)
	}
	<-got

	outcome = "error"
	_, err = channel.Steer(context.Background(), "s-1", "x")
	var refusal interface{ ChatRefusal() bool }
	if err == nil || !errors.As(err, &refusal) || !refusal.ChatRefusal() {
		t.Fatalf("error answer = %v, want a refusal", err)
	}
	<-got
}

func TestSteerChannelWithoutTheExtensionIsARefusal(t *testing.T) {
	_, err := steerChannel{dir: shortDir(t)}.Steer(context.Background(), "s-1", "use staging")
	var refusal interface{ ChatRefusal() bool }
	if err == nil || !errors.As(err, &refusal) || !refusal.ChatRefusal() {
		t.Fatalf("steer with no socket = %v, want a definite refusal (nothing was sent)", err)
	}
}

func TestSteerSocketPathMatchesTheExtension(t *testing.T) {
	// agent/pi's extension listens on <AO_PI_STEER_DIR>/ao-steer.sock.
	if got, want := steerSocketPath("/run"), "/run/ao-steer.sock"; got != want {
		t.Fatalf("socket path = %q, want %q", got, want)
	}
}

func TestSteeringIsOfferedOnlyForTheSupportedAdapter(t *testing.T) {
	ok := acpsdk.InitializeResponse{AgentInfo: &acpsdk.Implementation{Name: distributionName, Version: "0.17.1"}}
	other := acpsdk.InitializeResponse{AgentInfo: &acpsdk.Implementation{Name: "pi-acp", Version: "0.0.34"}}
	if got := steerExternal.Supported(ok); got != (runtimeSupportsSteering()) {
		t.Fatalf("supported(%s) = %v", distributionName, got)
	}
	if steerExternal.Supported(other) || steerExternal.Supported(acpsdk.InitializeResponse{}) {
		t.Fatal("steering offered for another adapter")
	}
	cfg := acpdriver.LaunchConfig{DataDir: "/data", SessionID: "proj-1"}
	if got := steerExternal.Open(cfg).(steerChannel).dir; got != "/data/pi-acp/proj-1/run" {
		t.Fatalf("channel dir = %q", got)
	}
}

func runtimeSupportsSteering() bool { return runtime.GOOS != "windows" }
