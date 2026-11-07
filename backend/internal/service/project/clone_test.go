package project

import (
	"reflect"
	"strings"
	"testing"
)

func TestCloneRepositoryNameDecodesURLPathOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "space", url: "file:///tmp/my%20repo.git", want: "my repo"},
		{name: "path separator", url: "https://github.com/acme/nested%2Frepo.git", want: "repo"},
		{name: "escaped percent", url: "file:///tmp/literal%252Frepo.git", want: "literal%2Frepo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := cloneRepositoryName(tt.url)
			if err != nil {
				t.Fatalf("cloneRepositoryName(%q) returned error: %v", tt.url, err)
			}
			if got != tt.want {
				t.Fatalf("cloneRepositoryName(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestCloneRepositoryNameRejectsMalformedEscape(t *testing.T) {
	t.Parallel()

	if _, err := cloneRepositoryName("file:///tmp/bad%ZZ.git"); err == nil {
		t.Fatal("cloneRepositoryName() accepted a malformed URL escape")
	}
}

func TestNormalizeCloneURL(t *testing.T) {
	for _, tt := range []struct{ input, want string }{
		{"https://github.com/acme/repo", "https://github.com/acme/repo.git"},
		{" https://github.com/acme/repo/ ", "https://github.com/acme/repo.git"},
		{"https://github.com/acme/repo#readme", "https://github.com/acme/repo.git"},
		{"https://github.com/acme/repo.git", "https://github.com/acme/repo.git"},
		{"git@github.com:acme/repo.git", "git@github.com:acme/repo.git"},
		{"https://git.example.com/repo", "https://git.example.com/repo"},
		{"https://github.com/acme/repo?token=secret", "https://github.com/acme/repo?token=secret"},
		{"https://user:secret@github.com/acme/repo", "https://user:secret@github.com/acme/repo"},
		{"https://github.com/acme/repo/tree/main", "https://github.com/acme/repo/tree/main"},
	} {
		if got := normalizeCloneURL(tt.input); got != tt.want {
			t.Errorf("normalizeCloneURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestTransientCloneFailure(t *testing.T) {
	for _, tt := range []struct {
		output string
		want   bool
	}{
		{"fatal: unable to access 'https://example.test': Could not resolve host: example.test", true},
		{"fatal: early EOF", true},
		{"error: RPC failed; HTTP 503", true},
		{"fatal: Authentication failed", false},
		{"Permission denied (publickey). Connection reset", false},
		{"fatal: destination path already exists", false},
	} {
		if got := transientCloneFailure(tt.output); got != tt.want {
			t.Errorf("transientCloneFailure(%q) = %v, want %v", tt.output, got, tt.want)
		}
	}
}

func TestCloneStderrReportsPhasesAndKeepsErrorsClassifiable(t *testing.T) {
	type step struct {
		phase   CloneProgressPhase
		percent int // -1 when git printed none
	}
	var steps []step
	stderr := cloneStderr{report: func(phase CloneProgressPhase, percent *int) {
		value := -1
		if percent != nil {
			value = *percent
		}
		steps = append(steps, step{phase, value})
	}}
	// A long redraw run would fill the 32 KiB buffer if progress were kept.
	redraws := strings.Repeat("Receiving objects:  50% (1/2), 1.00 MiB | 1.00 MiB/s\r", 2000)
	for _, chunk := range []string{
		"Cloning into '/tmp/.ao-clone-1'...\nremote: Enumerating objects: 3, done.\nremote: Coun",
		"ting objects: 100% (3/3), done.\n",
		redraws,
		"Receiving objects: 100% (2/2), done.\nResolving deltas: 100% (1/1), done.\n",
		"fatal: unable to access 'https://user:secret@example.test/': Connection reset by peer\n",
	} {
		if _, err := stderr.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	// Redraws at the same percent report once; a new percent reports again.
	want := []step{
		{ClonePhaseEnumeratingObjects, -1},
		{ClonePhaseCountingObjects, 100},
		{ClonePhaseReceivingObjects, 50},
		{ClonePhaseReceivingObjects, 100},
		{ClonePhaseResolvingDeltas, 100},
	}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("steps = %v, want %v", steps, want)
	}
	if strings.Contains(stderr.String(), "Receiving objects") {
		t.Fatal("progress lines were kept for classification")
	}
	if !transientCloneFailure(stderr.String()) {
		t.Fatalf("final error was not classifiable: %q", stderr.String())
	}
}
