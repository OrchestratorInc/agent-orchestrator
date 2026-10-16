package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestBrowserSendsTheCommandAndItsDirectoryOverTheSocket(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// A short path: a socket's must fit in about 100 bytes.
	sockets, err := os.MkdirTemp("/tmp", "ab")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockets) })
	socket := filepath.Join(sockets, "b.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	type request struct {
		Args  []string          `json:"args"`
		Dir   string            `json:"dir"`
		Files map[string][]byte `json:"files"`
	}
	var got []request
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in request
		if r.Method != http.MethodPost || r.URL.Path != "/agent" || json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		got = append(got, in)
		response := map[string]any{"output": "ok\n"}
		if in.Args[0] == "upload" {
			response["error"] = "upload failed"
		}
		_ = json.NewEncoder(w).Encode(response)
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	t.Setenv("AO_BROWSER_SOCKET", "")
	if err := runBrowser([]string{"snapshot"}); err == nil || err.Error() != "this sandbox has no browser" {
		t.Fatalf("without AO_BROWSER_SOCKET: %v, want the no-browser message", err)
	}
	t.Setenv("AO_BROWSER_SOCKET", socket)

	if err := runBrowser([]string{"--session", "x", "screenshot", "shot.png"}); err != nil {
		t.Fatal(err)
	}
	// The browser shares these files: it is told where they are, not sent them.
	if len(got) != 1 || got[0].Dir == "" || got[0].Files != nil || len(got[0].Args) != 4 || got[0].Args[2] != "screenshot" {
		t.Fatalf("sent %+v, want the arguments and this directory, no files", got)
	}
	if want, _ := filepath.EvalSymlinks(dir); func() string { d, _ := filepath.EvalSymlinks(got[0].Dir); return d }() != want {
		t.Errorf("dir = %q, want %q", got[0].Dir, dir)
	}

	if err := runBrowser([]string{"upload", "@e2", "notes.txt"}); err == nil || err.Error() != "upload failed" {
		t.Fatalf("upload error = %v, want the browser's error", err)
	}
}
