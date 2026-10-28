package litmus

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

const commentTimeout = 60 * time.Second

func commentMarker(run Run, suffix ...string) string {
	key := cmp.Or(run.Kind, "pr") + ":" + run.ID
	if len(suffix) > 0 {
		key += ":" + suffix[0]
	}
	return "<!-- litmus:" + key + " -->"
}

// mdText escapes Markdown without rewording, recasing, folding whitespace or clipping tester text.
func mdText(s string) string {
	return strings.NewReplacer(`\`, `\\`, "&", "&amp;", "<", "&lt;", ">", "&gt;", "[", `\[`, "]", `\]`, "(", `\(`, ")", `\)`, "*", `\*`, "_", `\_`, "`", "\\`", "#", `\#`, "!", `\!`, "|", `\|`, "@", "@\u200b").Replace(s)
}

func renderVerdict(run Run, v Verdict, receipts []Receipt, runDir, ownerLogins string, ev evidence, cleaners ...func(string) string) string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	clean := func(s string) string { return s }
	if len(cleaners) > 0 {
		clean = cleaners[0]
	}
	text := func(s string) string { return mdText(clean(s)) }
	p("# Verdict: %s\n\n%s\n", v.Result, text(v.Headline))
	if v.BlockedOn != "" {
		p("Blocked on: %s\n", text(v.BlockedOn))
	}
	p("## Findings\n")
	for i, f := range v.Findings {
		p("### %s\n\n%s, %s\n\n%s\n", text(f.Title), text(f.Severity), text(run.LegName(f.Leg)), text(f.Text))
		if links := receiptLinks(f.Leg, f.Receipts, receipts, runDir, clean); links != "" {
			p("Evidence: %s\n", links)
		}
		name := findingID(i)
		for _, it := range ev.items {
			if it.finding == name {
				p("![%s](evidence/%s)\n", text(f.Title), it.file)
			}
		}
		if file := findingClipPath(runDir, i); fileExists(file) {
			label := "Clip"
			if span := ev.clipSpans[name]; span > 0 {
				label = fmt.Sprintf("Clip, %.0f s", span.Seconds())
			}
			p("[%s](%s)\n", label, relPath(runDir, file))
		}
	}
	p("## Coverage\n")
	for _, trigger := range []bool{true, false} {
		for _, c := range v.Coverage {
			if c.Trigger != trigger {
				continue
			}
			prefix := ""
			if c.Trigger {
				prefix = "Trigger: "
			}
			links := receiptLinks(c.Leg, c.Receipts, receipts, runDir, clean)
			if links == "" {
				links = "no receipt"
			}
			p("%s%s, %s\n\n%s\n", prefix, text(run.LegName(c.Leg)), links, text(c.Text))
		}
	}
	p("## Environment\n\n%s\n", text(v.Environment))
	if line := homeEnvironment(run, runDir); line != "" {
		p("%s\n", text(line))
	}
	p("Receipts: %d\n", len(receipts))
	if data, err := os.ReadFile(filepath.Join(runDir, "home-changes.txt")); err == nil {
		count := 0
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "+ ") || strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "~ ") {
				count++
			}
		}
		p("Home changes (not attributed): %d\n", count)
	}
	if line := homeSnapshotNote(runDir); line != "" {
		p("%s\n", line)
	}
	p("%s\n", text(cmp.Or(ownerLogins, "owner logins: unverified")))
	for _, l := range run.Legs {
		if l.Name == "base" && l.State == "failed" {
			p("Base failed to launch.\n")
		}
	}
	for _, gap := range run.Gaps {
		p("%s\n", text(gap))
	}
	for _, limit := range ev.limits {
		p("%s\n", text(limit))
	}
	for _, it := range ev.items {
		if it.kind == kindGIF {
			p("![%s](evidence/%s)\n", text(it.alt), it.file)
		}
	}
	p("%s", commentMarker(run))
	return b.String()
}

func receiptLinks(leg string, ids []int, receipts []Receipt, runDir string, clean func(string) string) string {
	var links []string
	separator := ", "
	for _, id := range ids {
		for _, r := range receipts {
			if r.onLeg() == leg && r.Seq == id {
				label := fmt.Sprintf("%s receipt %d", mdText(leg), id)
				if r.Theme != "" {
					label += " (theme=" + mdText(r.Theme) + ")"
				}
				if fileExists(r.AfterPath) {
					links = append(links, fmt.Sprintf("[%s](%s)", label, relPath(runDir, r.AfterPath)))
				} else {
					links = append(links, label+" (after-shot unavailable)")
				}
				if r.Output != "" {
					separator = "\n\n"
					links[len(links)-1] += "\n\n" + receiptOutput(clean(r.Output)) + "\n"
				}
				break
			}
		}
	}
	return strings.Join(links, separator)
}

