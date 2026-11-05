package mobilebridge

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := Path(dir)
	want := State{Enabled: true, Password: "abc", LastPort: 3011}
	if err := Save(p, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got != want {
		t.Fatalf("round trip: got %+v want %+v", got, want)
	}
	info, _ := os.Stat(p)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v want 0600", info.Mode().Perm())
	}
}

func TestLoadMissingIsZero(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "mobile", "config.json"))
	if err != nil || got != (State{}) {
		t.Fatalf("missing file: got %+v err %v", got, err)
	}
}

// TestSaveReplacesConfigWhileReaderIsOpen 验证读者持有完整旧快照时，
// Save 仍能原子发布新配置，且后续 Load 读取完整的新快照。
func TestSaveReplacesConfigWhileReaderIsOpen(t *testing.T) {
	path := Path(t.TempDir())
	old := State{Enabled: true, Password: "old", LastPort: 3011}
	if err := Save(path, old); err != nil {
		t.Fatal(err)
	}
	reader, err := openConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	newState := State{Enabled: true, Password: "new", LastPort: 3012, SecurePairing: true}
	if err := Save(path, newState); err != nil {
		t.Fatalf("save with open reader: %v", err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot State
	if err := json.Unmarshal(data, &snapshot); err != nil || snapshot != old {
		t.Fatalf("open reader snapshot = %+v, err = %v; want %+v", snapshot, err, old)
	}
	got, err := Load(path)
	if err != nil || got != newState {
		t.Fatalf("new config = %+v, err = %v; want %+v", got, err, newState)
	}
}

// TestConcurrentConfigLoadAndSave 验证轮询与替换并发时不丢失配置，
// 每次 Load 只返回其中一个完整快照，不因文件替换出现读取错误或混合字段。
func TestConcurrentConfigLoadAndSave(t *testing.T) {
	path := Path(t.TempDir())
	states := [2]State{
		{Enabled: true, Password: "first", LastPort: 3011},
		{Enabled: true, Password: "second", LastPort: 3012, SecurePairing: true},
	}
	if err := Save(path, states[0]); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		<-start
		for i := range 100 {
			if err := Save(path, states[i%len(states)]); err != nil {
				t.Errorf("concurrent save: %v", err)
				return
			}
		}
	}()
	for range 2 {
		go func() {
			defer wg.Done()
			<-start
			for range 1_000 {
				state, err := Load(path)
				if err != nil || state != states[0] && state != states[1] {
					t.Errorf("concurrent snapshot = %+v, err = %v; want a complete saved state", state, err)
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
}

func TestGeneratePasswordFormat(t *testing.T) {
	pw, err := GeneratePassword()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9]{8}$`).MatchString(pw) {
		t.Fatalf("password %q not 8 alnum", pw)
	}
}

func TestPasswordMatches(t *testing.T) {
	pw, _ := GeneratePassword()
	h := HashPassword(pw)
	if !PasswordMatches(h, pw) {
		t.Fatal("expected match")
	}
	if PasswordMatches(h, pw+"x") {
		t.Fatal("expected mismatch")
	}
}
