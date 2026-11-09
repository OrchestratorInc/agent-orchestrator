package modelcatalog

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestOMPCatalogKeepsNativeRunnableModels 验证原生选择器保留在可用列表中，全量缓存只补充浏览目录。
func TestOMPCatalogKeepsNativeRunnableModels(t *testing.T) {
	env := providerTestEnv(t)
	db, err := sql.Open("sqlite", filepath.Join(env["PI_CODING_AGENT_DIR"], "models.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE model_cache (provider_id TEXT, models TEXT);
INSERT INTO model_cache VALUES ('codegpt', '[{"id":"DeepSeek-V4.1-Flash","provider":"codegpt","name":"DeepSeek V4.1 Flash"}]');`); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	binary := filepath.Join(root, "omp")
	script := "#!/bin/sh\ncat \"$(dirname \"$0\")/catalog.json\"\n"
	if runtime.GOOS == "windows" {
		binary += ".cmd"
		script = "@echo off\r\ntype \"%~dp0catalog.json\"\r\n"
	}
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "catalog.json"), []byte(`{"models":[{
"provider":"codegpt","id":"cn:deepseek-v4.1-flash",
"selector":"codegpt/cn:deepseek-v4.1-flash","name":"DeepSeek V4.1 Flash"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, err := (Discoverer{}).Discover(context.Background(), ports.AgentModelDiscoveryRequest{
		AgentID: "omp", Binary: binary, WorkingDir: root, Env: env,
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"codegpt/cn:deepseek-v4.1-flash"}; !reflect.DeepEqual(catalog.RunnableModels, want) {
		t.Fatalf("runnable models = %v, want %v", catalog.RunnableModels, want)
	}
	if len(catalog.Models) != 2 {
		t.Fatalf("browse catalog = %#v, want native and cached models", catalog.Models)
	}
}
