package litmus

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg" // Register JPEG decoding for evidence images.
	"image/png"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Evidence modes, set on the run by intake. The states a run reports are published, off and failed:<reason>.
const (
	EvidencePost = "post"
	EvidenceOff  = "off"
)

const (
	evidenceBranch = "litmus/evidence"
	stageTimeout   = time.Minute      // ffmpeg for the images
	pushTimeout    = 90 * time.Second // git, and the wait for another run's publish
	maxShots       = 6                // images in one comment
	shotWidth      = 1280             // widest image published
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

type cropBox struct {
	x, y, w, h float64
	vw, vh     float64
}

// receiptCropBox uses the click target recorded with the receipt. Older
// receipts have no box and intentionally use the full screenshot.
func receiptCropBox(r Receipt) (cropBox, bool) {
	if r.Box == nil || r.Box.W <= 0 || r.Box.H <= 0 {
		return cropBox{}, false
	}
	return cropBox{
		x: r.Box.X, y: r.Box.Y, w: r.Box.W, h: r.Box.H,
		vw: r.Box.ViewportWidth, vh: r.Box.ViewportHeight,
	}, true
}

// shot is a receipt's after-shot; label is the check in words.
type shot struct {
	label string
	rcpt  Receipt
	theme string
}

// hero is one table row's lead image: main and the PR build side by side, or whichever side there is.
type hero struct {
	label            string
	pr, base         *shot
	prNote, baseNote string // what the tester saw on each side, for the caption
}

// lastAfterShot is the check's last receipt on leg whose after-shot file is still there.
func lastAfterShot(c Check, leg string) (Receipt, bool) {
	for i := len(c.Receipts) - 1; i >= 0; i-- {
		if r := c.Receipts[i]; r.onLeg() == leg && fileExists(r.AfterPath) {
			return r, true
		}
	}
	return Receipt{}, false
}

// headAfterShot finds a saved PR after-shot by its run-wide receipt number.
func headAfterShot(checks []Check, seq int) (Receipt, bool) {
	if seq > 0 {
		for _, c := range checks {
			for _, r := range c.Receipts {
				if r.Seq == seq && r.onLeg() == "head" && fileExists(r.AfterPath) {
					return r, true
				}
			}
		}
	}
	return Receipt{}, false
}

// bugImageShot prefers the final receipt's check-end theme shot, without a crop.
func bugImageShot(c Check, leg string) (r Receipt, theme, ok bool) {
	for i := len(c.Receipts) - 1; i >= 0; i-- {
		r = c.Receipts[i]
		if r.onLeg() != leg {
			continue
		}
		for _, preference := range []string{"light", "dark"} {
			for _, shot := range r.ThemeShots {
				if shot.Theme == preference && fileExists(shot.Path) {
					r.AfterPath, r.Box = shot.Path, nil
					return r, true, true
				}
			}
		}
		break
	}
	r, ok = lastAfterShot(c, leg)
	return r, false, ok
}

func openEvidenceImage(file string) (image.Image, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	img, _, err := image.Decode(f)
	return img, err
}

func cropRect(bounds image.Rectangle, b cropBox) image.Rectangle {
	if b.w <= 0 || b.h <= 0 {
		return bounds
	}
	sx, sy := 1.0, 1.0
	if b.vw > 0 {
		sx = float64(bounds.Dx()) / b.vw
	}
	if b.vh > 0 {
		sy = float64(bounds.Dy()) / b.vh
	}
	cx, cy := (b.x+b.w/2)*sx+float64(bounds.Min.X), (b.y+b.h/2)*sy+float64(bounds.Min.Y)
	w, h := max(b.w*sx+80, 600), max(b.h*sy+80, 400)
	const aspect = 1.5
	if w/h < aspect {
		w = h * aspect
	} else {
		h = w / aspect
	}
	if w > float64(bounds.Dx()) {
		w = float64(bounds.Dx())
	}
	if h > float64(bounds.Dy()) {
		h = float64(bounds.Dy())
	}
	left, top := cx-w/2, cy-h/2
	if left < float64(bounds.Min.X) {
		left = float64(bounds.Min.X)
	}
	if top < float64(bounds.Min.Y) {
		top = float64(bounds.Min.Y)
	}
	if left+w > float64(bounds.Max.X) {
		left = float64(bounds.Max.X) - w
	}
	if top+h > float64(bounds.Max.Y) {
		top = float64(bounds.Max.Y) - h
	}
	return image.Rect(int(left), int(top), int(left+w), int(top+h)).Intersect(bounds)
}

func cropEvidenceImage(src image.Image, b cropBox, hasBox bool) image.Image {
	if !hasBox {
		return src
	}
	r := cropRect(src.Bounds(), b)
	if sub, ok := src.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return sub.SubImage(r)
	}
	return src
}

