package runner

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	sdkauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

func TestDeviceTransport(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	auth := vaultFixture()
	plain, _ := json.Marshal(auth)
	if !bytes.Contains(plain, []byte("vault-secret")) {
		t.Fatal("missing plaintext scan positive control")
	}
	sealed, err := sealDeviceCredential(key, auth)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.RawStdEncoding.DecodeString(sealed)
	if bytes.Contains(raw, []byte("vault-secret")) || strings.Contains(sealed, "vault-secret") {
		t.Fatal("unencrypted device transport")
	}
	opened, err := openDeviceCredential(key, sealed)
	if err != nil || opened.Metadata["access_token"] != auth.Metadata["access_token"] {
		t.Fatal("encrypted transport round trip failed")
	}
	another, err := sealDeviceCredential(key, auth)
	if err != nil || another == sealed {
		t.Fatal("transport nonce reused")
	}
	raw[len(raw)-1] ^= 1
	for _, input := range []string{"", "invalid", base64.RawStdEncoding.EncodeToString(raw), strings.Repeat("A", deviceFrameLimit+1)} {
		if _, err := openDeviceCredential(key, input); !errors.Is(err, errDeviceLogin) {
			t.Fatal("invalid device frame admitted")
		}
	}
	for _, key := range [][]byte{nil, bytes.Repeat([]byte{8}, 32)} {
		if _, err := openDeviceCredential(key, sealed); !errors.Is(err, errDeviceLogin) {
			t.Fatal("wrong device key admitted")
		}
	}
}

func TestDeviceWorkerNoFileStore(t *testing.T) {
	for _, outcome := range []string{"success", "provider error", "cancel", "missing key"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			key := bytes.Repeat([]byte{7}, 32)
			input := bytes.NewReader(key)
			if outcome == "missing key" {
				input = bytes.NewReader(nil)
			}
			called := false
			provider := memoryAuthenticator{login: func(_ context.Context, _ *sdkconfig.Config, opts *sdkauth.LoginOptions) (*coreauth.Auth, error) {
				called = true
				if !opts.NoBrowser || opts.Metadata["codex_login_mode"] != "device" || opts.CallbackListener != nil {
					t.Fatal("unsafe device login options")
				}
				if outcome == "provider error" {
					return nil, errors.New("private-vault-secret")
				}
				if outcome == "cancel" {
					cancel()
				}
				return vaultFixture(), nil
			}}
			var output bytes.Buffer
			err := deviceLoginResult(ctx, &sdkconfig.Config{}, provider, input, &output)
			if outcome == "success" {
				if err != nil || !strings.HasPrefix(output.String(), deviceResultPrefix) || strings.Contains(output.String(), "vault-secret") {
					t.Fatal("device result was not privately encoded")
				}
			} else if !errors.Is(err, errDeviceLogin) || output.Len() != 0 {
				t.Fatal("unsuccessful device worker emitted a credential")
			}
			if outcome == "missing key" && called {
				t.Fatal("worker ran without transport key")
			}
		})
	}
}

func TestDeviceProcess(t *testing.T) {
	for _, mode := range []string{"success", "duplicate", "missing", "tampered", "oversized", "invalid-code", "exit-error", "silent", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			commandFor := func(ctx context.Context) *exec.Cmd {
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDeviceWorkerProcess$")
				cmd.Env = append(os.Environ(), "DEVICE_TEST_MODE="+mode, "GORACE=atexit_sleep_ms=0")
				return cmd
			}
			timeout := 2 * time.Second
			if mode == "silent" {
				timeout = 50 * time.Millisecond
			}
			login, err := startDeviceProcess(ctx, commandFor, timeout)
			if err != nil {
				if mode == "success" || mode == "cancel" {
					t.Fatal(err)
				}
				return
			}
			defer login.close()
			if mode == "cancel" {
				cancel()
			}
			select {
			case err, ok := <-login.Done:
				if !ok || (err == nil) != (mode == "success") {
					t.Fatalf("device completion mode=%s err=%v", mode, err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("device process did not stop")
			}
			auth, ok := <-login.Result
			if mode == "success" {
				if !ok || auth == nil || auth.Provider != "codex" || login.UserCode != "ABCD-EFGH" {
					t.Fatal("device process result missing")
				}
			} else if ok || auth != nil {
				t.Fatal("failed process published credential")
			}
		})
	}
}

