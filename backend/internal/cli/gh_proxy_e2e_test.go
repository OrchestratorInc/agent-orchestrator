//go:build e2e

package cli_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

// Exercise the actual installed native alias, not a direct call to the proxy.
// The fake provider CLI keeps this deterministic without creating live PRs.
func TestE2E_GHWrapper(t *testing.T) {
	realDir := t.TempDir()
	source := filepath.Join(realDir, "fake.go")
	body := `package main
import("fmt";"os";"time";"os/exec")
func main(){for name,want:=range map[string]string{"gh":os.Getenv("AO_EXPECT_REAL_GH"),"ao":os.Getenv("AO_EXPECT_PINNED_AO")}{got,err:=exec.LookPath(name);if err!=nil || got!=want{fmt.Fprintf(os.Stderr,"delegated %s=%s want=%s err=%v",name,got,want,err);os.Exit(39)}};if len(os.Args)>1 && os.Args[1]=="wait"{fmt.Println("ready");time.Sleep(time.Hour);return};fmt.Fprintln(os.Stdout,"https://github.com/example/frontend/pull/123");fmt.Fprintln(os.Stderr,"gh stderr");if len(os.Args)>1 && os.Args[1]=="fail"{os.Exit(37)}}`
	if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	name := "gh"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", filepath.Join(realDir, name), source).CombinedOutput(); err != nil {
		t.Fatalf("build fake gh: %v %s", err, out)
	}
	for _, status := range []int{200, 502} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			e := newEnv(t)
			var claims atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/api/v1/sessions/worker-4/pr/claim" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				var body struct {
					PR            string `json:"pr"`
					AllowTakeover bool   `json:"allowTakeover"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil || body.PR != "https://github.com/example/frontend/pull/123" || body.AllowTakeover {
					t.Errorf("claim=%+v", body)
				}
				claims.Add(1)
				w.WriteHeader(status)
				if status != 200 {
					_, _ = w.Write([]byte(`{"code":"PROVIDER_FAILURE","message":"failure","requestId":"gh-e2e"}`))
				}
			}))
			defer server.Close()
			if err := runfile.Write(e.runFile, runfile.Info{PID: os.Getpid(), Port: server.Listener.Addr().(*net.TCPAddr).Port}); err != nil {
				t.Fatal(err)
			}
			pinned, err := agentlaunch.PinnedPATH(func() (string, error) { return aoBin, nil }, os.Getenv, map[string]string{"PATH": realDir + string(os.PathListSeparator) + os.Getenv("PATH")}, e.dataDir)
			if err != nil {
				t.Fatal(err)
			}
			launch := map[string]string{"PATH": pinned}
			agentlaunch.AugmentRuntimePATHForLaunchBinary(context.Background(), launch, []string{filepath.Join(realDir, "agent")}, exec.LookPath, agentlaunch.PinnedDir(func() (string, error) { return aoBin, nil }, e.dataDir))
			wrapper := filepath.Join(strings.Split(launch["PATH"], string(os.PathListSeparator))[0], name)
			environ := e.environ("")
			for i := len(environ) - 1; i >= 0; i-- {
				key, _, _ := strings.Cut(environ[i], "=")
				if strings.EqualFold(key, "PATH") {
					environ = append(environ[:i], environ[i+1:]...)
				}
			}
			aoName := "ao"
			if runtime.GOOS == "windows" {
				aoName += ".exe"
			}
			environ = append(environ, "PATH="+launch["PATH"], "AO_SESSION_ID=worker-4", "AO_EXPECT_REAL_GH="+filepath.Join(realDir, name), "AO_EXPECT_PINNED_AO="+filepath.Join(filepath.Dir(wrapper), "passthrough", aoName))
			for _, args := range [][]string{{"pr", "create", "--repo", "example/frontend", "--head", "unexpected-branch"}, {"pr", "view"}, {"fail"}} {
				cmd := exec.Command(wrapper, args...)
				cmd.Env = environ
				var out, stderr bytes.Buffer
				cmd.Stdout = &out
				cmd.Stderr = &stderr
				err := cmd.Run()
				if args[0] == "fail" {
					if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 37 {
						t.Fatalf("exit=%v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if out.String() != "https://github.com/example/frontend/pull/123\n" {
					t.Fatalf("stdout=%q", out.String())
				}
				if status == 502 && args[0] == "pr" && args[1] == "create" {
					if !strings.Contains(stderr.String(), "gh-e2e") {
						t.Fatal(stderr.String())
					}
				} else if stderr.String() != "gh stderr\n" {
					t.Fatal(stderr.String())
				}
			}
			if runtime.GOOS != "windows" {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, wrapper, "wait")
				cmd.Env = environ
				stdout, err := cmd.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				line, err := bufio.NewReader(stdout).ReadString('\n')
				if err != nil || line != "ready\n" {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					t.Fatalf("ready=%q err=%v", line, err)
				}
				if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					t.Fatal(err)
				}
				if err := cmd.Wait(); err == nil {
					t.Fatal("termination lost")
				} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 143 {
					t.Fatalf("termination exit=%v", err)
				}
			}
			if claims.Load() != 1 {
				t.Fatalf("claim attempts=%d", claims.Load())
			}
		})
	}
}
