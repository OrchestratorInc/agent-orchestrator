package project

import "testing"

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
