package authprobe

import (
	"context"
	"os"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// RecordFileDiagnostic records only an adapter-owned source path and authored code.
func RecordFileDiagnostic(ctx context.Context, path, code string) {
	step := domain.NewAgentAuthDiagnostic("credential_store", code)
	if source := domain.FileSource(path).Sanitized(); source != nil {
		step.Source = source.Source
	}
	ports.RecordAuthDiagnostic(ctx, step)
}

// ReadCredentialFile preserves the file-read result and records no file contents.
func ReadCredentialFile(ctx context.Context, path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	code := "credential_file_read"
	switch {
	case os.IsNotExist(err):
		code = "credential_file_missing"
	case err != nil:
		code = "credential_store_unreadable"
	case strings.TrimSpace(string(raw)) == "":
		code = "credential_file_empty"
	}
	RecordFileDiagnostic(ctx, path, code)
	return raw, err
}

// FirstCredentialEnvironment checks the adapter's known credential variables.
// Only names and presence are retained; values and shell profiles are not read.
func FirstCredentialEnvironment(ctx context.Context, names []string) string {
	for _, name := range names {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			step := domain.NewAgentAuthDiagnostic("environment", "credential_present")
			step.Source = name
			ports.RecordAuthDiagnostic(ctx, step)
			return name
		}
	}
	step := domain.NewAgentAuthDiagnostic("environment", "credential_environment_missing")
	step.Source = strings.Join(names, ", ")
	ports.RecordAuthDiagnostic(ctx, step)
	return ""
}
