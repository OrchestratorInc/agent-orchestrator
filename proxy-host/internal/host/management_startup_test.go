package host

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestBuiltSDKAllowsPrivateManagementAndBothLoginFlows(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	control, inference := strings.Repeat("c", 32), strings.Repeat("i", 32)
	service, err := Build(t.TempDir(), port, control, inference, testRoutes(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- service.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("SDK service did not stop")
		}
	})
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 5 * time.Second}
	request := func(method, path, key string) (int, []byte, error) {
		req, err := http.NewRequest(method, base+path, nil)
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		response, err := client.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		return response.StatusCode, body, err
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		status, _, err := request("GET", "/ao/status", control)
		if err == nil && status == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("SDK management startup timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, key := range []string{inference, "wrong-control", ""} {
		status, _, err := request("GET", "/v8/management/credentials", key)
		if err != nil || status != http.StatusUnauthorized {
			t.Fatalf("non-control identity reached management: status=%d error=%v", status, err)
		}
	}
	status, _, err := request("GET", "/v8/management/config", control)
	if err != nil || status != http.StatusNotFound {
		t.Fatalf("private management allowlist expanded: status=%d error=%v", status, err)
	}
	status, _, err = request("GET", "/v8/management/credentials", control)
	if err != nil || status != http.StatusOK {
		t.Fatalf("SDK rejected the private control identity: status=%d error=%v", status, err)
	}
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			status, body, err := request("GET", "/v8/management/oauth/auth-url?provider="+provider, control)
			if err != nil || status != http.StatusOK {
				t.Fatalf("login URL failed: status=%d error=%v", status, err)
			}
			var login struct{ URL, State string }
			if err := json.Unmarshal(body, &login); err != nil || login.State == "" || !strings.HasPrefix(login.URL, "https://") {
				t.Fatal("SDK did not return a valid pending login")
			}
			state := url.QueryEscape(login.State)
			status, body, err = request("GET", "/v8/management/oauth/status?state="+state, control)
			if err != nil || status != http.StatusOK {
				t.Fatalf("pending login status failed: status=%d error=%v", status, err)
			}
			var pending struct{ Status string }
			if err := json.Unmarshal(body, &pending); err != nil || pending.Status != "wait" {
				t.Fatal("uncompleted login was not waiting")
			}
			status, _, err = request("DELETE", "/v8/management/oauth/session?state="+state, control)
			if err != nil || status < 200 || status >= 300 {
				t.Fatalf("login cancellation failed: status=%d error=%v", status, err)
			}
		})
	}
}
