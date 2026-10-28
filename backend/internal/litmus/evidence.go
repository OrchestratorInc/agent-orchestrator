package litmus

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	evidenceBranch = "litmus/evidence"
	stageTimeout   = time.Minute      // ffmpeg for the images
	pushTimeout    = 90 * time.Second // git, and the wait for another run's publish
	gifWidth       = 640              // the GIFs are previews of the full video
	gifCap         = 4 << 20          // GitHub's image proxy gives up on files much past this
	uploadTimeout  = 90 * time.Second // one video's upload
	videoCap       = 25 << 20         // a recording over this goes up as its cut
	assetsURL      = "https://uploads.github.com/user-attachments/assets"
)

// videoRetryWait is how long a fresh upload that answers 404 waits before its one retry. Tests shorten it.
var videoRetryWait = 15 * time.Second

var (
	commitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)
	assetURL  = regexp.MustCompile(`^https://github\.com/user-attachments/assets/[0-9a-fA-F-]{36}$`)
)

// evItem is one image in the comment: file is its name in the stage dir and in the repo, kind where it goes.
type evItem struct {
	file, alt, caption, url string
	finding                 string // finding id
	kind                    int
}

const (
	kindFinding = iota
	kindGIF
	kindReceipt
)

func (it evItem) image() string { return "![" + it.alt + "](" + it.url + ")" }

// vidItem is one uploaded video: url is empty when it could not be attached, cut when the cut went up because the
// recording was over videoCap, span how long it plays (zero for a cut, or when unknown).
type vidItem struct {
	leg, url string
	cut      bool
	span     time.Duration
}

// evidence is what publishing left for the comment: the state, the commit the links are pinned to, the images, the
// Limits lines it earns, and the videos with their own state. The zero value is a comment without images or videos.
type evidence struct {
	state, commit string
	items         []evItem
	limits        []string
	videos        []vidItem
	videoState    string
	findingImages map[string]string        // finding id -> published URL
	findingClips  map[string]string        // finding id -> published or local clip URL
	clipSpans     map[string]time.Duration // finding id -> measured local clip duration
}

// videoURLs are the attached videos' links by leg, for the Run.
func (e evidence) videoURLs() (m map[string]string) {
	if len(e.findingClips) > 0 {
		m = make(map[string]string, len(e.findingClips))
		for name, url := range e.findingClips {
			m[name] = url
		}
	}
	for _, v := range e.videos {
		if v.url != "" {
			if m == nil {
				m = map[string]string{}
			}
			m[v.leg] = v.url
		}
	}
	return m
}

// evidenceURL is how the comment links a published file, pinned to the commit. A public repo serves it from
// raw.githubusercontent.com. For a private repo that host answers 404 to the browser, so the blob page's raw form is
// used, which renders for members (reported by users, not verified here).
func evidenceURL(repo, sha, file string, private bool) string {
	if private {
		return fmt.Sprintf("https://github.com/%s/blob/%s/%s?raw=true", repo, sha, file)
	}
	return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s", repo, sha, file)
}

// gifArgs makes a looping GIF of in at fps with one palette pass (split, palettegen, paletteuse in one graph).
func gifArgs(in, out string, fps int) []string {
	return []string{"-y", "-i", in, "-an", "-sn", "-filter_complex",
		fmt.Sprintf("fps=%d,scale=%d:-2:flags=lanczos,split[a][b];[a]palettegen=stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=5:diff_mode=rectangle", fps, gifWidth),
		"-loop", "0", out}
}

// makeGIF writes out from in at 8 fps, and again at 4 fps when that is over gifCap. It reports false, without a
// file, when even that is too big.
func (s *Service) makeGIF(ctx context.Context, in, out string) (bool, error) {
	for _, fps := range []int{8, 4} {
		if err := s.runMedia(ctx, gifArgs(in, out, fps)...); err != nil {
			return false, err
		}
		st, err := os.Stat(out)
		if err != nil {
			return false, err // ffmpeg said ok and wrote nothing
		}
		if st.Size() <= gifCap {
			return true, nil
		}
	}
	_ = os.Remove(out)
	return false, nil
}

func findingID(i int) string { return fmt.Sprintf("finding-%d", i+1) }

func findingClipPath(runDir string, i int) string {
	return filepath.Join(runDir, "clips", findingID(i)+".mp4")
}

