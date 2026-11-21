package persistenthost

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestCommandConfigRoundTrip(t *testing.T) {
	for _, protocol := range []Protocol{ProtocolRaw, ProtocolACP, ProtocolUnreal} {
		t.Run(string(protocol), func(t *testing.T) {
			want := Config{SessionID: "session", DataDir: "/data", Workdir: "/workspace", Argv: []string{"provider", "--flag"}, Protocol: protocol}
			if protocol != ProtocolRaw {
				want.OwnershipFingerprint = "fingerprint"
			}
			got, err := commandConfig(hostArgs(want)[1:])
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("config = %+v, %v; want %+v", got, err, want)
			}
		})
	}
}

func TestCommandConfigRejectsIncompleteLaunch(t *testing.T) {
	for _, args := range [][]string{
		nil, {"session", "/data", "/workspace"},
		{"session", "/data", "/workspace", "--"},
		{"session", "/data", "/workspace", "acp", "", "--", "provider"},
		{"session", "/data", "/workspace", "unreal", "fingerprint", "provider"},
	} {
		if _, err := commandConfig(args); !errors.Is(err, ErrCommandUsage) {
			t.Fatalf("args %q: got %v, want usage error", args, err)
		}
	}
}

func TestHostExecutable(t *testing.T) {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "ao"+ext)
	helper := filepath.Join(dir, "ao-chat-host"+ext)
	args := []string{"chat-host", "session", "/data", "/workspace", "--", "provider"}
	check := func(wantExe string, wantArgs []string) {
		t.Helper()
		gotExe, gotArgs, err := hostExecutable(exe, args)
		if err != nil || gotExe != wantExe || !reflect.DeepEqual(gotArgs, wantArgs) {
			t.Fatalf("executable = %q %q, %v; want %q %q", gotExe, gotArgs, err, wantExe, wantArgs)
		}
	}
	check(exe, args)
	if err := os.Mkdir(helper, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := hostExecutable(exe, args); err == nil {
		t.Fatal("accepted a directory as the host executable")
	}
	if err := os.Remove(helper); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	check(helper, args[1:])
	if runtime.GOOS != "windows" {
		if err := os.Chmod(helper, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := hostExecutable(exe, args); err == nil {
			t.Fatal("accepted a host without execute permission")
		}
	}
}
