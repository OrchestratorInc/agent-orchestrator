//go:build darwin

package conpty

import (
	"context"
	"encoding/binary"
	"os"
	"reflect"
	"testing"
)

func TestParseProcArgs2(t *testing.T) {
	raw := binary.LittleEndian.AppendUint32(nil, 3)
	raw = append(raw, "/opt/ao\x00\x00\x00ao\x00agent-process\x00explain <defunct> processes\x00HOME=/x\x00"...)
	got, err := parseProcArgs2(raw)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ao", "agent-process", "explain <defunct> processes"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	if _, err := parseProcArgs2(raw[:len(raw)-25]); err == nil {
		t.Fatal("truncated procargs must be an error")
	}
}

func TestProcessArgvReadsThisProcess(t *testing.T) {
	got, err := processArgv(context.Background(), os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, os.Args) {
		t.Fatalf("argv = %q, want %q", got, os.Args)
	}
}
