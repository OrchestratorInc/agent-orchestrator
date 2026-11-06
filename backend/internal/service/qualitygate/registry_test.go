package qualitygate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestQualityRegistry 通过宿主 JSON 文件加载真实策略；每种拒绝都必须丢弃已经解析的合法前缀。
func TestQualityRegistry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policies.json")
	registry, err := LoadRegistry(path)
	if err != nil || len(registry) != 0 {
		t.Fatalf("无登记文件应关闭 Gate: %v, %v", registry, err)
	}
	policy := domain.GatePolicy{ID: "trusted", Revision: 1, ValidFor: time.Hour, MaxFixes: 1,
		Rules: []domain.GateRule{{ID: "check", Level: 1, Required: true, Executable: filepath.Join(t.TempDir(), "checker.exe"), Cwd: ".", Timeout: time.Minute, MaxLogBytes: 1024}}}
	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	valid := "[" + string(raw) + "]"
	if err := os.WriteFile(path, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	registry, err = LoadRegistry(path)
	if err != nil || len(registry) != 1 || !reflect.DeepEqual(registry[policy.ID], policy) {
		t.Fatalf("合法策略未完整解析: %v, %v", registry, err)
	}
	second := strings.Replace(string(raw), `"trusted"`, `"second"`, 1)
	policies := make([]string, 33)
	for i := range policies {
		policies[i] = strings.Replace(string(raw), `"trusted"`, fmt.Sprintf(`"policy-%d"`, i), 1)
	}
	for _, tc := range []struct{ name, data string }{
		{"unknown-policy-field", "[" + string(raw) + "," + strings.Replace(second, `"revision":`, `"untrusted":true,"revision":`, 1) + "]"},
		{"unknown-rule-field", "[" + strings.Replace(string(raw), `"level":`, `"untrusted":true,"level":`, 1) + "]"},
		{"duplicate-policy", "[" + string(raw) + "," + string(raw) + "]"},
		{"invalid-policy", "[" + string(raw) + "," + strings.Replace(second, `"revision":1`, `"revision":0`, 1) + "]"},
		{"trailing-json", valid + " []"},
		{"size-limit", valid + strings.Repeat(" ", 65537-len(valid))},
		{"policy-count-limit", "[" + strings.Join(policies, ",") + "]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.data), 0600); err != nil {
				t.Fatal(err)
			}
			registry, err := LoadRegistry(path)
			if err == nil || len(registry) != 0 {
				t.Fatalf("拒绝失败或泄漏部分登记: %v, %v", registry, err)
			}
		})
	}
}
