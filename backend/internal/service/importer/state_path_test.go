package importer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestImportRejectsConfiguredStateDirectory 防止自动 Git 操作接触自定义 AO 数据目录。
func TestImportRejectsConfiguredStateDirectory(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "custom-state")
	child := filepath.Join(state, "data")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	// 相对环境值保留启动时的写法；服务必须使用配置层已解析的 DataDir。
	t.Setenv("AO_DATA_DIR", "custom-state")
	svc := New(Deps{Store: newFakeStore(), DataDir: state})
	for _, path := range []string{state, child, root} {
		result, err := svc.Validate(context.Background(), ImportValidationInput{ImportKind: ImportKindProject, Path: path})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsValid {
			t.Errorf("state directory accepted: %s", path)
		}
		wantActions(t, result.BlockingErrors, []string{"IMPORT_PATH_UNSAFE"})
		prepared, err := svc.PrepareGit(context.Background(), GitPreparationInput{ImportKind: ImportKindProject, Path: path, ApprovedActions: []string{GitPreparationActionInit}})
		if err != nil {
			t.Fatal(err)
		}
		if len(prepared.Events) != 0 || prepared.Validation.IsValid {
			t.Fatalf("state preparation was not rejected: %#v", prepared)
		}
		if _, err := os.Stat(filepath.Join(path, ".git")); !os.IsNotExist(err) {
			t.Fatalf("state Git metadata changed: %v", err)
		}
	}
	sibling := filepath.Join(root, "custom-state-project")
	if err := os.Mkdir(sibling, 0o700); err != nil {
		t.Fatal(err)
	}
	allowed, err := svc.Validate(context.Background(), ImportValidationInput{ImportKind: ImportKindProject, Path: sibling})
	if err != nil || !allowed.IsValid {
		t.Fatalf("adjacent project incorrectly blocked: %#v, %v", allowed, err)
	}
}
