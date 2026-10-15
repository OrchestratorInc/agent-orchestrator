package litmus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommentUploadsOnlyTheFailingChecksClip(t *testing.T) {
	svc, _ := newSvc(t)
	g := &commentGH{t: t, login: "litmus-bot"}
	svc.gh = g
	run := Run{ID: "run_s1", Repo: "o/r", RepoID: 1234, Evidence: EvidencePost}
	if err := svc.store.Save(run); err != nil {
		t.Fatal(err)
	}
	file := checkClipPath(filepath.Join(svc.store.root, run.ID), "c1")
	if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("clip"), 0o600); err != nil {
		t.Fatal(err)
	}
	v := Verdict{Checks: []Check{
		{ID: "c1", Name: "menu", State: CheckFail},
		{ID: "c2", Name: "menu", Leg: "base", State: CheckPass},
		{ID: "c3", Name: "search", State: CheckPass},
	}}
	urls, state := svc.publishCheckClips(ctx, run, v)
	if state != "attached" || !assetURL.MatchString(urls["menu"]) || len(urls) != 1 || g.uploads != 1 {
		t.Fatalf("clip URLs %v, state %s, uploads %d", urls, state, g.uploads)
	}
	found := false
	for _, call := range g.calls {
		if strings.HasPrefix(call, upload) {
			found = strings.HasSuffix(call, "--input "+file)
		}
	}
	if !found {
		t.Fatalf("upload did not use the check clip: %v", g.calls)
	}
	run.Evidence = EvidenceOff
	if _, state := svc.publishCheckClips(ctx, run, v); state != "off" || g.uploads != 1 {
		t.Fatal("evidence-off run uploaded a clip")
	}
}