// findingShot uses the selected clip's final after-shot on the finding's leg.
func findingShot(f Finding, receipts []Receipt) (Receipt, bool) {
	if f.Clip != nil {
		for _, r := range receipts {
			if r.onLeg() == f.Leg && r.Seq == f.Clip.To {
				return r, fileExists(r.AfterPath)
			}
		}
	}
	return Receipt{}, false
}

// stageEvidence keeps each finding's final after-shot at its original width and the leg GIF previews.
func (s *Service) stageEvidence(ctx context.Context, run Run, v Verdict, dir string) (items []evItem, limits []string, first error) {
	note := func(err error) {
		s.commentLogf(run, "evidence: %v", err)
		first = cmp.Or(first, err)
		limits = append(limits, err.Error())
	}
	receipts, err := s.store.Receipts(run.ID)
	if err != nil {
		note(err)
		return items, limits, first
	}
	for i, f := range v.Findings {
		if f.Clip == nil || (run.Kind != "issue" && f.Leg == "base") {
			continue
		}
		r, ok := findingShot(f, receipts)
		if !ok {
			note(fmt.Errorf("%s: clip.to has no saved after-shot", findingID(i)))
			continue
		}
		name := findingID(i) + ".png"
		if err := copyFile(r.AfterPath, filepath.Join(dir, name)); err != nil {
			note(err)
			continue
		}
		items = append(items, evItem{file: name, alt: mdText(f.Title), finding: findingID(i), kind: kindFinding})
	}
	wanted := map[string]bool{}
	for _, f := range v.Findings {
		if run.Kind != "issue" && f.Leg == "base" {
			continue
		}
		for _, id := range f.Receipts {
			wanted[fmt.Sprintf("%s/%d", f.Leg, id)] = true
		}
	}
	for _, c := range v.Coverage {
		for _, id := range c.Receipts {
			wanted[fmt.Sprintf("%s/%d", c.Leg, id)] = true
		}
	}
	for _, r := range receipts {
		if !wanted[fmt.Sprintf("%s/%d", r.onLeg(), r.Seq)] || !fileExists(r.AfterPath) {
			continue
		}
		name := fmt.Sprintf("%s-%d-after.png", r.onLeg(), r.Seq)
		if err := copyFile(r.AfterPath, filepath.Join(dir, name)); err != nil {
			note(err)
			continue
		}
		items = append(items, evItem{file: name, kind: kindReceipt})
	}
	for _, l := range run.Legs {
		cut := cutPath(filepath.Join(s.store.root, run.ID), l.Name)
		if !fileExists(cut) {
			continue
		}
		file := l.Name + ".gif"
		made, err := s.makeGIF(ctx, cut, filepath.Join(dir, file))
		if err != nil {
			note(err)
		} else if !made {
			limits = append(limits, fmt.Sprintf("%s recording preview exceeded %d MB", l.Name, gifCap>>20))
		} else {
			items = append(items, evItem{file: file, alt: l.Name + " recording", kind: kindGIF})
		}
	}
	return items, limits, first
}

// prepareEvidence stages images locally so publication can check all metadata before login or uploads.
func (s *Service) prepareEvidence(ctx context.Context, run Run, v Verdict) evidence {
	dir := filepath.Join(s.store.root, run.ID, "evidence")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return s.evidenceFailed(run, err)
	}
	sctx, cancel := context.WithTimeout(ctx, stageTimeout)
	items, limits, first := s.stageEvidence(sctx, run, v, dir)
	cancel()
	if len(items) == 0 {
		if first != nil {
			return s.evidenceFailed(run, first)
		}
		return evidence{state: EvidenceOff, limits: limits}
	}
	return evidence{items: items, limits: limits}
}

// publishEvidence pushes the prepared images. Failures become the state failed:<reason> and a Limits line.
func (s *Service) publishEvidence(ctx context.Context, run Run, ev evidence, re *regexp.Regexp) evidence {
	items := ev.items
	if len(items) == 0 {
		return ev
	}
	dir := filepath.Join(s.store.root, run.ID, "evidence")
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.file
	}
	gctx, cancel := context.WithTimeout(ctx, pushTimeout)
	defer cancel()
	sha, err := s.pushEvidence(gctx, run, dir, names, re)
	if err != nil {
		return s.evidenceFailed(run, err)
	}
	private := s.repoPrivate(gctx, run)
	findingImages := map[string]string{}
	for i := range items {
		items[i].url = evidenceURL(run.Repo, sha, path.Join(evidencePath(run), items[i].file), private)
		if items[i].kind == kindFinding && items[i].finding != "" {
			findingImages[items[i].finding] = items[i].url
		}
	}
	s.commentLogf(run, "evidence published at %s: %d files", sha, len(items))
	return evidence{state: "published", commit: sha, items: items, limits: ev.limits, findingImages: findingImages}
}