// receiptOutput uses a fence longer than any backtick run in the captured output.
func receiptOutput(output string) string {
	fence := "```"
	for strings.Contains(output, fence) {
		fence += "`"
	}
	return fence + "text\n" + output + "\n" + fence
}

func commentSummaryText(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) <= limit {
		return mdText(s)
	}
	s = string(runes[:limit+1])
	if i := max(strings.LastIndex(s, ". "), strings.LastIndex(s, "! "), strings.LastIndex(s, "? ")); i >= 0 {
		s = s[:i]
	} else if i := strings.LastIndexByte(s, ' '); i >= 0 {
		s = s[:i]
	} else {
		s = string(runes[:limit])
	}
	s = strings.TrimRight(s, ".!?… ")
	return mdText(s) + "…"
}

func renderCommentSummary(result, sha, headline, blockedOn, gap string, gapCount, findings int, old ...string) string {
	body := fmt.Sprintf("Litmus: %s at %.7s\n%s\n", mdText(strings.ReplaceAll(result, "_", " ")), sha, commentSummaryText(headline, 200))
	blockedOn = strings.TrimSpace(blockedOn)
	if untested := strings.TrimRight(strings.TrimSpace(cmp.Or(blockedOn, gap)), "."); untested != "" {
		ending := "."
		if blockedOn == "" && gapCount > 1 {
			ending = fmt.Sprintf(" and %d more.", gapCount-1)
		}
		limit := 200 - len("Not tested: ") - len(ending)
		summary := commentSummaryText(untested, limit)
		if ending == "." && len([]rune(strings.Join(strings.Fields(untested), " "))) > limit {
			ending = ""
		}
		body += "Not tested: " + summary + ending + "\n"
	}
	if findings > 0 || len(old) > 0 {
		line := "No new findings"
		noun := "findings"
		if findings == 1 {
			noun = "finding"
		}
		if findings > 0 {
			line = fmt.Sprintf("%d %s below", findings, noun)
		}
		if len(old) > 0 {
			line += "; also seen before this PR: " + strings.Join(old, "; ") + "."
		}
		body += commentSummaryText(line, 200) + "\n"
	}
	return body
}

// renderComments writes the summary first, then findings, each with its own upsert marker.
func renderComments(run Run, v Verdict, runDir string, ev evidence, clean func(string) string) string {
	if v.Result == "blocked" {
		return ""
	}
	text := func(s string) string { return mdText(clean(s)) }
	sha, leg, gap := run.HeadSHA, "head", ""
	if run.Kind == "issue" {
		sha, leg = run.TargetSHA, "main"
	}
	if len(run.Gaps) > 0 {
		gap = run.Gaps[0]
	}
	var old []string
	for _, f := range v.Findings {
		if run.Kind != "issue" && f.Leg == "base" {
			old = append(old, clean(f.Title))
		}
	}
	findings := len(v.Findings) - len(old)
	body := renderCommentSummary(v.Result, sha, clean(v.Headline), clean(v.BlockedOn), clean(gap), len(run.Gaps), findings, old...)
	if findings == 0 {
		for _, it := range ev.items {
			if it.kind == kindGIF && it.file == leg+".gif" {
				body += fmt.Sprintf("\n![%s](evidence/%s)\n", text(it.alt), it.file)
			}
		}
	}
	blocks := []string{body + "\n" + commentMarker(run, "summary") + "\n"}
	for i, f := range v.Findings {
		if run.Kind != "issue" && f.Leg == "base" {
			continue
		}
		name := findingID(i)
		body := fmt.Sprintf("**%s (%s)**\n\n%s\n", text(f.Title), text(run.LegName(f.Leg)), text(f.Text))
		for _, it := range ev.items {
			if it.kind == kindFinding && it.finding == name {
				body += fmt.Sprintf("\n![%s](evidence/%s)\n", text(f.Title), it.file)
			}
		}
		if fileExists(findingClipPath(runDir, i)) {
			label := "Clip"
			if span := ev.clipSpans[name]; span > 0 {
				label = fmt.Sprintf("Clip, %.0f s", span.Seconds())
			}
			body += fmt.Sprintf("\n[%s](clips/%s.mp4)\n", label, name)
		}
		blocks = append(blocks, body+"\n"+commentMarker(run, name)+"\n")
	}
	return strings.Join(blocks, "\n")
}

