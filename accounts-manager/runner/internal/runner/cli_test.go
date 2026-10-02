package runner

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunCLIVersion(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := RunCLI(context.Background(), []string{"version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("RunCLI() code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "ao-accounts-manager") || !strings.Contains(stdout.String(), UpstreamVersion) {
		t.Fatalf("version output = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunCLIServeRequiresAbsoluteStateDirectory(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"serve"},
		{"serve", "--state-dir", "relative"},
		{"unknown"},
	} {
		var stdout, stderr bytes.Buffer
		if code := RunCLI(context.Background(), args, &stdout, &stderr); code != 2 {
			t.Fatalf("RunCLI(%q) code = %d, want 2; stderr=%q", args, code, stderr.String())
		}
	}
}
