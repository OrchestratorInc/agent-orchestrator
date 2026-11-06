package specgen_test

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec/specgen"
	"gopkg.in/yaml.v3"
)

// TestBuild_ArtifactFilesPreserveFrozenNull 删除冻结工件数组的豁免会使契约拒绝合法的无变更快照。
func TestBuild_ArtifactFilesPreserveFrozenNull(t *testing.T) {
	data, err := specgen.Build()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]openAPISchemaNode `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if !schemaAllowsNull(doc.Components.Schemas["GitArtifact"].Properties["files"]) {
		t.Fatal("冻结工件 files 的 null 必须保留在生成契约中")
	}
}