// composeComment writes the draft bodies and their local evidence without posting or pushing anything.
func (s *Service) composeComment(run Run, v Verdict, re *regexp.Regexp) (string, evidence, error) {
	ev := evidence{state: EvidenceOff, videoState: "off"}
	runDir := filepath.Join(s.store.root, run.ID)
	dir := filepath.Join(runDir, "evidence")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", ev, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), stageTimeout)
	defer cancel()
	ev.clipSpans = map[string]time.Duration{}
	for i := range v.Findings {
		if span, ok := s.recordingDuration(ctx, findingClipPath(runDir, i)); ok {
			ev.clipSpans[findingID(i)] = time.Duration(span * float64(time.Second))
		}
	}
	ev.items, ev.limits, _ = s.stageEvidence(ctx, run, v, dir) // individual media failures are logged and rendered as limits
	body := renderComments(run, v, runDir, ev, func(text string) string {
		text, _ = redact(text, re)
		return s.scrub(text)
	})
	return body, ev, s.store.WriteFile(run.ID, "comment.md", []byte(body))
}

// publishComment preserves edits to the draft and replaces only generated evidence link targets.
func (s *Service) publishComment(ctx context.Context, run Run, v Verdict, body string, re *regexp.Regexp, upload bool) (url, state string, ev evidence) {
	url = run.CommentURL
	if v.Result == "blocked" {
		return "", CommentOff, evidence{state: EvidenceOff, videoState: "off"}
	}
	ev = evidence{state: EvidenceOff, videoState: "off"}
	if upload {
		ev = s.prepareEvidence(ctx, run, v)
	}
	outgoing := []string{body, run.Repo, run.ID, evidencePath(run)}
	staged := map[string]bool{}
	for _, it := range ev.items {
		outgoing = append(outgoing, it.file, it.alt, it.caption)
		staged["evidence/"+it.file] = true
	}
	if upload {
		for i, f := range v.Findings {
			if run.Kind != "issue" && f.Leg == "base" {
				continue
			}
			file := findingClipPath(filepath.Join(s.store.root, run.ID), i)
			if fileExists(file) {
				outgoing = append(outgoing, filepath.Base(file), findingID(i)+".mp4")
				staged["clips/"+findingID(i)+".mp4"] = true
			}
		}
	}
	// Substitute public targets during preflight so other links on a media line still get checked.
	checked := rewriteCommentMedia(run, v, body, evidence{}, "https://litmus.invalid/proof", nil)
	if err := errors.Join(checkEgress(re, outgoing...), checkCommentLinks(checked, filepath.Join(s.store.root, run.ID))); err != nil {
		if upload {
			ev = s.evidenceFailed(run, err)
			ev.videoState = ev.state
		}
		return url, s.commentFailed(run, err), ev
	}
	body = s.scrub(body)
	blocks, err := splitComments(run, v, body)
	if err != nil {
		return url, s.commentFailed(run, err), ev
	}
	lctx, cancel := context.WithTimeout(ctx, commentTimeout)
	login, err := s.ghLogin(lctx)
	cancel()
	if err != nil {
		return url, s.commentFailed(run, err), ev
	}
	missingURL := ""
	if upload {
		ev = s.publishEvidence(ctx, run, ev, re)
		ev.findingClips, ev.videoState = s.publishFindingClips(ctx, run, v, re)
		missingURL = "evidence/unpublished"
	}
	body = rewriteCommentMedia(run, v, body, ev, missingURL, staged)
	if err := checkCommentLinks(body, filepath.Join(s.store.root, run.ID)); err != nil {
		return url, s.commentFailed(run, fmt.Errorf("images: %s; clips: %s: %w", ev.state, ev.videoState, err)), ev
	}
	if err := s.store.WriteFile(run.ID, "comment.md", []byte(body)); err != nil {
		return url, s.commentFailed(run, err), ev
	}
	pctx, cancel := context.WithTimeout(ctx, commentTimeout)
	defer cancel()
	raw, err := s.ghRun(pctx, "api", "repos/"+run.Repo+"/issues/"+strconv.Itoa(run.Number())+"/comments", "--paginate")
	if err != nil {
		return url, s.commentFailed(run, err), ev
	}
	state = "updated"
	for _, block := range blocks {
		id, err := findComment(raw, block.marker, login)
		if err != nil {
			return url, s.commentFailed(run, fmt.Errorf("unreadable comment list: %w", err)), ev
		}
		sent, sentState, err := s.sendComment(pctx, run, rewriteCommentMedia(run, v, s.scrub(block.body), ev, "", nil), id, re)
		if err != nil {
			return url, s.commentFailed(run, err), ev
		}
		sentURL, _ := sent["html_url"].(string)
		url = cmp.Or(url, sentURL, run.CommentURLs[block.marker])
		if block.marker == commentMarker(run, "summary") {
			url = cmp.Or(sentURL, run.CommentURLs[block.marker], url)
		}
		run.CommentURL = url
		if run.CommentURLs == nil {
			run.CommentURLs = map[string]string{}
		}
		run.CommentURLs[block.marker] = cmp.Or(sentURL, run.CommentURLs[block.marker])
		if _, err := s.update(run.ID, func(cur *Run) { cur.CommentURL, cur.CommentURLs = url, run.CommentURLs }); err != nil {
			return url, s.commentFailed(run, err), ev
		}
		if sentState == "posted" {
			state = sentState
		}
	}
	return url, state, ev
}

