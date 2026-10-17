package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "chat-host" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestWorkerProviderHostChild(t *testing.T) {
	marker := os.Getenv("AO_TEST_PROVIDER_HOST_MARKER")
	if marker == "" {
		return
	}
	if err := os.WriteFile(marker, []byte("started"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func TestWorkerMainRunsProviderHostBeforeBootstrap(t *testing.T) {
	for _, protocol := range []string{"raw", "acp"} {
		t.Run(protocol, func(t *testing.T) {
			dataDir, workdir := t.TempDir(), t.TempDir()
			marker := filepath.Join(dataDir, "provider-started")
			args := []string{"chat-host", "naming-test", dataDir, workdir}
			if protocol == "acp" {
				args = append(args, "acp", "test-ownership")
			}
			args = append(args, "--", os.Args[0], "-test.run=^TestWorkerProviderHostChild$")
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], args...)
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "AO_CLOUD_") && !strings.HasPrefix(entry, "AO_WORKER_") && !strings.HasPrefix(entry, "AO_WORKSPACE_DIR=") {
					cmd.Env = append(cmd.Env, entry)
				}
			}
			cmd.Env = append(cmd.Env, "AO_TEST_PROVIDER_HOST_MARKER="+marker)
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			reaped := false
			go func() { done <- cmd.Wait() }()
			t.Cleanup(func() {
				cancel()
				if !reaped {
					<-done
				}
			})
			path := filepath.Join(dataDir, "chat-hosts", "naming-test", "host.json")
			var descriptor struct {
				Version  int    `json:"version"`
				Protocol string `json:"protocol"`
				Address  string `json:"address"`
				Token    string `json:"token"`
			}
			for {
				body, err := os.ReadFile(path)
				_, markerErr := os.Stat(marker)
				if err == nil && markerErr == nil && json.Unmarshal(body, &descriptor) == nil {
					break
				}
				select {
				case err := <-done:
					reaped = true
					t.Fatalf("provider host exited before starting: %v: %s", err, output.String())
				case <-ctx.Done():
					t.Fatal("provider host did not start")
				case <-time.After(10 * time.Millisecond):
				}
			}
			wantProtocol := "" // The raw host profile predates protocol tagging.
			if protocol == "acp" {
				wantProtocol = "acp"
			}
			if descriptor.Protocol != wantProtocol {
				t.Fatalf("host protocol = %q, want %q", descriptor.Protocol, wantProtocol)
			}
			conn, err := net.DialTimeout("tcp", descriptor.Address, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := json.NewEncoder(conn).Encode(map[string]any{"version": descriptor.Version, "token": descriptor.Token, "action": "shutdown"}); err != nil {
				t.Fatal(err)
			}
			var response struct {
				OK bool `json:"ok"`
			}
			if err := json.NewDecoder(conn).Decode(&response); err != nil || !response.OK {
				t.Fatalf("shutdown response = %+v, error = %v", response, err)
			}
			err = <-done
			reaped = true
			if err != nil {
				t.Fatalf("host shutdown failed: %v: %s", err, output.String())
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("host descriptor remains after shutdown: %v", err)
			}
		})
	}
}