// publishFindingClips uploads each finding's receipt range. Failed uploads keep their reason in the run state.
func (s *Service) publishFindingClips(ctx context.Context, run Run, v Verdict, re *regexp.Regexp) (map[string]string, string) {
	clips := map[string]string{}
	attempted := 0
	var first error
	for i, f := range v.Findings {
		if run.Kind != "issue" && f.Leg == "base" {
			continue
		}
		name := findingID(i)
		if url := run.VideoURLs[name]; url != "" {
			clips[name] = url
			attempted++
			continue
		}
		file := findingClipPath(filepath.Join(s.store.root, run.ID), i)
		if !fileExists(file) {
			continue
		}
		attempted++
		id, err := s.repoID(ctx, run)
		var url string
		if err == nil {
			url, err = s.uploadVideoFile(ctx, file, name, id, re)
		}
		if err != nil {
			first = cmp.Or(first, err)
			s.commentLogf(run, "video of %s: %v", name, err)
			continue
		}
		clips[name] = url
	}
	if attempted == 0 {
		return clips, "off"
	}
	if len(clips) == attempted {
		return clips, "attached"
	}
	reason, _, _ := strings.Cut(first.Error(), "\n")
	state := "failed:"
	if len(clips) > 0 {
		state = "partial:"
	}
	return clips, state + clip(strings.TrimSpace(reason), 120)
}

func (s *Service) uploadVideoFile(ctx context.Context, file, name string, repoID int64, re *regexp.Regexp) (string, error) {
	if err := checkEgress(re, filepath.Base(file), name); err != nil {
		return "", err
	}
	uctx, cancel := context.WithTimeout(ctx, uploadTimeout)
	out, err := s.ghRun(uctx, "api", "--method", "POST", "-H", "Content-Type: video/mp4",
		fmt.Sprintf("%s?name=%s.mp4&content_type=video%%2Fmp4&repository_id=%d", assetsURL, name, repoID), "--input", file)
	cancel()
	if err != nil {
		return "", err
	}
	var up struct{ URL string }
	if json.Unmarshal(out, &up) != nil || !assetURL.MatchString(up.URL) {
		return "", errors.New("GitHub's answer to the upload had no video link")
	}
	if err := s.verifyVideo(ctx, up.URL); err != nil {
		return "", err
	}
	return up.URL, nil
}

