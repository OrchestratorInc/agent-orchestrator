package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartRejectionCleansPrivatePreparation(t *testing.T) {
	probeErr := errors.New("port probe failed")
	for _, test := range []struct {
		name string
		port int
		err  error
	}{{"zero", 0, nil}, {"live", 3001, nil}, {"out of range", 65536, nil}, {"probe", 0, probeErr}} {
		t.Run(test.name, func(t *testing.T) {
			f, spec := preparedStartFixture(t, false)
			a := f.a
			root := filepath.Join(filepath.Dir(spec.CheckoutPath), string(spec.AttemptID))
			prepare := a.ops.prepare
			a.ops.prepare = func(ctx context.Context, frontend, commit, path string) error {
				if err := prepare(ctx, frontend, commit, path); err != nil {
					return err
				}
				for _, name := range []string{"data", "electron", "daemon", "setup"} {
					if err := os.Mkdir(filepath.Join(path, name), 0700); err != nil {
						return err
					}
				}
				return os.WriteFile(filepath.Join(path, "diagnostic.json"), []byte("checked facts"), 0600)
			}
			a.ops.freePort = func() (int, error) { return test.port, test.err }
			target, err := a.Start(context.Background(), spec)
			if err == nil || target.ID != "" || (test.err != nil && !errors.Is(err, test.err)) {
				t.Fatalf("rejected start: target=%+v err=%v", target, err)
			}
			for _, name := range []string{"data", "electron", "fixtures", "daemon", "setup"} {
				if _, err := os.Stat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("private %s remains: %v", name, err)
				}
			}
			if _, err := os.Stat(filepath.Join(root, "diagnostic.json")); err != nil {
				t.Fatal("preparation diagnostic lost", err)
			}
			if _, err := os.Stat(filepath.Join(spec.CheckoutPath, "frontend", ".vite", "testing-target.json")); err != nil {
				t.Fatal("warm checkout changed", err)
			}
		})
	}
}

func TestStartRejectionRefusesChangedPrivateRoot(t *testing.T) {
	f, spec := preparedStartFixture(t, false)
	a := f.a
	root := filepath.Join(filepath.Dir(spec.CheckoutPath), string(spec.AttemptID))
	a.ops.freePort = func() (int, error) {
		if err := os.Rename(root, root+"-owned"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(root, "fixtures"), 0700); err != nil {
			t.Fatal(err)
		}
		return 0, nil
	}
	_, err := a.Start(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "target state directory ownership changed") {
		t.Fatalf("changed root cleanup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "fixtures")); err != nil {
		t.Fatal("changed root was modified", err)
	}
}
