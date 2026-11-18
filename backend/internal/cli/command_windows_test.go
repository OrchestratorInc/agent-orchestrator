//go:build windows

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCLIBatchArguments(t *testing.T) {
	for _, ext := range []string{".cmd", ".bat"} {
		t.Run(ext, func(t *testing.T) {
			dir := t.TempDir()
			shim := filepath.Join(dir, "%PATH% agent tool"+ext)
			// Forward exactly as npm's shims do.
			script := "@echo off\r\n\"" + os.Args[0] + "\" -test.run=TestCLIBatchArgHelper -- %*\r\n"
			if err := os.WriteFile(shim, []byte(script), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("AO_CLI_BATCH_HELPER", "1")
			// Batch shims cannot forward newlines, so the launcher passes a one-line prompt pointer.
			args := []string{"space in argument", "%PATH%", `quoted "text" & data`}
			var out bytes.Buffer
			if err := runInteractiveCommand(t.Context(), shim, dir, args, bytes.NewReader(nil), &out, &out); err != nil {
				t.Fatal(err, out.String())
			}
			var got []string
			if err := json.Unmarshal(out.Bytes(), &got); err != nil || !reflect.DeepEqual(got, args) {
				t.Fatalf("%v: got %q, want %q", err, out.String(), args)
			}
		})
	}
}

func TestCLIBatchArgHelper(t *testing.T) {
	if os.Getenv("AO_CLI_BATCH_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			_ = json.NewEncoder(os.Stdout).Encode(os.Args[i+1:])
			os.Exit(0)
		}
	}
	os.Exit(1)
}
