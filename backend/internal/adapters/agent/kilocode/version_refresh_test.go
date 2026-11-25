package kilocode

import (
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

func TestImportedHistoryPreflightPinsStableDatabase(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	t.Setenv("KILO_WORKSPACE_ID", "")
	t.Setenv("KILO_DB", "")
	if err := os.Unsetenv("KILO_DB"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AO_PRESERVED", "inherited")
	for _, test := range []struct {
		name             string
		imported         bool
		env              map[string]string
		database, marker string
	}{
		{"imported unset environment", true, nil, "kilo.db", "inherited"},
		{"imported launch environment", true, map[string]string{"AO_PRESERVED": "launch"}, "kilo.db", "launch"},
		{"imported explicit empty database", true, map[string]string{"KILO_DB": "", "AO_PRESERVED": "launch"}, "kilo.db", "launch"},
		{"ordinary release-channel default", false, nil, "kilo-rc.db", "inherited"},
		{"ordinary custom database", false, map[string]string{"KILO_DB": "custom.db", "AO_PRESERVED": "launch"}, "custom.db", "launch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AO_EXPECT_DATABASE", test.database)
			t.Setenv("AO_EXPECT_PRESERVED", test.marker)
			binary := filepath.Join(t.TempDir(), "kilo")
			script := `#!/bin/sh
[ "$1" = export ] && [ "$2" = ses_imported ] || exit 98
[ "${KILO_DB:-kilo-rc.db}" = "$AO_EXPECT_DATABASE" ] || exit 99
[ "$AO_PRESERVED" = "$AO_EXPECT_PRESERVED" ] || exit 100
printf '%s' '{"info":{"id":"ses_imported"}}'
`
			if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			ref := ports.SessionRef{WorkspacePath: t.TempDir()}
			if test.imported {
				ref.Metadata = map[string]string{ports.MetadataKeyImportConfigDir: "/native/kilo"}
			}
			before := maps.Clone(test.env)
			exists, err := (&Plugin{resolvedBinary: binary}).NativeConversationExists(t.Context(), ref, "ses_imported", test.env)
			if err != nil || !exists {
				t.Fatalf("preflight rejected exact history: exists=%v err=%v", exists, err)
			}
			if !maps.Equal(before, test.env) {
				t.Fatalf("preflight changed caller environment: before=%v after=%v", before, test.env)
			}
		})
	}
}

func TestImportedHistoryPreflightRejectsOverridesBeforeExport(t *testing.T) {
	t.Setenv("KILO_WORKSPACE_ID", "")
	t.Setenv("KILO_DB", "")
	for _, test := range []struct{ key, value, want string }{
		{"KILO_WORKSPACE_ID", "other-workspace", "workspace"},
		{"KILO_DB", "other.db", "database filename"},
	} {
		t.Run(test.key, func(t *testing.T) {
			ref := ports.SessionRef{Metadata: map[string]string{ports.MetadataKeyImportConfigDir: "/native/kilo"}}
			p := &Plugin{resolvedBinary: filepath.Join(t.TempDir(), "must-not-run")}
			exists, err := p.NativeConversationExists(t.Context(), ref, "ses_imported", map[string]string{test.key: test.value})
			if exists || err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("override not refused before export: exists=%v err=%v", exists, err)
			}
		})
	}
}
