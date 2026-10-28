package litmus

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

// verdictInput contains only fields the tester owns.
type verdictInputData struct {
	Result      string     `json:"result"`
	Headline    string     `json:"headline"`
	Findings    []Finding  `json:"findings"`
	Coverage    []Coverage `json:"coverage"`
	Environment string     `json:"environment"`
	BlockedOn   string     `json:"blocked_on,omitempty"`
	NotTested   string     `json:"not_tested,omitempty"`
}

// Submit accepts the tester's JSON under scratch and obeys the project's posting setting.
func (s *Service) Submit(ctx context.Context, sessionID, verdictPath string) (Verdict, error) {
	run, done, err := s.begin(sessionID, "submit")
	if err != nil {
		return Verdict{}, err
	}
	defer done()
	v, data, err := s.readVerdict(run, verdictPath)
	if err == nil && v.Result != "blocked" && v.BlockedOn != "" { // the tester is still here to fix it
		err = invalid("TESTING_VERDICT_BLOCKED_ON", "blocked_on is only for blocked results; use not_tested for scope, version or platform limits")
	}
	if err != nil {
		return Verdict{}, err
	}
	return s.closeRun(ctx, run, v, data)
}

// readVerdict applies the same path, JSON and evidence checks to submit and deadline salvage.
func (s *Service) readVerdict(run Run, verdictPath string) (Verdict, []byte, error) {
	runDir := filepath.Join(s.store.root, run.ID)
	path := verdictPath
	if !filepath.IsAbs(path) {
		path = filepath.Join(runDir, path)
	}
	scratch, err := filepath.EvalSymlinks(filepath.Join(runDir, "scratch"))
	if err != nil {
		return Verdict{}, nil, fmt.Errorf("read verdict scratch directory: %w", err)
	}
	root, err := filepath.EvalSymlinks(runDir)
	if err != nil {
		return Verdict{}, nil, err
	}
	if rel, err := filepath.Rel(root, scratch); err != nil || !filepath.IsLocal(rel) || rel == "." {
		return Verdict{}, nil, invalid("TESTING_VERDICT_PATH", "verdict file must be under this run's scratch/")
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return Verdict{}, nil, fmt.Errorf("read verdict: %w", err)
	}
	if rel, err := filepath.Rel(scratch, path); err != nil || !filepath.IsLocal(rel) || rel == "." {
		return Verdict{}, nil, invalid("TESTING_VERDICT_PATH", "verdict file must be under this run's scratch/")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Verdict{}, nil, fmt.Errorf("read verdict: %w", err)
	}
	var input verdictInputData
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil {
		return Verdict{}, nil, invalid("TESTING_VERDICT_INVALID", "invalid verdict JSON: "+err.Error())
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return Verdict{}, nil, invalid("TESTING_VERDICT_INVALID", "verdict JSON must contain one object")
	}
	v := Verdict{Result: input.Result, Headline: input.Headline, Findings: input.Findings, Coverage: input.Coverage, Environment: input.Environment, BlockedOn: input.BlockedOn, NotTested: input.NotTested}
	receipts, err := s.store.Receipts(run.ID)
	if err != nil {
		return Verdict{}, nil, err
	}
	if err := validateVerdict(run, v, receipts); err != nil {
		return Verdict{}, nil, err
	}
	return v, data, nil
}

