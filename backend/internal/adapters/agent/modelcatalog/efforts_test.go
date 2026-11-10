package modelcatalog

import (
	"context"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestJSONCatalogRetainsNativeThinking 覆盖原生命令已报告档位但目录转换丢失的回归。
func TestJSONCatalogRetainsNativeThinking(t *testing.T) {
	models, err := parseJSONModels([]byte(`{"models":[
{"provider":"codegpt","id":"cn:glm-5.3","selector":"codegpt/cn:glm-5.3","reasoning":true,"thinking":["low","high","max"]},
{"id":"unknown","reasoning":true}
]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %+v, want two model records", models)
	}
	for _, model := range models {
		var want []string
		if model.ID == "codegpt/cn:glm-5.3" {
			want = []string{"low", "high", "max"}
		}
		if !reflect.DeepEqual(model.Efforts, want) {
			t.Fatalf("%s efforts = %v, want %v", model.ID, model.Efforts, want)
		}
	}
}

// TestACPModelCatalogRetainsEfforts 确认 ACP 报告的当前模型档位能到达项目模型目录。
func TestACPModelCatalogRetainsEfforts(t *testing.T) {
	d := Discoverer{ACPModels: map[string]ACPModelListFunc{"deepseek-harness": func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.AgentModelInfo, error) {
		return []ports.AgentModelInfo{{ID: "custom/model", Label: "Custom", Provider: "custom", IsDefault: true, Efforts: []string{"low", "medium", "high"}, DefaultEffort: "medium"}}, nil
	}}}
	catalog, err := d.Discover(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "deepseek-harness"})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 1 || !reflect.DeepEqual(catalog.Models[0].Efforts, []string{"low", "medium", "high"}) || catalog.Models[0].DefaultEffort != "medium" {
		t.Fatalf("catalog lost reasoning metadata: %+v", catalog.Models)
	}
}
