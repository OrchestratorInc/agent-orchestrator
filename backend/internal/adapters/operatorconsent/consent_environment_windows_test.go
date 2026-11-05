//go:build windows

package operatorconsent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// TestOperatorConsentRejectsUserModuleInjection 使用真实 Verifier；无效审批在加载JSON后、WinRT和UI前拒绝。
func TestOperatorConsentRejectsUserModuleInjection(t *testing.T) {
	directory, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	systemModules := filepath.Join(directory, "WindowsPowerShell", "v1.0", "Modules")
	scratch := t.TempDir()
	module := filepath.Join(scratch, "modules", "Microsoft.PowerShell.Utility")
	if err := os.MkdirAll(module, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(scratch, "injected")
	manifest := "@{ ModuleVersion='1.0.0'; RootModule='Microsoft.PowerShell.Utility.psm1'; FunctionsToExport=@('ConvertFrom-Json') }"
	source := "function ConvertFrom-Json { param([string]$InputObject); [IO.File]::WriteAllText('" +
		strings.ReplaceAll(marker, "'", "''") + "', 'loaded'); [Console]::Out.Write('Verified'); exit 0 }; Export-ModuleMember -Function ConvertFrom-Json"
	for name, content := range map[string]string{"Microsoft.PowerShell.Utility.psd1": manifest, "Microsoft.PowerShell.Utility.psm1": source} {
		if err := os.WriteFile(filepath.Join(module, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(scratch)
	for _, scenario := range []struct{ name, path string }{
		{"system-only", systemModules}, {"user-injection", filepath.Join(scratch, "modules") + ";" + systemModules},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Setenv("PSModulePath", scenario.path)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			err := New().Verify(ctx, principal.Approval{})
			if !errors.Is(err, principal.ErrConsentDenied) {
				t.Fatalf("native input boundary did not reject invalid approval: %v", err)
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("user module executed inside verifier: %v", err)
			}
		})
	}
}
