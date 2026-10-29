package litmus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

const fakeSHA = "0123456789abcdef0123456789abcdef01234567"

// evGit stands in for git: it records "dir|git argv", makes a clone dir on `clone`, answers ls-remote with remote
// and rev-parse with fakeSHA, notes the files staged at `add` time, and fails any argv containing a key of fail
// (once per entry in failOnce, else always).
type evGit struct {
	t         *testing.T
	remote    string
	fail      map[string]error
	failOnce  map[string]error
	calls     []string
	added     []string
	unchanged bool // an empty staged diff; otherwise diff returns the files recorded by add
}

func (g *evGit) Run(ctx context.Context, dir string, _ []string, name string, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if filepath.Base(name) == "ffprobe" {
		return []byte("120\n"), nil
	}
	if name != "git" {
		g.t.Fatalf("unexpected command %s", name)
	}
	line := strings.Join(args, " ")
	if len(args) > 2 && args[0] == "-C" && args[2] == "for-each-ref" { // the retention pass lists the mirror's pins (`git -C <mirror> for-each-ref ...`); it is not evidence work
		return nil, nil
	}
	g.calls = append(g.calls, dir+"|"+line)
	if len(args) < 3 || args[0] != "-c" || args[1] != ghCredentials {
		g.t.Fatalf("evidence git lacks its per-command credential helper: %v", args)
	}
	args = args[2:]
	for k, err := range g.failOnce {
		if strings.Contains(line, k) {
			delete(g.failOnce, k)
			return nil, err
		}
	}
	for k, err := range g.fail {
		if strings.Contains(line, k) {
			return nil, err
		}
	}
	switch args[0] {
	case "clone":
		dest := args[len(args)-1]
		_ = os.MkdirAll(filepath.Join(dest, ".git"), 0o750)
		_ = os.WriteFile(filepath.Join(dest, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600)
	case "ls-remote":
		return []byte(g.remote), nil
	case "add":
		files, _ := filepath.Glob(filepath.Join(dir, args[len(args)-1], "*"))
		for _, f := range files {
			g.added = append(g.added, filepath.Base(f))
		}
	case "rev-parse":
		return []byte(fakeSHA + "\n"), nil
	case "diff":
		if g.unchanged {
			return nil, nil
		}
		return []byte(strings.Join(g.added, "\n")), nil
	}
	return nil, nil
}

func (g *evGit) argv() []string { // the git argv alone, without the dir
	out := make([]string, len(g.calls))
	for i, c := range g.calls {
		_, out[i], _ = strings.Cut(c, "|")
	}
	return out
}

// sizedMedia stands in for ffmpeg: every output is a few bytes, except a GIF, whose size depends on the fps asked for.
type sizedMedia struct {
	gif   map[string]int // "fps=8" -> bytes
	err   error
	calls [][]string
}

func (m *sizedMedia) Run(_ context.Context, args ...string) error {
	m.calls = append(m.calls, args)
	if m.err != nil {
		return m.err
	}
	out, size := args[len(args)-1], 3
	if strings.HasSuffix(out, ".gif") {
		for k, n := range m.gif {
			if strings.Contains(strings.Join(args, " "), k+",") {
				size = n
			}
		}
	}
	return os.WriteFile(out, make([]byte, size), 0o600)
}

// shotCheck is a check whose receipts have after-shot files on disk, except those in missing.
func TestGIFFallsBackToFourFpsThenSkips(t *testing.T) {
	for name, c := range map[string]struct {
		sizes map[string]int
		made  bool
		fps   []string // the fps asked for, in order
	}{
		"8 fps fits":         {map[string]int{"fps=8": 100, "fps=4": 50}, true, []string{"fps=8"}},
		"4 fps fits":         {map[string]int{"fps=8": gifCap + 1, "fps=4": gifCap}, true, []string{"fps=8", "fps=4"}},
		"nothing fits":       {map[string]int{"fps=8": gifCap + 1, "fps=4": gifCap + 1}, false, []string{"fps=8", "fps=4"}},
		"exactly at the cap": {map[string]int{"fps=8": gifCap}, true, []string{"fps=8"}},
	} {
		t.Run(name, func(t *testing.T) {
			svc, _ := newSvc(t)
			m := &sizedMedia{gif: c.sizes}
			svc.media = m
			out := filepath.Join(t.TempDir(), "head.gif")
			made, err := svc.makeGIF(ctx, "in.mp4", out)
			var fps []string
			for _, a := range m.calls {
				fps = append(fps, strings.Split(a[slices.Index(a, "-filter_complex")+1], ",")[0])
			}
			if err != nil || made != c.made || !reflect.DeepEqual(fps, c.fps) || fileExists(out) != c.made {
				t.Fatalf("made %t err %v fps %q file %t", made, err, fps, fileExists(out))
			}
			a := m.calls[0]
			if !strings.Contains(a[slices.Index(a, "-filter_complex")+1], "scale=640:-2") || !strings.Contains(a[slices.Index(a, "-filter_complex")+1], "palettegen") || !slices.Contains(a, "-loop") {
				t.Fatalf("recipe: %q", a)
			}
		})
	}
}

func evRun(t *testing.T, mode string) (*Service, *evGit, Run, string) {
	t.Helper()
	svc, _ := newSvc(t)
	g := &evGit{t: t}
	svc.runner = g
	run := commentRun
	run.Evidence = mode
	is(t, "")(nil, svc.store.Save(run))
	stage := t.TempDir()
	is(t, "")(nil, os.WriteFile(filepath.Join(stage, "c1-head-1.png"), []byte("png"), 0o600))
	is(t, "")(nil, os.WriteFile(filepath.Join(stage, "head.gif"), []byte("gif"), 0o600))
	return svc, g, run, stage
}

func TestPushEvidenceCreatesAnOrphanBranchOrContinuesTheRemoteOne(t *testing.T) {
	svc, g, run, stage := evRun(t, EvidencePost)
	clone := filepath.Join(svc.store.dataDir(), "litmus", "evidence", "o__r")
	names := []string{"c1-head-1.png", "head.gif"}
	msg := "litmus: evidence for PR 7 run run_s1"

	sha, err := svc.pushEvidence(ctx, run, stage, names, svc.credentialPattern(ctx)) // the remote has no litmus/evidence: an orphan
	wantNew := []string{
		"clone --no-checkout --depth 1 --filter=blob:none https://github.com/o/r.git " + clone,
		"ls-remote --heads origin refs/heads/litmus/evidence",
		"symbolic-ref HEAD refs/heads/litmus/evidence", "update-ref -d refs/heads/litmus/evidence", "read-tree --empty",
		"add -f -- runs/7/20261011-085600", "diff --cached --name-only", "commit -q --no-gpg-sign --no-verify -m " + msg, "rev-parse HEAD", "push origin litmus/evidence",
	}
	for i := range wantNew {
		wantNew[i] = "-c " + ghCredentials + " " + wantNew[i]
	}
	if err != nil || sha != fakeSHA || !reflect.DeepEqual(g.argv(), wantNew) || !reflect.DeepEqual(g.added, names) {
		t.Fatalf("sha %q err %v\ngit: %q\nadded %q", sha, err, g.argv(), g.added)
	}
	if !strings.HasPrefix(g.calls[0], filepath.Dir(clone)+"|") || !strings.HasPrefix(g.calls[1], clone+"|") {
		t.Fatalf("the clone is made beside, and git runs in, the dedicated dir: %q", g.calls[:2])
	}
	if _, err := os.Stat(filepath.Join(clone, "runs")); !os.IsNotExist(err) {
		t.Fatalf("the staged copy stays in the clone: %v", err)
	}

	g.calls, g.added, g.remote = nil, nil, "abc\trefs/heads/litmus/evidence\n" // a later run: the clone exists and the branch is on the remote
	if _, err := svc.pushEvidence(ctx, run, stage, names, svc.credentialPattern(ctx)); err != nil {
		t.Fatal(err)
	}
	wantOld := []string{
		"ls-remote --heads origin refs/heads/litmus/evidence",
		"fetch --depth 1 origin refs/heads/litmus/evidence", "symbolic-ref HEAD refs/heads/litmus/evidence", "reset -q --mixed FETCH_HEAD",
		"add -f -- runs/7/20261011-085600", "diff --cached --name-only", "commit -q --no-gpg-sign --no-verify -m " + msg, "rev-parse HEAD", "push origin litmus/evidence",
	}
	for i := range wantOld {
		wantOld[i] = "-c " + ghCredentials + " " + wantOld[i]
	}
	if !reflect.DeepEqual(g.argv(), wantOld) {
		t.Fatalf("git: %q", g.argv())
	}
	for _, c := range g.calls {
		if strings.Contains(c, "checkout") || strings.Contains(c, "push -f") || strings.Contains(c, "--force") {
			t.Fatalf("evidence must never check files out or force-push: %q", c)
		}
	}
}

func TestPushEvidenceUsesPerCommandCredentialsAndSurfacesFailuresWithoutRetry(t *testing.T) {
	for _, command := range []string{"clone", "ls-remote", "fetch", "push"} {
		for _, reason := range []string{
			"fatal: could not read Username for 'https://github.com': terminal prompts disabled",
			"fatal: Authentication failed for 'https://github.com/o/r.git/'",
			"fatal: unable to access 'https://github.com/o/r.git/': The requested URL returned error: 403",
		} {
			t.Run(command+"/"+reason, func(t *testing.T) {
				svc, g, run, stage := evRun(t, EvidencePost)
				gh := &commentGH{t: t, login: "bot"}
				svc.gh = gh
				g.remote = fakeSHA + "\trefs/heads/litmus/evidence\n"
				g.failOnce = map[string]error{command: errors.New("git " + command + ": exit status 128\n" + reason)}
				sha, err := svc.pushEvidence(ctx, run, stage, []string{"head.gif"}, svc.credentialPattern(ctx))
				if err == nil || err.Error() != "git "+command+": "+reason || sha != "" {
					t.Fatalf("sha %q err %v", sha, err)
				}
				attempts := 0
				for _, args := range g.argv() {
					if !strings.HasPrefix(args, "-c "+ghCredentials+" ") {
						t.Fatalf("missing per-command credentials: %s", args)
					}
					if strings.HasPrefix(args, "-c "+ghCredentials+" "+command+" ") {
						attempts++
					}
				}
				if attempts != 1 || len(gh.calls) != 0 {
					t.Fatalf("attempts %d, gh %q, git %q", attempts, gh.calls, g.argv())
				}
			})
		}
	}
}

// evSubmit is a submit with after-shots and no recordings.

func TestEvidenceFailureReasonHidesLocalPaths(t *testing.T) {
	svc := &Service{store: NewStore("/data/ao")}
	ev := svc.evidenceFailed(Run{ID: "run_s1"}, &os.PathError{Op: "mkdir", Path: "/data/ao/litmus/evidence/o__r", Err: errors.New("denied")})
	if strings.Contains(ev.state, "/data/ao") || strings.Contains(strings.Join(ev.limits, "\n"), "/data/ao") {
		t.Fatalf("path leaked: %q %q", ev.state, ev.limits)
	}
	if !strings.Contains(ev.state, "<data dir>/litmus/evidence/o__r") {
		t.Fatalf("state = %q", ev.state)
	}
}

type mediaFunc func(args []string) error

func TestPushEvidenceReusesCommitOnlyWhenStagedFilesAreUnchanged(t *testing.T) {
	for _, mode := range []string{"unchanged", "unchanged without saved commit", "changed", "diff failure"} {
		t.Run(mode, func(t *testing.T) {
			svc, g, run, stage := evRun(t, EvidencePost)
			run.EvidenceCommit = strings.Repeat("a", 40)
			g.remote = fakeSHA + "\trefs/heads/litmus/evidence\n"
			g.fail = map[string]error{}
			if strings.HasPrefix(mode, "unchanged") {
				g.unchanged = true
				g.fail["commit "] = errors.New("nothing to commit, working tree clean")
				if mode == "unchanged without saved commit" {
					run.EvidenceCommit = ""
				}
			} else if mode == "diff failure" {
				g.fail["diff "] = errors.New("cannot read index")
			}
			sha, err := svc.pushEvidence(ctx, run, stage, []string{"head.gif"}, svc.credentialPattern(ctx))
			if mode == "diff failure" {
				if err == nil || !strings.Contains(err.Error(), "cannot read index") || sha != "" {
					t.Fatalf("diff failure hidden: sha %q err %v", sha, err)
				}
			} else {
				want := fakeSHA
				if mode == "unchanged" {
					want = run.EvidenceCommit
				}
				if err != nil || sha != want {
					t.Fatalf("sha %q err %v, want %s", sha, err, want)
				}
			}
			commits, pushes := 0, 0
			for _, call := range g.argv() {
				if strings.Contains(call, " commit ") {
					commits++
				}
				if strings.Contains(call, " push ") {
					pushes++
				}
			}
			wantWrites := 0
			if mode == "changed" {
				wantWrites = 1
			}
			if commits != wantWrites || pushes != wantWrites {
				t.Fatalf("unnecessary or missing writes: commits %d pushes %d", commits, pushes)
			}
			if mode == "unchanged without saved commit" && !slices.Contains(g.argv(), "-c "+ghCredentials+" rev-parse HEAD") {
				t.Fatal("unchanged checkout did not resolve its existing HEAD")
			}
		})
	}
}

func TestFindingClipRangeAcrossSleep(t *testing.T) {
	start := time.UnixMilli(1793165389392)
	a, b := int64(239713), int64(260440)
	frames := []screencastFrame{
		{N: 34, WallMS: 1793165629105, OffsetMS: &a},
		{N: 35, WallMS: 1793166252101, OffsetMS: &b},
	}
	f := Finding{Leg: "head", Clip: &FindingClip{From: 1, To: 2}}
	receipts := []Receipt{{Seq: 1, At: time.UnixMilli(frames[0].WallMS + 100)}, {Seq: 2, At: time.UnixMilli(frames[1].WallMS + 100)}}
	from, duration, ok := findingClipRange(f, receipts, start, 592647*time.Millisecond, frames...)
	if !ok || from != 236813*time.Millisecond || duration != 26727*time.Millisecond {
		t.Fatalf("clip from %s duration %s ok %t", from, duration, ok)
	}
	// A still screen sends no frames: a click 20 s after the last frame is 20 s later in the video.
	still := []Receipt{{Seq: 1, At: time.UnixMilli(frames[0].WallMS + 1000)}, {Seq: 2, At: time.UnixMilli(frames[0].WallMS + 20000)}}
	if from, duration, ok := findingClipRange(f, still, start, 592647*time.Millisecond, frames...); !ok || from != 237713*time.Millisecond || duration != 25000*time.Millisecond {
		t.Fatalf("still-screen clip from %s duration %s ok %t", from, duration, ok)
	}
	// Missing wall timestamps keep the old recording-origin calculation.
	for i := range frames {
		frames[i].WallMS = 0
	}
	if _, _, ok := findingClipRange(f, receipts, start, 592647*time.Millisecond, frames...); ok {
		t.Fatal("wall-less index did not retain legacy out-of-range rejection")
	}
}
