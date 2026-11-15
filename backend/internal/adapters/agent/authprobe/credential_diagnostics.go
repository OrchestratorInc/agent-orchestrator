package authprobe

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func Getenv(ctx context.Context, name string) string {
	if value, ok := ports.AuthEnvironmentOverride(ctx, name); ok {
		return value
	}
	return os.Getenv(name)
}

func Environ(ctx context.Context, env []string) []string {
	overrides := ports.AuthEnvironmentOverrides(ctx)
	if len(overrides) == 0 {
		return env
	}
	out := make([]string, 0, len(env)+len(overrides))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if _, ok := overrides[name]; !ok {
			out = append(out, entry)
		}
	}
	for name, value := range overrides {
		out = append(out, name+"="+value)
	}
	return out
}

func FileSource(path string) domain.AgentAccountIdentity {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			path = filepath.Join("~", rel)
		}
	}
	return domain.AgentAccountIdentity{SourceKind: domain.AgentCredentialSourceFile, Source: path}
}

func RecordFileDiagnostic(ctx context.Context, path, code string) {
	step := domain.NewAgentAuthDiagnostic("credential_store", code)
	if source := FileSource(path).Sanitized(); source != nil {
		step.Source = source.Source
	}
	ports.RecordAuthDiagnostic(ctx, step)
}

func ReadCredentialFile(ctx context.Context, path string) ([]byte, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // harness credential file chosen by the adapter
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

func CredentialEnvironment(ctx context.Context, method domain.AgentAccountMethod, names ...string) string {
	for _, name := range names {
		if strings.TrimSpace(Getenv(ctx, name)) != "" {
			ports.RecordAuthEvidence(ctx, domain.EnvironmentSource(name).WithMethod(method))
			return name
		}
	}
	step := domain.NewAgentAuthDiagnostic("environment", "credential_environment_missing")
	step.Source = strings.Join(names, ", ")
	ports.RecordAuthDiagnostic(ctx, step)
	return ""
}