func validateVerdict(run Run, v Verdict, receipts []Receipt) error {
	launched := func(name string) bool {
		return slices.ContainsFunc(run.Legs, func(l Leg) bool {
			return l.Name == name && (l.State == "running" || l.Engine != nil || !l.RecordStartedAt.IsZero())
		})
	}
	leg := func(name string) error {
		if !launched(name) {
			return invalid("TESTING_VERDICT_LEG", "leg "+name+" never ran")
		}
		return nil
	}
	if run.Kind == "issue" {
		if v.Result != "reproduced" && v.Result != "not_reproduced" && v.Result != "blocked" {
			return invalid("TESTING_VERDICT_INVALID", "issue result must be reproduced, not_reproduced or blocked")
		}
	} else if v.Result != "pass" && v.Result != "fail" && v.Result != "partial" && v.Result != "blocked" {
		return invalid("TESTING_VERDICT_INVALID", "PR result must be pass, fail, partial or blocked")
	}
	required := "head"
	if run.Kind == "issue" {
		required = "main"
	}
	if v.Result != "blocked" {
		if err := leg(required); err != nil {
			return err
		}
	}
	for _, f := range v.Findings {
		if err := leg(f.Leg); err != nil {
			return err
		}
	}
	for _, c := range v.Coverage {
		if err := leg(c.Leg); err != nil {
			return err
		}
	}
	exists := func(name string, id int) bool {
		return slices.ContainsFunc(receipts, func(r Receipt) bool { return r.onLeg() == name && r.Seq == id })
	}
	for i, f := range v.Findings {
		if f.Severity != "bug" && f.Severity != "worth-a-look" {
			return invalid("TESTING_VERDICT_SEVERITY", fmt.Sprintf("finding %d: severity must be bug or worth-a-look, got %q", i+1, f.Severity))
		}
		if f.Severity == "bug" && !slices.ContainsFunc(f.Receipts, func(id int) bool { return exists(f.Leg, id) }) {
			return invalid("TESTING_VERDICT_BUG_RECEIPT", fmt.Sprintf("finding %d: bug needs an existing receipt on %s", i+1, f.Leg))
		}
	}
	// Each result word has one agreement rule, checked against the required leg.
	rule := map[string]struct{ bug, noBug, coverage, trigger, blocked bool }{
		"fail": {bug: true}, "reproduced": {bug: true},
		"pass": {noBug: true, coverage: true, trigger: true}, "not_reproduced": {noBug: true, coverage: true, trigger: true},
		"partial": {coverage: true}, "blocked": {noBug: true, blocked: true},
	}[v.Result]
	hasBug := slices.ContainsFunc(v.Findings, func(f Finding) bool {
		return f.Leg == required && f.Severity == "bug" && slices.ContainsFunc(f.Receipts, func(id int) bool { return exists(required, id) })
	})
	if rule.bug && !hasBug {
		return invalid("TESTING_VERDICT_REPRODUCTION", v.Result+" needs a bug finding with an existing receipt on "+required)
	}
	if rule.noBug && hasBug {
		return invalid("TESTING_VERDICT_CONTRADICTION", v.Result+" forbids a receipted bug on "+required)
	}
	if rule.coverage && !slices.ContainsFunc(v.Coverage, func(c Coverage) bool {
		return c.Leg == required && (!rule.trigger || c.Trigger) && slices.ContainsFunc(c.Receipts, func(id int) bool { return exists(required, id) })
	}) {
		item := "a coverage item"
		if rule.trigger {
			item = "the trigger coverage item (trigger: true)"
		}
		return invalid("TESTING_VERDICT_COVERAGE", v.Result+" needs "+item+" with an existing receipt on "+required)
	}
	if rule.blocked && strings.TrimSpace(v.BlockedOn) == "" {
		return invalid("TESTING_VERDICT_BLOCKED_ON", "blocked needs blocked_on")
	}
	for i, f := range v.Findings {
		for _, id := range f.Receipts {
			if !exists(f.Leg, id) {
				return invalid("TESTING_VERDICT_RECEIPT", fmt.Sprintf("receipt %d does not exist on leg %s", id, f.Leg))
			}
		}
		if f.Severity == "bug" && f.Clip == nil {
			return invalid("TESTING_VERDICT_CLIP", fmt.Sprintf("finding %d: bug needs clip: {from, to} receipt ids on %s", i+1, f.Leg))
		}
		if f.Clip != nil {
			if !exists(f.Leg, f.Clip.From) {
				return invalid("TESTING_VERDICT_RECEIPT", fmt.Sprintf("finding %d: clip from receipt %d does not exist on leg %s", i+1, f.Clip.From, f.Leg))
			}
			if !exists(f.Leg, f.Clip.To) {
				return invalid("TESTING_VERDICT_RECEIPT", fmt.Sprintf("finding %d: clip to receipt %d does not exist on leg %s", i+1, f.Clip.To, f.Leg))
			}
			if f.Clip.From > f.Clip.To {
				return invalid("TESTING_VERDICT_RECEIPT", fmt.Sprintf("finding %d: clip from receipt %d must not follow to receipt %d on leg %s", i+1, f.Clip.From, f.Clip.To, f.Leg))
			}
		}
	}
	for _, c := range v.Coverage {
		for _, id := range c.Receipts {
			if !exists(c.Leg, id) {
				return invalid("TESTING_VERDICT_RECEIPT", fmt.Sprintf("receipt %d does not exist on leg %s", id, c.Leg))
			}
		}
	}
	return nil
}