func rewriteCommentMedia(run Run, v Verdict, body string, ev evidence, missingURL string, staged map[string]bool) string {
	replace := func(local, url string) {
		// Preflight substitutes every generated name; posting marks only staged media as unpublished.
		if staged == nil || staged[local] {
			url = cmp.Or(url, missingURL)
		}
		body = rewriteCommentLink(body, local, url)
	}
	for i := range v.Findings {
		name := findingID(i)
		replace("evidence/"+name+".png", ev.findingImages[name])
		replace("clips/"+name+".mp4", ev.findingClips[name])
	}
	images := map[string]string{}
	for _, it := range ev.items {
		images[it.file] = it.url
	}
	for _, leg := range run.Legs {
		replace("evidence/"+leg.Name+".gif", images[leg.Name+".gif"])
	}
	return strings.ReplaceAll(body, "<data dir>", "&lt;data dir&gt;")
}

func decodeCommentTarget(target string) string {
	target = html.UnescapeString(string(util.UnescapePunctuations([]byte(target))))
	return regexp.MustCompile(`(?:%[0-9a-fA-F]{2})+`).ReplaceAllStringFunc(target, func(encoded string) string {
		decoded, _ := url.PathUnescape(encoded)
		return decoded
	})
}

func checkCommentLinks(body, runDir string) error {
	decoded := decodeCommentTarget(body)
	home, _ := os.UserHomeDir()
	local := regexp.MustCompile(`(?m)(?:^|[^[:alnum:]_./\\-])(?:\./)*(?:evidence|clips|receipts|shots)/[^\s)>]*`).FindString(decoded)
	for _, dir := range []string{runDir, home} {
		if dir != "" && strings.Contains(decoded, dir) {
			local = dir
		}
	}
	if local != "" {
		return fmt.Errorf("comment.md contains local path %q", strings.TrimSpace(local))
	}
	validate := func(target string) error {
		target = html.UnescapeString(string(util.UnescapePunctuations([]byte(target))))
		u, err := url.Parse(target)
		if !strings.HasPrefix(target, "#") && (err != nil || u.Scheme != "https" || u.Host == "") {
			return fmt.Errorf("comment.md has unsafe link target %q", target)
		}
		return nil
	}
	checkHTML := func(raw string) error {
		// Consume complete attribute values so quoted text cannot hide a later destination.
		attributes := regexp.MustCompile(`(?i)\b([a-z_:][a-z0-9:._-]*)\s*=\s*("[^"]*"|'[^']*'|[^\s"'=<>` + "`" + `]*)`)
		for _, attr := range attributes.FindAllStringSubmatch(raw, -1) {
			if name := strings.ToLower(attr[1]); name != "href" && name != "src" && name != "srcset" && name != "poster" {
				continue
			}
			targets := []string{html.UnescapeString(strings.Trim(attr[2], `"'`))}
			if strings.EqualFold(attr[1], "srcset") {
				targets = strings.Split(targets[0], ",")
			}
			for _, target := range targets {
				if strings.EqualFold(attr[1], "srcset") {
					target = regexp.MustCompile(`\s+`).Split(strings.TrimSpace(target), 2)[0]
				}
				if err := validate(target); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return ast.Walk(goldmark.New(goldmark.WithExtensions(extension.Linkify)).Parser().Parse(text.NewReader([]byte(body))), func(n ast.Node, _ bool) (ast.WalkStatus, error) {
		var target string
		switch n := n.(type) {
		case *ast.Link:
			target = string(n.Destination)
		case *ast.Image:
			target = string(n.Destination)
		case *ast.AutoLink:
			target = string(n.URL([]byte(body)))
		case *ast.RawHTML, *ast.HTMLBlock:
			return ast.WalkContinue, checkHTML(string(n.Text([]byte(body))))
		default:
			return ast.WalkContinue, nil
		}
		return ast.WalkContinue, validate(target)
	})
}

func rewriteCommentLink(body, local, url string) string {
	pattern := regexp.MustCompile(`\]\((<[^>\n]*>|[^\s)\n]*)(?:[ \t]+"[^"\n]*")?[ \t]*\)`)
	var kept []string
	for _, line := range strings.Split(body, "\n") {
		drop := false
		line = pattern.ReplaceAllStringFunc(line, func(link string) string {
			target := pattern.FindStringSubmatch(link)[1]
			if path.Clean(decodeCommentTarget(strings.Trim(target, "<>"))) != local {
				return link
			}
			drop = drop || url == ""
			return strings.Replace(link, target, url, 1)
		})
		if !drop {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

type commentBody struct{ body, marker string }

func splitComments(run Run, v Verdict, body string) ([]commentBody, error) {
	expected := map[string]bool{commentMarker(run, "summary"): true}
	for i, f := range v.Findings {
		expected[commentMarker(run, findingID(i))] = run.Kind == "issue" || f.Leg != "base"
	}
	var blocks []commentBody
	start, end, pos := 0, 0, 0
	for _, line := range strings.SplitAfter(body, "\n") {
		pos += len(line)
		if !strings.Contains(line, "<!-- litmus:") {
			continue
		}
		marker := strings.TrimSpace(line)
		_, ok := expected[marker]
		if !ok {
			return nil, fmt.Errorf("comment.md marker %q is unexpected, repeated or inline", line[strings.Index(line, "<!-- litmus:"):])
		}
		delete(expected, marker)
		end = pos
		blocks = append(blocks, commentBody{body: body[start:end], marker: marker})
		start = end
		if start < len(body) && body[start] == '\n' {
			start++
		}
	}
	for marker, required := range expected {
		if required {
			return nil, fmt.Errorf("comment.md is missing marker %s", marker)
		}
	}
	blocks[len(blocks)-1].body += body[end:]
	i := slices.IndexFunc(blocks, func(block commentBody) bool { return block.marker == commentMarker(run, "summary") })
	summary := blocks[i]
	copy(blocks[1:i+1], blocks[:i])
	blocks[0] = summary
	return blocks, nil
}

func (s *Service) sendComment(ctx context.Context, run Run, body string, id int64, re *regexp.Regexp) (sent map[string]any, state string, err error) {
	if err := checkEgress(re, body); err != nil {
		return nil, "", err
	}
	method, path, state := "POST", "repos/"+run.Repo+"/issues/"+strconv.Itoa(run.Number())+"/comments", "posted"
	if id != 0 {
		method, path, state = "PATCH", "repos/"+run.Repo+"/issues/comments/"+strconv.FormatInt(id, 10), "updated"
	}
	out, err := s.ghSend(ctx, run, method, path, map[string]string{"body": body})
	if err != nil {
		return nil, "", err
	}
	sent = make(map[string]any)
	if err := json.Unmarshal(out, &sent); err != nil {
		return nil, "", err
	}
	if _, ok := sent["id"]; !ok {
		sent["id"] = id
	}
	return sent, state, nil
}

// commentFailed logs err to comment.log in the run dir and returns the state it earns.
func (s *Service) commentFailed(run Run, err error) string {
	slog.Warn("litmus: PR comment failed", "run", run.ID, "error", err)
	s.commentLogf(run, "%v", err)
	reason, _, _ := strings.Cut(err.Error(), "\n")
	reason = clip(strings.TrimSpace(reason), 120)
	if run.CommentURL != "" || len(run.CommentURLs) > 0 {
		return "partial:" + reason
	}
	return "failed:" + reason
}

func (s *Service) commentLogf(run Run, format string, a ...any) {
	f, ferr := os.OpenFile(filepath.Join(s.store.root, run.ID, "comment.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if ferr != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "%s %s\n", s.clock().UTC().Format(time.RFC3339), fmt.Sprintf(format, a...))
}

// ghLogin is the login the gh token acts as, asked once per service.
func (s *Service) ghLogin(ctx context.Context) (string, error) {
	s.mu.Lock()
	login := s.login
	s.mu.Unlock()
	if login != "" {
		return login, nil
	}
	out, err := s.ghRun(ctx, "api", "user", "--jq", ".login")
	if login = strings.TrimSpace(string(out)); err == nil && login == "" {
		err = errors.New("gh api user returned no login")
	}
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.login = login
	s.mu.Unlock()
	return login, nil
}

// ghSend calls the GitHub API with a JSON body read from a file, never from argv, and returns the object gh prints.
func (s *Service) ghSend(ctx context.Context, run Run, method, path string, body map[string]string) ([]byte, error) {
	data, _ := json.Marshal(body)
	f, err := os.CreateTemp(filepath.Join(s.store.root, run.ID), ".gh-*.json")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	return s.ghRun(ctx, "api", "--method", method, path, "--input", f.Name())
}

// findComment returns the id of the comment holding marker that login wrote, or 0. `gh --paginate` prints one JSON
// array per page, back to back.
func findComment(raw []byte, marker, login string) (int64, error) {
	for dec := json.NewDecoder(bytes.NewReader(raw)); dec.More(); {
		var page []struct {
			ID   int64
			Body string
			User struct{ Login string }
		}
		if err := dec.Decode(&page); err != nil {
			return 0, err
		}
		for _, c := range page {
			if strings.Contains(c.Body, marker) && strings.EqualFold(c.User.Login, login) {
				return c.ID, nil
			}
		}
	}
	return 0, nil
}

var secretEnvName = regexp.MustCompile(`(?i)TOKEN|SECRET|KEY|PASSWORD|AUTH`)

const credentialShape = `(?:gh[pousr]_|github_pat_)[A-Za-z0-9_]+|\bsk-(?:ant-)?[A-Za-z0-9_-]+|(?i:Bearer)[\t ]+[A-Za-z0-9._~+/-]{20,}=*|\bAKIA[A-Z0-9]{16}\b|eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+|xox[a-z]-[A-Za-z0-9-]+`

func (g execGH) AuthToken(ctx context.Context) string {
	out, _ := g.Run(ctx, "auth", "token")
	return strings.TrimSpace(string(out))
}

func (s *Service) credentialPattern(ctx context.Context) *regexp.Regexp {
	patterns := []string{credentialShape, `(?:gh[pousr]|github(?:_|\\+_|\\+u005[fF])pat)(?:_|\\+_|\\+u005[fF])(?:[A-Za-z0-9_]|\\+_|\\+u005[fF])+`}
	add := func(value string) {
		if len(value) >= 8 {
			encoded, _ := json.Marshal(value)
			patterns = append(patterns, regexp.QuoteMeta(value), regexp.QuoteMeta(string(encoded[1:len(encoded)-1])))
		}
	}
	for _, entry := range os.Environ() {
		if name, value, _ := strings.Cut(entry, "="); secretEnvName.MatchString(name) {
			add(value)
		}
	}
	if gh, ok := s.gh.(interface{ AuthToken(context.Context) string }); ok {
		add(gh.AuthToken(ctx))
	}
	re := regexp.MustCompile(strings.Join(patterns, "|"))
	re.Longest()
	return re
}

// redact runs on raw comment text, before Markdown escaping.
func redact(text string, re *regexp.Regexp) (string, bool) {
	return re.ReplaceAllString(text, "[...]"), re.MatchString(text)
}

var jsonEscape = regexp.MustCompile(`\\+(?:u[0-9a-fA-F]{4}|["\\/bfnrt])`)

func checkEgress(re *regexp.Regexp, texts ...string) error {
	raw := strings.Join(texts, "\n")
	text := jsonEscape.ReplaceAllStringFunc(raw, func(escaped string) string {
		var decoded string
		_ = json.Unmarshal([]byte(`"\`+strings.TrimLeft(escaped, `\`)+`"`), &decoded)
		return decoded
	})
	if re.MatchString(raw) || re.MatchString(text) || re.MatchString(strings.ReplaceAll(text, `\`, "")) {
		return errors.New("credential detected in outgoing text; publication refused")
	}
	return nil
}