func TestDeviceWorkerProcess(t *testing.T) {
	mode := os.Getenv("DEVICE_TEST_MODE")
	if mode == "" {
		return
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(os.Stdin, key); err != nil {
		os.Exit(2)
	}
	if mode == "silent" {
		time.Sleep(time.Minute)
		os.Exit(2)
	}
	code := "ABCD-EFGH"
	if mode == "invalid-code" {
		code = "https://unsafe.example"
	}
	fmt.Fprintln(os.Stdout, codexDeviceCodePrefix+code)
	if mode == "cancel" {
		time.Sleep(time.Minute)
		os.Exit(2)
	}
	if mode == "missing" {
		os.Exit(0)
	}
	if mode == "oversized" {
		fmt.Fprintln(os.Stdout, strings.Repeat("A", deviceFrameLimit+1))
		os.Exit(0)
	}
	sealed, err := sealDeviceCredential(key, vaultFixture())
	if err != nil {
		os.Exit(2)
	}
	if mode == "tampered" {
		sealed = "invalid"
	}
	fmt.Fprintln(os.Stdout, deviceResultPrefix+sealed)
	if mode == "duplicate" {
		fmt.Fprintln(os.Stdout, deviceResultPrefix+sealed)
	}
	if mode == "exit-error" {
		fmt.Fprintln(os.Stderr, "private-vault-secret")
		os.Exit(2)
	}
	os.Exit(0)
}

func TestManagedDeviceCoordinator(t *testing.T) {
	for _, outcome := range []string{"success", "cancel", "failure", "shutdown", "instant"} {
		t.Run(outcome, func(t *testing.T) {
			vault := newTestVault(t)
			runtime := &credentialRuntime{vault: vault, manager: coreauth.NewManager(vault, nil, nil)}
			coordinator := newOAuthCoordinator("", "management", nil, nil)
			coordinator.useCredentials(t.Context(), runtime, &sdkconfig.Config{})
			defer coordinator.Close()
			done, result := make(chan error, 1), make(chan *coreauth.Auth, 1)
			coordinator.startCodexDevice = func(context.Context) (codexDeviceLogin, error) {
				if outcome == "instant" {
					result <- vaultFixture()
					done <- nil
				}
				return codexDeviceLogin{AuthorizationURL: codexDeviceVerificationURL, UserCode: "ABCD-EFGH", Done: done, Result: result}, nil
			}
			call := func(method, path, body string, want int) []byte {
				t.Helper()
				request := httptest.NewRequest(method, "/ao/internal/oauth/"+path, strings.NewReader(body))
				request.Header.Set("Authorization", "Bearer management")
				response := httptest.NewRecorder()
				coordinator.ServeHTTP(response, request)
				if response.Code != want || strings.Contains(response.Body.String(), "vault-secret") {
					t.Fatalf("%s: status=%d want=%d", path, response.Code, want)
				}
				return response.Body.Bytes()
			}
			var started struct{ State, UserCode string }
			if json.Unmarshal(call("POST", "start", `{"provider":"codex","mode":"device"}`, 200), &started) != nil || started.State == "" || started.UserCode != "ABCD-EFGH" {
				t.Fatal("missing device instructions")
			}
			if outcome != "instant" && len(runtime.manager.List()) != 0 {
				t.Fatal("pending device account published")
			}
			switch outcome {
			case "cancel":
				call("DELETE", "session?state="+started.State, "", 204)
			case "shutdown":
				coordinator.Close()
			case "failure":
				done <- errors.New("private-vault-secret")
			case "success":
				result <- vaultFixture()
				done <- nil
			}
			coordinator.workers.Wait()
			if outcome == "success" || outcome == "instant" {
				if len(runtime.manager.List()) != 1 {
					t.Fatal("committed device credential missing")
				}
				call("DELETE", "session?state="+started.State, "", 409)
			} else {
				if len(runtime.manager.List()) != 0 {
					t.Fatal("failed device credential published")
				}
				if _, err := vault.Commit(t.Context(), started.State, vaultFixture()); !errors.Is(err, errCredentialFenced) {
					t.Fatal("late device completion admitted")
				}
			}
			assertVaultHasNoSecret(t, vault.root.Name())
		})
	}
}