// verifyVideo reads the upload back as the token: it must answer 200 and video/mp4. A fresh asset answers 404 for a
// while, so a 404 is tried once more after videoRetryWait.
func (s *Service) verifyVideo(ctx context.Context, url string) error {
	for retried := false; ; retried = true {
		out, err := s.ghRun(ctx, "api", "-i", url)
		if err == nil {
			status, typ := "", ""
			for i, l := range strings.Split(string(out[:min(len(out), 4<<10)]), "\n") {
				if l = strings.TrimSpace(l); l == "" {
					break
				} else if i == 0 {
					status = l
				} else if k, v, ok := strings.Cut(l, ":"); ok && strings.EqualFold(k, "content-type") {
					typ = strings.TrimSpace(v)
				}
			}
			if f := strings.Fields(status); len(f) < 2 || f[1] != "200" || !strings.HasPrefix(typ, "video/mp4") {
				return fmt.Errorf("GitHub does not serve the upload as a video (%s, %s)", clip(status, 40), clip(typ, 40))
			}
			return nil
		}
		if retried || !strings.Contains(err.Error(), "HTTP 404") {
			return err
		}
		select {
		case <-time.After(videoRetryWait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// repoID is the repo's numeric id, which the upload endpoint wants; gh is asked once per run.
func (s *Service) repoID(ctx context.Context, run Run) (int64, error) {
	if run.RepoID != 0 {
		return run.RepoID, nil
	}
	out, err := s.ghRun(ctx, "api", "repos/"+run.Repo, "--jq", ".id")
	if err != nil {
		return 0, err
	}
	id, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("gh returned no repository id")
	}
	if _, err := s.update(run.ID, func(cur *Run) { cur.RepoID = id }); err != nil {
		slog.Warn("litmus: could not save the repo id", "run", run.ID, "error", err)
	}
	return id, nil
}

func (s *Service) evidenceFailed(run Run, err error) evidence {
	slog.Warn("litmus: evidence not published", "run", run.ID, "error", err)
	s.commentLogf(run, "evidence: %v", err)
	reason, _, _ := strings.Cut(err.Error(), "\n")
	reason = clip(strings.TrimSpace(s.scrub(reason)), 120)
	return evidence{state: "failed:" + reason, limits: []string{"I could not publish the screenshots and recordings. The error was " + mdText(reason) + "."}}
}

// repoPrivate asks gh once whether the repo is private and keeps the answer on the run. When gh cannot say, links
// take the blob form, which serves a public repo too.
func (s *Service) repoPrivate(ctx context.Context, run Run) bool {
	if run.RepoVisibility == "" {
		out, err := s.ghRun(ctx, "repo", "view", run.Repo, "--json", "isPrivate", "--jq", ".isPrivate")
		switch strings.TrimSpace(string(out)) {
		case "true":
			run.RepoVisibility = "private"
		case "false":
			run.RepoVisibility = "public"
		default:
			s.commentLogf(run, "repo visibility unknown (%v); linking evidence in the form that works for both", err)
			return true
		}
		if _, err := s.update(run.ID, func(cur *Run) { cur.RepoVisibility = run.RepoVisibility }); err != nil {
			slog.Warn("litmus: could not save the repo visibility", "run", run.ID, "error", err)
		}
	}
	return run.RepoVisibility == "private"
}

// evidencePath is the run's folder on the evidence branch: runs/<pr number>/<start time, UTC>, with no session id in it.
func evidencePath(run Run) string {
	return path.Join("runs", strconv.Itoa(run.Number()), run.CreatedAt.UTC().Format("20060102-150405"))
}

// pushEvidence commits the staged files to runs/<n>/<start time>/ on the litmus/evidence branch of the repo's dedicated
// clone (<dataDir>/litmus/evidence/<owner>__<repo>, never the user's checkout) and pushes it. The branch is created as
// an orphan when the remote has none, else continued from the remote tip without checking its files out. It returns
// the commit the links pin to.
func (s *Service) pushEvidence(ctx context.Context, run Run, stage string, names []string, re *regexp.Regexp) (sha string, err error) {
	// Pictures and clips cannot be scanned for credentials; visible secrets remain a residual risk.
	if err := checkEgress(re, append([]string{run.Repo, run.ID, evidencePath(run)}, names...)...); err != nil {
		return "", err
	}
	s.evidenceMu.Lock() // runs of one repo share its clone
	defer s.evidenceMu.Unlock()
	clone := filepath.Join(filepath.Dir(s.store.root), "evidence", strings.ReplaceAll(run.Repo, "/", "__"))
	defer func() { // a killed or failed git can leave locks or a half clone behind: the next run starts from a fresh clone
		if err != nil {
			_ = os.RemoveAll(clone)
		}
	}()
	env := append(gitEnv(), "GIT_AUTHOR_NAME=Litmus", "GIT_AUTHOR_EMAIL=litmus@users.noreply.github.com", "GIT_COMMITTER_NAME=Litmus", "GIT_COMMITTER_EMAIL=litmus@users.noreply.github.com")
	g := func(args ...string) ([]byte, error) {
		dir := clone
		if args[0] == "clone" {
			dir = filepath.Dir(clone)
		}
		out, err := s.command(ctx, dir, env, "git", append([]string{"-c", ghCredentials}, args...)...)
		if err != nil {
			s.commentLogf(run, "git %s: %v", strings.Join(args, " "), err)
			err = fmt.Errorf("git %s: %s", args[0], gitReason(err))
		}
		return out, err
	}
	if !fileExists(filepath.Join(clone, ".git", "HEAD")) {
		if err := errors.Join(os.RemoveAll(clone), os.MkdirAll(filepath.Dir(clone), 0o750)); err != nil {
			return "", err
		}
		if _, err := g("clone", "--no-checkout", "--depth", "1", "--filter=blob:none", "https://github.com/"+run.Repo+".git", clone); err != nil {
			return "", err
		}
	}
	ref := "refs/heads/" + evidenceBranch
	remote, err := g("ls-remote", "--heads", "origin", ref)
	if err != nil {
		return "", err
	}
	steps := make([][]string, 0, 5)
	if len(bytes.TrimSpace(remote)) > 0 { // continue the remote branch in the index only, so old evidence is never checked out
		steps = append(steps, []string{"fetch", "--depth", "1", "origin", ref}, []string{"symbolic-ref", "HEAD", ref}, []string{"reset", "-q", "--mixed", "FETCH_HEAD"})
	} else { // a new orphan branch
		steps = append(steps, []string{"symbolic-ref", "HEAD", ref}, []string{"update-ref", "-d", ref}, []string{"read-tree", "--empty"})
	}
	rel := evidencePath(run)
	runs := filepath.Join(clone, "runs")
	_ = os.RemoveAll(runs) // earlier runs stay in the index, not on disk
	defer func() { _ = os.RemoveAll(runs) }()
	if err := os.MkdirAll(filepath.Join(clone, rel), 0o750); err != nil {
		return "", err
	}
	for _, n := range names {
		if err := copyFile(filepath.Join(stage, n), filepath.Join(clone, rel, n)); err != nil {
			return "", err
		}
	}
	// Omit the hash so GitHub does not add an evidence commit reference to the target's timeline.
	steps = append(steps, []string{"add", "-f", "--", rel},
		[]string{"commit", "-q", "--no-gpg-sign", "--no-verify", "-m", fmt.Sprintf("litmus: evidence for %s %d run %s", run.kindNoun(), run.Number(), run.ID)})
	unchanged := false
	for _, step := range steps {
		if step[0] == "commit" {
			changed, err := g("diff", "--cached", "--name-only")
			if err != nil {
				return "", err
			}
			if len(bytes.TrimSpace(changed)) == 0 {
				if commitSHA.MatchString(run.EvidenceCommit) {
					return run.EvidenceCommit, nil
				}
				unchanged = true
				break
			}
		}
		if _, err := g(step...); err != nil {
			return "", err
		}
	}
	out, err := g("rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	sha = strings.TrimSpace(string(out))
	if !commitSHA.MatchString(sha) {
		return "", fmt.Errorf("git rev-parse returned %q", clip(sha, 40))
	}
	if !unchanged {
		if _, err := g("push", "origin", evidenceBranch); err != nil {
			return "", err
		}
	}
	return sha, nil
}

// findingClipRange uses the selected receipt endpoints.
// Three seconds of padding on each side are clamped to the recording.
func findingClipRange(f Finding, receipts []Receipt, started time.Time, span time.Duration, frames ...screencastFrame) (from, duration time.Duration, ok bool) {
	if f.Clip == nil || started.IsZero() || span <= 0 {
		return 0, 0, false
	}
	firstID, lastID := f.Clip.From, f.Clip.To
	if lastID <= 0 || firstID > lastID {
		return 0, 0, false
	}
	var first, last time.Time
	var firstMS int64
	for _, r := range receipts {
		if r.onLeg() != f.Leg {
			continue
		}
		if r.Seq == firstID {
			first, firstMS = r.At, r.MS
		}
		if r.Seq == lastID {
			last = r.At
		}
	}
	// position maps wall time to video time from the last frame before it: wall time after that frame counts
	// (a still screen sends no frames), up to the next frame's offset (a sleep stops the video, not the wall clock).
	position := func(at time.Time) time.Duration {
		offset := func(frame screencastFrame) time.Duration {
			return time.Duration(frameOffset(frame, screencastManifest{StartedWall: started})) * time.Millisecond
		}
		pos := at.Sub(started)
		if len(frames) > 0 && frames[0].WallMS > at.UnixMilli() {
			pos = min(pos, offset(frames[0]))
		}
		for i, frame := range frames {
			if frame.WallMS > 0 && frame.WallMS <= at.UnixMilli() {
				pos = offset(frame) + at.Sub(time.UnixMilli(frame.WallMS))
				if i+1 < len(frames) {
					pos = min(pos, offset(frames[i+1]))
				}
			}
		}
		return pos
	}
	firstAt, lastAt := position(first), position(last)
	if first.IsZero() || last.IsZero() || first.After(last) || first.Before(started) || firstAt > lastAt || lastAt > span {
		return 0, 0, false
	}
	// Receipts mark action completion; include the selected action before padding.
	from = max(position(first.Add(-time.Duration(max(firstMS, 0))*time.Millisecond))-3*time.Second, 0)
	to := min(lastAt+3*time.Second, span)
	return from, to - from, to > from
}
