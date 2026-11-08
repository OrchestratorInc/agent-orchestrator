package agentauth

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestDeepSeekHarnessKeyKeepsCommentsAndOtherEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".credentials.yaml")
	t.Setenv("DSH_CREDENTIALS_PATH", path)
	original := "version: 1\nrefs:\n  # my OpenCode Go key\n  OPENCODE_API_KEY: keep-me\n  DEEPSEEK_API_KEY: old\nrecords:\n  dsh/acct:\n    kind: api-key\n    key: rec\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	// A lock left by a process that has exited is taken over.
	if err := os.WriteFile(path+".lock", []byte("2147483646\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewWithAgentResolver(foundExecutable("dsh"), nil, nil, t.TempDir())
	if methods := loginMethods("deepseek-harness"); len(methods) != 2 || methods[0].ID != "deepseek_api_key" || !methods[0].Secret {
		t.Fatalf("DeepSeek Harness methods = %+v", methods)
	}
	if _, err := svc.StartLogin(t.Context(), "deepseek-harness", "deepseek_api_key", "sk-new"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var store struct {
		Version int                       `yaml:"version"`
		Refs    map[string]string         `yaml:"refs"`
		Records map[string]map[string]any `yaml:"records"`
	}
	if err := yaml.Unmarshal(data, &store); err != nil || store.Version != 1 || store.Refs["DEEPSEEK_API_KEY"] != "sk-new" ||
		store.Refs["OPENCODE_API_KEY"] != "keep-me" || store.Records["dsh/acct"]["key"] != "rec" || !strings.Contains(string(data), "# my OpenCode Go key") {
		t.Fatalf("credential store = %s, %v", data, err)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("store mode = %v, %v", info, err)
		}
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Fatal("left DeepSeek Harness's lock behind")
	}
}

func TestDeepSeekHarnessKeyStartsAStoreAndRefusesOldLayouts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "home", ".credentials.yaml")
	t.Setenv("DSH_CREDENTIALS_PATH", path)
	svc := NewWithAgentResolver(foundExecutable("dsh"), nil, nil, t.TempDir())
	if _, err := svc.StartLogin(t.Context(), "deepseek-harness", "deepseek_api_key", "sk-first"); err != nil {
		t.Fatal(err)
	}
	var store struct {
		Version int               `yaml:"version"`
		Refs    map[string]string `yaml:"refs"`
	}
	data, err := os.ReadFile(path)
	if err != nil || yaml.Unmarshal(data, &store) != nil || store.Version != 1 || store.Refs["DEEPSEEK_API_KEY"] != "sk-first" {
		t.Fatalf("new store = %s, %v", data, err)
	}
	// Harness itself upgrades the pre-release flat layout; AO must not guess.
	if err := os.WriteFile(path, []byte("DEEPSEEK_API_KEY: flat\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartLogin(t.Context(), "deepseek-harness", "deepseek_api_key", "sk-second"); err == nil {
		t.Fatal("rewrote an unversioned credential store")
	}
	if data, _ := os.ReadFile(path); string(data) != "DEEPSEEK_API_KEY: flat\n" {
		t.Fatalf("changed a refused store: %s", data)
	}
}

func TestDeepSeekHarnessKeyWaitsForALiveLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".credentials.yaml")
	t.Setenv("DSH_CREDENTIALS_PATH", path)
	if err := os.WriteFile(path+".lock", []byte(strconv.Itoa(os.Getppid())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewWithAgentResolver(foundExecutable("dsh"), nil, nil, t.TempDir())
	if _, err := svc.StartLogin(t.Context(), "deepseek-harness", "deepseek_api_key", "sk-key"); err == nil {
		t.Fatal("wrote while DeepSeek Harness held its lock")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("created the store without the lock")
	}
}

func TestDeepSeekHarnessLockTakesOverOnlyAnExitedHolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell child and directory permissions")
	}
	exited := exec.Command("sh", "-c", "exit 0")
	if err := exited.Run(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".credentials.yaml")
	record := []byte(strconv.Itoa(exited.Process.Pid) + "\n")
	if err := os.WriteFile(path+".lock", record, 0o600); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockDeepSeekStore(t.Context(), path)
	if err != nil {
		t.Fatalf("did not take over an exited holder's lock: %v", err)
	}
	unlock()
	if claims, _ := filepath.Glob(path + ".lock.takeover-*"); len(claims) != 0 {
		t.Fatalf("takeover claim left behind: %v", claims)
	}
	if os.Geteuid() == 0 {
		t.Skip("root can remove the lock regardless of permissions")
	}
	// An exited holder's lock that can't be removed waits out the deadline
	// instead of spinning.
	if err := os.WriteFile(path+".lock", record, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := lockDeepSeekStore(ctx, path)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("locked without removing the earlier lock")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the lock wait never ended")
	}
}
