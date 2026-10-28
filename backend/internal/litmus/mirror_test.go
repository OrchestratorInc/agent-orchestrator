package litmus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// partialCloneRunner is a git whose bare clone writes some of the mirror and then dies.
type partialCloneRunner struct{ *fakeRunner }

func (p partialCloneRunner) Run(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	if name == "git" && slices.Contains(args, "clone") {
		dest := args[len(args)-1]
		return nil, errors.Join(os.MkdirAll(filepath.Join(dest, "objects"), 0o750), os.WriteFile(filepath.Join(dest, "config"), []byte("[core]\n"), 0o600), errors.New("fatal: early EOF"))
	}
	return p.fakeRunner.Run(ctx, dir, env, name, args...)
}

// A mirror exists only once it is whole: a clone that fails, or whose setup fails, leaves neither the mirror nor its
// .tmp, and the next call makes it.
func TestEnsureMirrorLeavesNothingWhenTheCloneFails(t *testing.T) {
	for name, fail := range map[string]func(svc *Service, f *fakeRunner){
		"clone dies midway":   func(svc *Service, f *fakeRunner) { svc.runner = partialCloneRunner{f} },
		"config fails":        func(_ *Service, f *fakeRunner) { f.gitErr["config gc.auto"] = errors.New("fatal: boom") },
		"clone fails at once": func(_ *Service, f *fakeRunner) { f.gitErr["clone --bare"] = errors.New("fatal: unable to access") },
	} {
		t.Run(name, func(t *testing.T) {
			svc, _ := newSvc(t)
			f := svc.runner.(*fakeRunner)
			mirror := svc.store.mirrorDir("acme/widgets")
			fail(svc, f)
			if err := svc.ensureMirror(ctx, "acme/widgets"); err == nil {
				t.Fatal("ensureMirror succeeded")
			}
			if pathExists(mirror) || pathExists(mirror+".tmp") {
				t.Fatalf("a failed clone left %v (mirror) %v (tmp)", pathExists(mirror), pathExists(mirror+".tmp"))
			}
			svc.runner = f
			delete(f.gitErr, "config gc.auto")
			delete(f.gitErr, "clone --bare")
			if err := svc.ensureMirror(ctx, "acme/widgets"); err != nil || !fileExists(filepath.Join(mirror, "HEAD")) || pathExists(mirror+".tmp") {
				t.Fatalf("the next call did not make the mirror: %v", err)
			}
		})
	}
}
