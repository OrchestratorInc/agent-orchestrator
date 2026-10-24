package agentauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

func TestLoginMethodsInvokeOnlyFixedCommands(t *testing.T) {
	for _, tc := range []struct {
		agent, method string
		args          []string
		secret        bool
	}{
		{"codex", "browser", []string{"login"}, false},
		{"codex", "device_code", []string{"login", "--device-auth"}, false},
		{"codex", "api_key", []string{"login", "--with-api-key"}, true},
		{"codex", "access_token", []string{"login", "--with-access-token"}, true},
		{"claude-code", "browser", []string{"auth", "login", "--claudeai"}, false},
		{"claude-code", "console", []string{"auth", "login", "--console"}, false},
		{"claude-code", "sso", []string{"auth", "login", "--sso"}, false},
	} {
		t.Run(tc.agent+"/"+tc.method, func(t *testing.T) {
			args, secret, err := loginArgs(tc.agent, tc.method)
			if err != nil || !reflect.DeepEqual(args, tc.args) || secret != tc.secret {
				t.Fatalf("args=%v secret=%v err=%v", args, secret, err)
			}
		})
	}
}

func TestUILoginRejectsInvalidInputBeforeLaunch(t *testing.T) {
	svc := New(foundExecutable("codex"), nil)
	svc.commandContext = func(context.Context, string, ...string) *exec.Cmd { t.Fatal("launched invalid login"); return nil }
	for _, tc := range []struct{ agent, method, secret string }{
		{"unknown", "browser", ""}, {"cursor", "browser", ""}, {"codex", "--arbitrary", ""},
		{"codex", "browser", "secret"}, {"codex", "api_key", ""}, {"codex", "api_key", "one\ntwo"},
		{"codex", "api_key", strings.Repeat("s", 16*1024+1)}, {"claude-code", "api_key", "secret"},
	} {
		_, err := svc.StartLogin(t.Context(), tc.agent, tc.method, tc.secret)
		var apiErr *apierr.Error
		if !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindInvalid {
			t.Fatalf("expected invalid input: %v", err)
		}
	}
}

func TestLoginOutputExposesOnlyRecognizedPrompts(t *testing.T) {
	svc := New(nil, nil)
	op := &loginOperation{snapshot: LoginSnapshot{AgentID: "codex", Method: "device_code", Status: "running"}}
	output := &loginOutput{service: svc, operation: op}
	_, _ = output.Write([]byte("private output secret\nhttps://auth.openai.com/codex/dev"))
	if op.snapshot.LoginURL != "" {
		t.Fatal("published an incomplete URL")
	}
	_, _ = output.Write([]byte("ice\n\x1b[32mABCD-EFGHI\x1b[0m\nhttps://evil.example/oauth/authorize\n"))
	if op.snapshot.LoginURL != "https://auth.openai.com/codex/device" || op.snapshot.DeviceCode != "ABCD-EFGHI" {
		t.Fatalf("snapshot=%+v", op.snapshot)
	}
	data, _ := json.Marshal(op.snapshot)
	if strings.Contains(string(data), "private") || strings.Contains(string(data), "secret") || strings.Contains(string(data), "evil") {
		t.Fatalf("leaked raw output: %s", data)
	}
	_, _ = output.Write([]byte(strings.Repeat("x", 32*1024)))
	if len(output.tail) > 16*1024 {
		t.Fatal("unbounded output")
	}
	for _, raw := range []string{"http://auth.openai.com/codex/device", "https://auth.openai.com.evil.test/codex/device", "https://evil@auth.openai.com/codex/device", "https://auth.openai.com:444/codex/device", "https://auth.openai.com/other", "https://claude.ai/oauth/authorize"} {
		if safeLoginURL("codex", raw) {
			t.Fatalf("accepted unsafe URL: %s", raw)
		}
	}
}

