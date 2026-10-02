//go:build linux

package persistenthost

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNamespacePermitRequiresPositiveExactMessage(t *testing.T) {
	identity := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name, message string
		wantOK        bool
	}{
		{"closed", "", false},
		{"truncated", "AO-NS-START/1 " + identity, false},
		{"wrong-launch", "AO-NS-START/1 " + strings.Repeat("b", 64) + "\n", false},
		{"wrong-version", "AO-NS-START/2 " + identity + "\n", false},
		{"trailing", "AO-NS-START/1 " + identity + "\nx", false},
		{"repeated", strings.Repeat("AO-NS-START/1 "+identity+"\n", 2), false},
		{"complete", "AO-NS-START/1 " + identity + "\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			if _, err := io.WriteString(writer, tc.message); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			err = awaitNamespacePermit(t.Context(), identity, reader)
			if (err == nil) != tc.wantOK {
				t.Fatalf("execution admission: %v, want success %v", err, tc.wantOK)
			}
		})
	}
}

func TestNamespacePermitCancellationJoinsRead(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	defer reader.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- awaitNamespacePermit(ctx, strings.Repeat("a", 64), reader) }()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled helper admitted execution")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled permit reader did not stop")
	}
}