// closeRun stops captures before targets, writes the fixed reports and reports the tester done.
// Internal expiry and prepare timeout call it with a blocked verdict without tester JSON.
func (s *Service) closeRun(ctx context.Context, run Run, v Verdict, data []byte) (Verdict, error) {
	ctx = context.WithoutCancel(ctx)
	runDir := filepath.Join(s.store.root, run.ID)
	receipts, err := s.store.Receipts(run.ID)
	if err != nil {
		return Verdict{}, err
	}
	v.ReceiptCount = len(receipts)
	if data == nil {
		data, err = json.MarshalIndent(v, "", "  ")
		if err != nil {
			return Verdict{}, err
		}
	}
	if err := s.store.WriteFile(run.ID, verdictFile, data); err != nil {
		return Verdict{}, err
	}
	run = s.stopRecording(ctx, run)
	videos := s.finishVideo(ctx, runDir, run, v)
	cleanErr := s.teardown(ctx, run)
	if cleanErr != nil {
		slog.Warn("litmus: cleanup after submit failed", "run", run.ID, "error", cleanErr)
	}
	re := s.credentialPattern(ctx)
	body, ev, err := s.composeComment(run, v, re)
	if err != nil {
		return Verdict{}, fmt.Errorf("write comment.md: %w", err)
	}
	md := renderVerdict(run, v, receipts, runDir, s.ownerLoginStatus(ctx, run), ev, s.scrub)
	if cleanErr != nil {
		md += "\nCleanup after submit failed: " + mdText(s.scrub(cleanErr.Error())) + "\n"
	}
	ask, askErr := s.mustAsk(run, v)
	if askErr != nil {
		line := "Comment awaiting approval: " + askErr.Error()
		md += "\n" + mdText(s.scrub(line)) + "\n"
		if err := s.store.appendRunLog(run.ID, line); err != nil {
			return Verdict{}, errors.Join(askErr, fmt.Errorf("write approval failure to run.log: %w", err))
		}
	}
	if err := s.store.WriteFile(run.ID, "verdict.md", []byte(md)); err != nil {
		return Verdict{}, err
	}
	if err := publishArtifacts(sessionartifacts.Dir(s.store.dataDir(), domain.SessionID(run.SessionID)), runDir, md, renderIndex(run, v, videos, runDir, ev.clipSpans), videos); err != nil {
		return Verdict{}, fmt.Errorf("copy evidence to the session artifact dir: %w", err)
	}
	v.CommentState, v.EvidenceState = CommentOff, ev.state
	state := RunSubmitted
	var published evidence
	if ask {
		state, v.CommentState = RunAwaitingApproval, CommentAwaiting
	} else if run.Comment == CommentPost && v.Result != "blocked" {
		v.CommentURL, v.CommentState, published = s.publishComment(ctx, run, v, body, re, run.Evidence == EvidencePost)
		v.EvidenceState, v.EvidenceCommit = published.state, published.commit
		if v.CommentURL == "" && strings.HasPrefix(v.CommentState, "failed:") {
			state = RunAwaitingApproval
		}
	}
	_, err = s.update(run.ID, func(cur *Run) {
		cur.State, cur.CommentState, cur.EvidenceState = state, v.CommentState, v.EvidenceState
		cur.CommentURL, cur.EvidenceCommit = v.CommentURL, v.EvidenceCommit
		cur.VideoState, cur.VideoURLs = published.videoState, published.videoURLs()
		for i := range cur.Legs {
			if cleanErr == nil {
				cur.Legs[i].State = "cleaned"
			}
		}
	})
	if err != nil {
		return v, err
	}
	if s.report != nil {
		note := fmt.Sprintf("Litmus verdict %s for %s#%d: see verdict.md in the session artifacts.", v.Result, run.Repo, run.Number())
		if err := s.report(ctx, run.SessionID, note); err != nil {
			slog.Warn("litmus: done report failed", "run", run.ID, "error", err)
		}
	}
	if s.kill != nil {
		time.AfterFunc(endGrace, func() {
			kctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := s.kill(kctx, run.SessionID); err != nil {
				slog.Warn("litmus: could not stop the tester after its run closed", "run", run.ID, "error", err)
			}
		})
	}
	s.autoPrune(ctx, run)
	return v, nil
}

