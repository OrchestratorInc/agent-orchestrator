package modelcatalog

import (
	"path/filepath"
	"testing"
)

func TestEnvFileHasKeyParsesFinalAssignment(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"comment only", "OPENAI_API_KEY= # placeholder", false},
		{"empty single quotes", "OPENAI_API_KEY='' # unset", false},
		{"empty double quotes", "OPENAI_API_KEY=\"\" # unset", false},
		{"last blank wins", "OPENAI_API_KEY=old\nOPENAI_API_KEY=", false},
		{"last comment wins", "OPENAI_API_KEY=old\nOPENAI_API_KEY= # unset", false},
		{"tab comment", "OPENAI_API_KEY=\t# unset", false},
		{"quoted spaces", "OPENAI_API_KEY='  ' # unset", false},
		{"quoted hash", "OPENAI_API_KEY='#secret' # comment", true},
		{"quoted value", "export OPENAI_API_KEY=\"secret\" # comment", true},
		{"plain value", "OPENAI_API_KEY=secret # comment", true},
		{"last key wins", "OPENAI_API_KEY=\nOPENAI_API_KEY=new", true},
		{"different variable", "OTHER_KEY=secret", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".env")
			writeProviderTestFile(t, path, tc.body)
			if got := envFileHasKey(path, "OPENAI_API_KEY"); got != tc.want {
				t.Fatalf("has credential = %v, want %v", got, tc.want)
			}
		})
	}
}