// The subprocess verifies stdin without touching any real harness credentials.
func TestUILoginHelper(t *testing.T) {
	mode := os.Getenv("AO_TEST_UI_LOGIN")
	if mode == "" {
		return
	}
	switch mode {
	case "secret":
		data, _ := io.ReadAll(os.Stdin)
		if string(data) != "test-secret\n" {
			os.Exit(3)
		}
		fmt.Println("test-secret https://auth.openai.com/oauth/authorize?secret=test-secret")
	case "fail":
		fmt.Println("private failure details")
		os.Exit(4)
	case "wait":
		fmt.Println("https://auth.openai.com/codex/device\nABCD-EFGHI")
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}

func loginTestService(t *testing.T, mode string) *Service {
	t.Helper()
	svc := New(foundExecutable("codex"), nil)
	svc.commandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return process.CommandContext(ctx, os.Args[0], "-test.run=^TestUILoginHelper$")
	}
	t.Setenv("AO_TEST_UI_LOGIN", mode)
	t.Cleanup(svc.Close)
	return svc
}

func awaitLogin(t *testing.T, svc *Service, id string, predicate func(LoginSnapshot) bool) LoginSnapshot {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := svc.Login(t.Context(), "codex", id)
		if err != nil {
			t.Fatal(err)
		}
		if predicate(snapshot) {
			return snapshot
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("login did not reach expected state")
	return LoginSnapshot{}
}

func TestUILoginSecretUsesStdinAndDiscardsOutput(t *testing.T) {
	svc := loginTestService(t, "secret")
	started, err := svc.StartLogin(t.Context(), "codex", "api_key", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	result := awaitLogin(t, svc, started.ID, func(s LoginSnapshot) bool { return s.Status != "running" })
	if result.Status != "succeeded" || result.LoginURL != "" {
		t.Fatalf("result=%+v", result)
	}
	data, _ := json.Marshal(result)
	if strings.Contains(string(data), "test-secret") {
		t.Fatalf("credential leaked: %s", data)
	}
}

func TestUILoginFailureDoesNotExposeProcessOutput(t *testing.T) {
	svc := loginTestService(t, "fail")
	started, err := svc.StartLogin(t.Context(), "codex", "browser", "")
	if err != nil {
		t.Fatal(err)
	}
	result := awaitLogin(t, svc, started.ID, func(s LoginSnapshot) bool { return s.Status != "running" })
	if result.Status != "failed" {
		t.Fatalf("result=%+v", result)
	}
}

func TestUILoginCancellationAndOperationIsolation(t *testing.T) {
	svc := loginTestService(t, "wait")
	started, err := svc.StartLogin(t.Context(), "codex", "device_code", "")
	if err != nil {
		t.Fatal(err)
	}
	awaitLogin(t, svc, started.ID, func(s LoginSnapshot) bool { return s.DeviceCode != "" })
	if _, err := svc.StartLogin(t.Context(), "codex", "browser", ""); err == nil {
		t.Fatal("allowed concurrent login")
	}
	if _, err := svc.Start(t.Context(), "codex"); err == nil {
		t.Fatal("allowed a terminal login over a background login")
	}
	if _, err := svc.CancelLogin(t.Context(), "claude-code", started.ID); err == nil {
		t.Fatal("cancelled another harness login")
	}
	cancelled, err := svc.CancelLogin(t.Context(), "codex", started.ID)
	if err != nil || cancelled.Status != "cancelled" || cancelled.LoginURL != "" || cancelled.DeviceCode != "" {
		t.Fatalf("result=%+v err=%v", cancelled, err)
	}
	next, err := svc.StartLogin(t.Context(), "codex", "browser", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CancelLogin(t.Context(), "codex", started.ID); err == nil {
		t.Fatal("stale operation survived replacement")
	}
	if _, err := svc.Login(t.Context(), "codex", next.ID); err != nil {
		t.Fatal(err)
	}
}

func TestUILoginTimeoutAndShutdownStopProcesses(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		svc := loginTestService(t, "wait")
		if !shutdown {
			svc.loginTimeout = 100 * time.Millisecond
		}
		started, err := svc.StartLogin(t.Context(), "codex", "browser", "")
		if err != nil {
			t.Fatal(err)
		}
		if shutdown {
			svc.Close()
		}
		result := awaitLogin(t, svc, started.ID, func(s LoginSnapshot) bool { return s.Status != "running" })
		want := "timed_out"
		if shutdown {
			want = "cancelled"
		}
		if result.Status != want {
			t.Fatalf("result=%+v", result)
		}
		if shutdown {
			if _, err := svc.StartLogin(t.Context(), "codex", "browser", ""); err == nil {
				t.Fatal("started after shutdown")
			}
		}
	}
}