func cell(s string) string {
	return strings.ReplaceAll(strings.Join(strings.Fields(s), " "), "|", `\|`)
}

// relPath is p relative to the run dir, which is also where it sits in the session artifact dir.
func relPath(runDir, p string) string {
	if rel, err := filepath.Rel(runDir, p); err == nil && filepath.IsLocal(rel) {
		return filepath.ToSlash(rel)
	}
	return filepath.Base(p)
}

func renderIndex(run Run, v Verdict, videos []legVideo, runDir string, clipSpans map[string]time.Duration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Litmus: %s#%d, verdict %s\n\n[Full report](verdict.md).\n", run.Repo, run.Number(), v.Result)
	for i, finding := range v.Findings {
		if fileExists(filepath.Join(runDir, "evidence", findingID(i)+".png")) {
			fmt.Fprintf(&b, "\n<img src=\"evidence/%s.png\" alt=\"%s\">\n", findingID(i), html.EscapeString(finding.Title))
		}
	}
	for _, video := range videos {
		for _, f := range video.files() {
			fmt.Fprintf(&b, "\n[%s](%s)\n", f, f)
		}
		for _, f := range video.clips {
			name := filepath.Base(f)
			label := name
			if span := clipSpans[strings.TrimSuffix(name, ".mp4")]; span > 0 {
				label = fmt.Sprintf("%s, %.0f s", name, span.Seconds())
			}
			fmt.Fprintf(&b, "\n[%s](clips/%s)\n", label, name)
		}
	}
	return b.String()
}

// publishArtifacts copies the receipts and each leg's videos into the session's artifact dir and writes verdict.md and
// index.md beside them; index.md with relative <img> links is how AO shows content inline in the session.
func publishArtifacts(dir, runDir, verdictMD, indexMD string, videos []legVideo) error {
	if dir == "" {
		return errors.New("no artifact dir for this session")
	}
	if err := os.MkdirAll(filepath.Join(dir, "receipts"), 0o750); err != nil {
		return err
	}
	files, _ := filepath.Glob(filepath.Join(runDir, "receipts", "*"))
	copies := map[string]string{} // source -> destination
	for _, name := range []string{"comment.md", verdictFile} {
		copies[filepath.Join(runDir, name)] = filepath.Join(dir, name)
	}
	images, err := filepath.Glob(filepath.Join(runDir, "evidence", "*"))
	if err != nil {
		return err
	}
	for _, file := range images {
		copies[file] = filepath.Join(dir, "evidence", filepath.Base(file))
	}
	for _, f := range files {
		copies[f] = filepath.Join(dir, relPath(runDir, f))
	}
	for _, video := range videos {
		if video.raw {
			copies[cmp.Or(video.source, recordingPath(runDir, video.leg))] = filepath.Join(dir, video.leg+".mp4")
		}
		if video.cut {
			copies[cutPath(runDir, video.leg)] = filepath.Join(dir, video.leg+"-cut.mp4")
		}
		for _, clip := range video.clips {
			copies[clip] = filepath.Join(dir, "clips", filepath.Base(clip))
		}
	}
	for src, dst := range copies {
		if fileExists(src) {
			if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
				return err
			}
			if err := copyFile(src, dst); err != nil {
				return err
			}
		}
	}
	if err := writeAtomic(filepath.Join(dir, "verdict.md"), []byte(verdictMD)); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, "index.md"), []byte(indexMD))
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return errors.Join(err, out.Close())
}

// readActions loads actions.jsonl up to its first unreadable line.
func readActions(runDir string) (recs []actionRec) {
	f, err := os.Open(filepath.Join(runDir, "actions.jsonl"))
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	for dec := json.NewDecoder(f); dec.More(); {
		var r actionRec
		if dec.Decode(&r) != nil {
			break
		}
		recs = append(recs, r)
	}
	return recs
}
