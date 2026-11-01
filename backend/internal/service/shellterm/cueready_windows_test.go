//go:build windows

package shellterm

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	gopty "github.com/aymanbagabas/go-pty"
	"golang.org/x/sys/windows"
)

func TestCueReadinessFromInteractiveWindowsShell(t *testing.T) {
	for _, choice := range []string{"powershell", "pwsh", "cmd", "git-bash"} {
		t.Run(choice, func(t *testing.T) {
			argv, fallback := resolveWindowsShell(choice)
			if fallback || len(argv) == 0 {
				t.Skip("shell unavailable")
			}
			ready, err := prepareCueShellReadiness(t.TempDir(), argv, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ready.cleanup()
			command := "printf '%s' 'left\tright' > output"
			want := "left\tright"
			if choice == "powershell" || choice == "pwsh" {
				command = "[IO.File]::WriteAllText((Join-Path (Get-Location) 'output'), 'left\tright\r\n雪')"
				want = "left\tright\r\n雪"
			}
			p, err := gopty.New()
			if err != nil {
				t.Fatal(err)
			}
			cp := p.(gopty.ConPty)
			defer cp.Close()
			if err := cp.Resize(120, 40); err != nil {
				t.Fatal(err)
			}
			cmd := cp.Command(ready.argv[0], ready.argv[1:]...)
			cmd.Dir = t.TempDir()
			cmd.Env = append([]string{}, os.Environ()...)
			for key, value := range ready.env {
				cmd.Env = append(cmd.Env, key+"="+value)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			output := make(chan string, 64)
			go func() {
				buf := make([]byte, 4096)
				for {
					n, err := cp.Read(buf)
					if n > 0 {
						select {
						case output <- string(buf[:n]):
						default:
						}
					}
					if err != nil {
						return
					}
				}
			}()
			defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				if data, err := os.ReadFile(ready.file); err == nil && strings.TrimSpace(string(data)) == "ready" {
					if choice == "cmd" {
						return
					}
					delivery, err := prepareCueCommand(t.TempDir(), argv, command)
					if err != nil {
						t.Fatal(err)
					}
					defer delivery.cleanup()
					if _, err := io.WriteString(cp, delivery.input+"\r"); err != nil {
						t.Fatal(err)
					}
					for time.Now().Before(deadline) {
						if data, err := os.ReadFile(filepath.Join(cmd.Dir, "output")); err == nil && string(data) == want {
							return
						}
						time.Sleep(50 * time.Millisecond)
					}
					data, err := os.ReadFile(filepath.Join(cmd.Dir, "output"))
					t.Fatalf("%s command output = %q, err = %v, want %q", choice, data, err, want)
				}
				time.Sleep(50 * time.Millisecond)
			}
			var transcript string
			for {
				select {
				case part := <-output:
					transcript += part
				default:
					t.Fatalf("%s did not signal readiness at %s: %q", choice, filepath.Base(ready.file), transcript)
				}
			}
		})
	}
}

func TestCueCommandWindowsPayloadDirectoryIsPrivate(t *testing.T) {
	delivery, err := prepareCueCommand(t.TempDir(), []string{"powershell.exe"}, "Write-Output private")
	if err != nil {
		t.Fatal(err)
	}
	defer delivery.cleanup()
	for _, path := range []string{filepath.Dir(delivery.file), delivery.file} {
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		dacl, _, err := sd.DACL()
		if err != nil || dacl == nil {
			t.Fatalf("missing payload DACL: %v", err)
		}
		token, err := windows.OpenCurrentProcessToken()
		if err != nil {
			t.Fatal(err)
		}
		user, err := token.GetTokenUser()
		token.Close()
		if err != nil {
			t.Fatal(err)
		}
		system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
		if err != nil {
			t.Fatal(err)
		}
		for index := uint32(0); index < 3; index++ {
			var ace *windows.ACCESS_ALLOWED_ACE
			err := windows.GetAce(dacl, index, &ace)
			if index == 2 {
				if err == nil {
					t.Fatal("payload ACL grants access to more than two principals")
				}
				break
			}
			if err != nil || ace == nil {
				t.Fatalf("missing payload allow ACE %d: %v", index, err)
			}
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if !sid.Equals(user.User.Sid) && !sid.Equals(system) {
				t.Fatalf("payload ACL grants access to %s", sid)
			}
		}
		if path == filepath.Dir(delivery.file) {
			control, _, err := sd.Control()
			if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
				t.Fatalf("payload directory inherits permissions: %v", err)
			}
		}
	}
}