func scaleEvidenceImage(src image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	scale := min(float64(w)/float64(src.Bounds().Dx()), float64(h)/float64(src.Bounds().Dy()))
	dw, dh := max(1, int(float64(src.Bounds().Dx())*scale)), max(1, int(float64(src.Bounds().Dy())*scale))
	ox, oy := (w-dw)/2, (h-dh)/2
	for y := 0; y < dh; y++ {
		sy := src.Bounds().Min.Y + y*src.Bounds().Dy()/dh
		for x := 0; x < dw; x++ {
			sx := src.Bounds().Min.X + x*src.Bounds().Dx()/dw
			dst.Set(ox+x, oy+y, src.At(sx, sy))
		}
	}
	return dst
}

// makeBugImage writes one 1200x400 comparison image, with main on the left
// and the PR on the right. Check-end theme shots keep the full frame unless an
// explicit PR receipt selects a matching region on the base theme shot.
func makeBugImage(b bugComment, checks []Check, out string) error {
	pr, _, prOK := bugImageShot(b.Check, "head")
	if b.Check.Shot > 0 {
		pr, prOK = headAfterShot(checks, b.Check.Shot)
	}
	if !prOK {
		return errors.New("bug has no PR after-shot")
	}
	base, baseTheme, baseOK := Receipt{}, false, false
	if b.Base != nil {
		base, baseTheme, baseOK = bugImageShot(*b.Base, "base")
	}
	if !baseOK {
		base, baseTheme, baseOK = bugImageShot(b.Check, "base")
	}
	if !baseOK {
		return errors.New("bug has no base after-shot")
	}
	left, err := openEvidenceImage(base.AfterPath)
	if err != nil {
		return err
	}
	right, err := openEvidenceImage(pr.AfterPath)
	if err != nil {
		return err
	}
	lb, lok := receiptCropBox(base)
	rb, rok := receiptCropBox(pr)
	if b.Check.Shot > 0 && baseTheme {
		lb, lok = rb, rok
		if lb.vw <= 0 {
			lb.vw = float64(right.Bounds().Dx())
		}
		if lb.vh <= 0 {
			lb.vh = float64(right.Bounds().Dy())
		}
	}
	left = cropEvidenceImage(left, lb, lok)
	right = cropEvidenceImage(right, rb, rok)
	canvas := image.NewRGBA(image.Rect(0, 0, 1200, 400))
	draw.Draw(canvas, image.Rect(0, 0, 600, 400), scaleEvidenceImage(left, 600, 400), image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(600, 0, 1200, 400), scaleEvidenceImage(right, 600, 400), image.Point{}, draw.Src)
	if err := os.MkdirAll(filepath.Dir(out), 0o750); err != nil {
		return err
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	err = png.Encode(f, canvas)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// baseTwin is the index of the base-leg check that gives head check c its base result, or -1.
func baseTwin(checks []Check, c Check) int {
	return slices.IndexFunc(checks, func(b Check) bool {
		return b.onLeg() == "base" && strings.EqualFold(b.Name, c.Name) && slices.Contains(outcomes, b.State)
	})
}

// planShots picks, per table row, the last after-shot on each side (the base twin's, or the row's own base receipts)
// for its lead image, then every other after-shot as an extra, in check order, at most maxShots images in all.
func planShots(checks []Check) (heroes []hero, extras []shot) {
	used := map[int]bool{}
	pick := func(c Check, leg, label string) *shot {
		r, ok := lastAfterShot(c, leg)
		if ok {
			used[r.Seq] = true
			return &shot{label: label, rcpt: r}
		}
		return nil
	}
	for _, c := range checks {
		if _, _, beside := compareCells(checks, c); beside || len(heroes) == maxShots {
			continue
		}
		h := hero{label: checkLabel(c)}
		switch {
		case c.onLeg() == "base":
			h.base, h.baseNote = pick(c, "base", h.label), c.Note
		default:
			if !c.unreached() {
				h.pr, h.prNote = pick(c, "head", h.label), c.Note
			}
			if i := baseTwin(checks, c); i >= 0 {
				h.base, h.baseNote = pick(checks[i], "base", h.label), checks[i].Note
			} else if baseResult(checks, c) != "" {
				h.base = pick(c, "base", h.label) // "observed": the head check also looked at the base
			}
		}
		if h.pr != nil || h.base != nil {
			heroes = append(heroes, h)
		}
	}
	for _, c := range checks {
		for _, r := range c.Receipts {
			if !used[r.Seq] && fileExists(r.AfterPath) && len(heroes)+len(extras) < maxShots {
				extras = append(extras, shot{label: rowLabel(checks, c), rcpt: r})
			}
		}
	}
	// Theme pairs are required evidence, so the cap on optional step shots cannot
	// discard a check's light or dark capture.
	for _, c := range checks {
		for _, r := range c.Receipts {
			for _, theme := range r.ThemeShots {
				if fileExists(theme.Path) {
					pair := r
					pair.AfterPath = theme.Path
					extras = append(extras, shot{label: rowLabel(checks, c), rcpt: pair, theme: theme.Theme})
				}
			}
		}
	}
	return heroes, extras
}

// evItem is one image in the comment: file is its name in the stage dir and in the repo, kind where it goes.
type evItem struct {
	file, alt, caption, url string
	check                   string // non-empty for one check's bug image
	kind                    int
}

// Where an image goes: lead images follow the table, recordings and extra screenshots sit in the details block.
const (
	kindHero = iota
	kindGIF
	kindShot
	kindTheme // required check-end evidence; captions do not consume the prose cap
	kindBug   // one cropped comparison image for one PR-introduced bug
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
	bugImages     map[string]string // check name -> published URL
	checkClips    map[string]string // check name -> published or local clip URL
}

// videoURLs are the attached videos' links by leg, for the Run.
func (e evidence) videoURLs() (m map[string]string) {
	if len(e.checkClips) > 0 {
		m = make(map[string]string, len(e.checkClips))
		for name, url := range e.checkClips {
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

// imageChars counts image lines and required theme captions, which do not consume the comment's prose cap.
func (e evidence) imageChars() (n int) {
	for _, it := range e.items {
		n += len(it.image()) + 1
		if it.kind == kindTheme {
			n += len(it.caption) + 2
		}
	}
	return n
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

func pngArgs(in, out string) []string {
	return []string{"-y", "-i", in, "-vf", fmt.Sprintf("scale='min(%d,iw)':-2", shotWidth), "-frames:v", "1", out}
}

// compositeArgs puts the base shot left of the head shot, each scaled to 640 wide (this ffmpeg has no drawtext, so
// the sides are named in the caption, not on the image). xstack, unlike hstack, accepts shots of different heights.
func compositeArgs(base, head, out string) []string {
	return []string{"-y", "-i", base, "-i", head, "-filter_complex", "[0:v]scale=640:-2[l];[1:v]scale=640:-2[r];[l][r]xstack=inputs=2:layout=0_0|w0_0:fill=white",
		"-frames:v", "1", out}
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
		if err := s.media.Run(ctx, gifArgs(in, out, fps)...); err != nil {
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

// stageEvidence writes the lead image of each check (a side-by-side when both sides have a shot), the extra
// after-shots and one GIF per leg into dir. A file that cannot be made is left out and logged; first is the first
// such error.
func (s *Service) stageEvidence(ctx context.Context, run Run, v Verdict, dir string) (items []evItem, limits []string, first error) {
	note := func(err error) {
		s.commentLogf(run, "evidence: %v", err)
		if first == nil {
			first = err
		}
	}
	taken := map[string]bool{}
	name := func(label, suffix string) string { // arrows-visible-main-vs-pr.png: the check in words, unique in the set
		stem, f := cmp.Or(slug(label), "check")+"-"+suffix, ""
		for n := 1; f == "" || taken[f]; n++ {
			f = fmt.Sprintf("%s-%d.png", stem, n)
			if n == 1 {
				f = stem + ".png"
			}
		}
		taken[f] = true
		return f
	}
	main := run.baseName()
	v = plainVerdict(run, v)
	bugs := failingBugComments(v.Checks)
	s.numberBugComments(run, bugs)
	for _, b := range bugs {
		file := fmt.Sprintf("bug-%d.png", b.Number)
		local := filepath.Join(s.store.root, run.ID, "comments", file)
		var err error
		if fileExists(local) {
			err = copyFile(local, filepath.Join(dir, file))
		} else {
			err = makeBugImage(b, v.Checks, filepath.Join(dir, file))
		}
		if err != nil {
			note(err)
			continue
		}
		items = append(items, evItem{file: file, alt: "base branch on the left, PR build on the right", caption: fmt.Sprintf("Bug %d of %d: %s", b.Number, b.Total, b.Check.Name), check: b.Check.Name, kind: kindBug})
	}
	heroes, extras := planShots(v.Checks)
	for _, h := range heroes {
		if h.pr != nil && h.base != nil {
			f := name(h.label, slug(main)+"-vs-pr")
			err := s.media.Run(ctx, compositeArgs(h.base.rcpt.AfterPath, h.pr.rcpt.AfterPath, filepath.Join(dir, f))...)
			if err == nil {
				items = append(items, evItem{file: f, alt: "base branch on the left, PR build on the right", caption: heroCaption(run, h), kind: kindHero})
				continue
			}
			note(err)
			limits = append(limits, "I could not build the side-by-side image for "+lowerFirst(h.label)+", so it shows the PR build only.")
			h.base = nil
		}
		sh := cmp.Or(h.pr, h.base)
		f := name(h.label, sideSlug(run, sh.rcpt.onLeg()))
		if err := s.media.Run(ctx, pngArgs(sh.rcpt.AfterPath, filepath.Join(dir, f))...); err != nil {
			note(err)
			limits = append(limits, "I could not prepare the screenshot for "+lowerFirst(h.label)+".")
			continue
		}
		items = append(items, evItem{file: f, alt: sideAlt(sh.rcpt.onLeg()), caption: heroCaption(run, h), kind: kindHero})
	}
	for _, x := range extras {
		leg := x.rcpt.onLeg()
		suffix := fmt.Sprintf("%s-step-%d", sideSlug(run, leg), x.rcpt.Seq)
		if x.theme != "" {
			suffix = sideSlug(run, leg) + "-" + x.theme + "-preference"
		}
		f := name(x.label, suffix)
		args := pngArgs(x.rcpt.AfterPath, filepath.Join(dir, f))
		if x.theme != "" {
			args = []string{"-y", "-i", x.rcpt.AfterPath, "-frames:v", "1", filepath.Join(dir, f)}
		}
		if err := s.media.Run(ctx, args...); err != nil {
			note(err)
			limits = append(limits, "I could not prepare a screenshot for "+lowerFirst(x.label)+".")
			continue
		}
		args = x.rcpt.Args // what was typed stays out of a public comment
		if x.rcpt.Action != "press" {
			args = args[:min(len(args), 1)]
		}
		step := fmt.Sprintf("Step %d, %s: %s", x.rcpt.Seq, sideLabel(run, leg), mdText(stepText(x.rcpt.Action, args, x.rcpt.TargetRole, x.rcpt.TargetName), 100))
		alt := ""
		kind := kindShot
		if x.theme != "" {
			step = x.label + ", " + sideLabel(run, leg) + ", " + x.theme + " system theme preference. Full retina size; embedded browser pages are separate targets and never appear in these shots."
			alt = step
			kind = kindTheme
		}
		items = append(items, evItem{file: f, alt: alt, caption: step, kind: kind})
	}
	for _, leg := range []string{"head", "base"} {
		cut := cutPath(filepath.Dir(dir), leg)
		if !fileExists(cut) {
			continue
		}
		file := sideSlug(run, leg) + ".gif"
		made, err := s.makeGIF(ctx, cut, filepath.Join(dir, file))
		switch {
		case err != nil:
			note(err)
			limits = append(limits, "I could not make the recording of "+run.sideName(leg)+".")
		case !made:
			limits = append(limits, fmt.Sprintf("I left out the recording of %s because it was over %d MB even at 4 fps.", run.sideName(leg), gifCap>>20))
		default:
			items = append(items, evItem{file: file, alt: sideAlt(leg) + " recording", caption: sideLabel(run, leg) + ", the moments around each step", kind: kindGIF})
		}
	}
	return items, limits, first
}

// heroCaption names the sides of a lead image, since the side-by-side has no labels on it, and says what each side
// showed in the tester's words, 90 characters each (the plain names alone when a note is missing).
func heroCaption(run Run, h hero) string {
	if h.pr != nil && h.base != nil {
		left, right := mdText(clause(sentences(h.baseNote, 90), 90), 200), mdText(clause(sentences(h.prNote, 90), 90), 200)
		if left == "" || right == "" {
			return recase(run.baseName(), true) + " on the left, PR build on the right."
		}
		return captionNote(run, recase(run.baseName(), true)+" on the left", left) + ". " + captionNote(run, "PR build on the right", right) + "."
	}
	leg, note := "base", h.baseNote
	if h.pr != nil {
		leg, note = "head", h.prNote
	}
	if note = mdText(clause(sentences(note, 200), 200), 400); note == "" {
		return recase(run.sideName(leg), true) + "."
	}
	return captionNote(run, sideLabel(run, leg), note) + "."
}

// captionNote joins a side's label and what the tester saw: "PR build: opening a project enabled Go back", or, when the
// note itself starts with a build as its subject, a full stop so the build is not named twice.
func captionNote(run Run, label, note string) string {
	if subjectLed(note, run) {
		return label + ". " + recase(note, true)
	}
	return label + ": " + lowerFirst(note)
}

// sideSlug is a leg in file names: pr, or the base branch.
func sideSlug(run Run, leg string) string {
	if leg != "base" {
		return "pr"
	}
	if s := slug(run.baseName()); s != "" && s != "pr" {
		return s
	}
	return "base"
}

// sideAlt is a leg in image alt text, where a branch name could close the image syntax early.
func sideAlt(leg string) string {
	return map[bool]string{true: "base branch", false: "PR build"}[leg == "base"]
}

// sideLabel names a leg at the start of a caption: "PR build" or the base branch with a capital.
func sideLabel(run Run, leg string) string {
	return recase(strings.TrimPrefix(run.sideName(leg), "the "), true)
}

// slug is text as a file name stem: lower-case words joined by dashes, at most 40 characters, cut at a word.
func slug(s string) string {
	s = strings.Trim(nonWord.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 40 {
		s = s[:40]
		if i := strings.LastIndex(s, "-"); i > 20 {
			s = s[:i]
		}
	}
	return s
}

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

// publishEvidence prepares the comment's images and publishes them to the repo's litmus/evidence branch. It never
// fails the submit: whatever goes wrong becomes the state failed:<reason> and a Limits line.
func (s *Service) publishEvidence(ctx context.Context, run Run, v Verdict) evidence {
	if run.Evidence != EvidencePost || len(failingBugComments(v.Checks)) == 0 {
		return evidence{state: EvidenceOff}
	}
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
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.file
	}
	gctx, cancel := context.WithTimeout(ctx, pushTimeout)
	defer cancel()
	sha, err := s.pushEvidence(gctx, run, dir, names)
	if err != nil {
		return s.evidenceFailed(run, err)
	}
	private := s.repoPrivate(gctx, run)
	bugImages := map[string]string{}
	for i := range items {
		items[i].url = evidenceURL(run.Repo, sha, path.Join(evidencePath(run), items[i].file), private)
		if items[i].kind == kindBug && items[i].check != "" {
			bugImages[items[i].check] = items[i].url
		}
	}
	s.commentLogf(run, "evidence published at %s: %d files", sha, len(items))
	return evidence{state: "published", commit: sha, items: items, limits: limits, bugImages: bugImages}
}

// publishCheckClips uploads each failing check's own PR clip. Local draft
// links are replaced only after GitHub serves the attachment back as video.
func (s *Service) publishCheckClips(ctx context.Context, run Run, v Verdict) (map[string]string, string) {
	if run.Evidence != EvidencePost {
		return nil, "off"
	}
	clips := make(map[string]string)
	attempted := 0
	var first error
	for _, bug := range failingBugComments(v.Checks) {
		file := checkClipPath(filepath.Join(s.store.root, run.ID), bug.Check.ID)
		if !fileExists(file) {
			continue
		}
		attempted++
		id, err := s.repoID(ctx, run)
		var url string
		if err == nil {
			url, err = s.uploadVideoFile(ctx, file, "check-"+bug.Check.ID, id)
		}
		if err != nil {
			first = cmp.Or(first, err)
			s.commentLogf(run, "video of check %s: %v", bug.Check.Name, err)
			continue
		}
		clips[bug.Check.Name] = url
	}
	if attempted == 0 {
		return clips, "off"
	}
	if len(clips) == attempted {
		return clips, "attached"
	}
	if len(clips) > 0 {
		return clips, "partial"
	}
	reason, _, _ := strings.Cut(s.scrub(first.Error()), "\n")
	return clips, "failed:" + clip(strings.TrimSpace(reason), 120)
}

func (s *Service) uploadVideoFile(ctx context.Context, file, name string, repoID int64) (string, error) {
	uctx, cancel := context.WithTimeout(ctx, uploadTimeout)
	out, err := s.gh.Run(uctx, "api", "--method", "POST", "-H", "Content-Type: video/mp4",
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
		out, err := s.gh.Run(ctx, "api", "-i", url)
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
	out, err := s.gh.Run(ctx, "api", "repos/"+run.Repo, "--jq", ".id")
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

// spanText is a length as a reader says it: 15 s, 1 min 19 s, 2 min. Empty for none.
func spanText(d time.Duration) string {
	switch s := int(d.Round(time.Second) / time.Second); {
	case s <= 0:
		return ""
	case s < 60:
		return fmt.Sprintf("%d s", s)
	case s%60 == 0:
		return fmt.Sprintf("%d min", s/60)
	default:
		return fmt.Sprintf("%d min %d s", s/60, s%60)
	}
}

func (s *Service) evidenceFailed(run Run, err error) evidence {
	slog.Warn("litmus: evidence not published", "run", run.ID, "error", err)
	s.commentLogf(run, "evidence: %v", err)
	reason, _, _ := strings.Cut(s.scrub(err.Error()), "\n")
	reason = clip(strings.TrimSpace(reason), 120)
	return evidence{state: "failed:" + reason, limits: []string{"I could not publish the screenshots and recordings. The error was " + mdText(reason, 120) + "."}}
}

// scrub hides the data dir and the home dir in a message that goes into a public comment or the pack.
func (s *Service) scrub(msg string) string {
	if d := s.store.dataDir(); d != "" {
		msg = strings.ReplaceAll(msg, d, "<data dir>")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		msg = strings.ReplaceAll(msg, home, "~")
	}
	return msg
}

// repoPrivate asks gh once whether the repo is private and keeps the answer on the run. When gh cannot say, links
// take the blob form, which serves a public repo too.
func (s *Service) repoPrivate(ctx context.Context, run Run) bool {
	if run.RepoVisibility == "" {
		out, err := s.gh.Run(ctx, "repo", "view", run.Repo, "--json", "isPrivate", "--jq", ".isPrivate")
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

// gitAuthFailure reports whether a git error is a missing credential, which `gh auth setup-git` can fix.
func gitAuthFailure(err error) bool {
	low := strings.ToLower(err.Error())
	return strings.Contains(low, "could not read username") || strings.Contains(low, "authentication failed") || strings.Contains(low, "terminal prompts disabled")
}

// gitReason is the last line git printed, which is where it says what went wrong.
func gitReason(err error) string {
	lines := strings.Split(strings.TrimSpace(err.Error()), "\n")
	return clip(strings.TrimSpace(lines[len(lines)-1]), 160)
}

// evidencePath is the run's folder on the evidence branch: runs/<pr number>/<start time, UTC>, with no session id in it.
func evidencePath(run Run) string {
	return path.Join("runs", strconv.Itoa(run.PRNumber), run.CreatedAt.UTC().Format("20060102-150405"))
}

// pushEvidence commits the staged files to runs/<n>/<start time>/ on the litmus/evidence branch of the repo's dedicated
// clone (<dataDir>/litmus/evidence/<owner>__<repo>, never the user's checkout) and pushes it. The branch is created as
// an orphan when the remote has none, else continued from the remote tip without checking its files out. It returns
// the commit the links pin to.
func (s *Service) pushEvidence(ctx context.Context, run Run, stage string, names []string) (sha string, err error) {
	s.evidenceMu.Lock() // runs of one repo share its clone
	defer s.evidenceMu.Unlock()
	clone := filepath.Join(filepath.Dir(s.store.root), "evidence", strings.ReplaceAll(run.Repo, "/", "__"))
	defer func() { // a killed or failed git can leave locks or a half clone behind: the next run starts from a fresh clone
		if err != nil {
			_ = os.RemoveAll(clone)
		}
	}()
	env := append(baseEnv(""), "GIT_AUTHOR_NAME=Litmus", "GIT_AUTHOR_EMAIL=litmus@users.noreply.github.com", "GIT_COMMITTER_NAME=Litmus", "GIT_COMMITTER_EMAIL=litmus@users.noreply.github.com")
	g := func(args ...string) ([]byte, error) {
		dir := clone
		if args[0] == "clone" {
			dir = filepath.Dir(clone)
		}
		out, err := s.runner.Run(ctx, dir, env, "git", args...)
		if err != nil && gitAuthFailure(err) && slices.Contains([]string{"clone", "ls-remote", "fetch", "push"}, args[0]) {
			if _, serr := s.gh.Run(ctx, "auth", "setup-git"); serr != nil { // the user's gh login is how git gets its credentials
				s.commentLogf(run, "gh auth setup-git: %v", serr)
			} else {
				out, err = s.runner.Run(ctx, dir, env, "git", args...) // once
			}
		}
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
	// "PR 7", not "#7": GitHub would list the commit on the PR's timeline as a reference.
	steps = append(steps, []string{"add", "-f", "--", rel},
		[]string{"commit", "-q", "--no-gpg-sign", "--no-verify", "-m", fmt.Sprintf("litmus: evidence for PR %d run %s", run.PRNumber, run.ID)})
	for _, step := range steps {
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
	if _, err := g("push", "origin", evidenceBranch); err != nil {
		return "", err
	}
	return sha, nil
}
