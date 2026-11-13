package github

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeGHOnPath puts a shell-script gh first on PATH for the production shell-out.
func fakeGHOnPath(t *testing.T, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake gh is a POSIX shell script")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestGHTokenSourceHostnameUsesOnlyHostLogin(t *testing.T) {
	// gh would send GH_TOKEN/GITHUB_TOKEN to any *.ghe.com host; they belong
	// to github.com and must not reach the tenant.
	fakeGHOnPath(t, `echo "${GH_TOKEN}${GITHUB_TOKEN}tok:$*"`)
	t.Setenv("GH_TOKEN", "com-tok")
	t.Setenv("GITHUB_TOKEN", "com-tok2")
	got, err := (&GHTokenSource{Hostname: "acme.ghe.com"}).Token(context.Background())
	if err != nil || got != "tok:auth token --hostname acme.ghe.com" {
		t.Fatalf("Token = %q, %v, want host login only", got, err)
	}
}

func TestGHTokenSourceHostnameWithoutLoginIsNoToken(t *testing.T) {
	fakeGHOnPath(t, `echo "no oauth token found for acme.ghe.com" >&2; exit 1`)
	if _, err := (&GHTokenSource{Hostname: "acme.ghe.com"}).Token(context.Background()); !errors.Is(err, ErrNoToken) {
		t.Fatalf("Token err = %v, want ErrNoToken", err)
	}
}
