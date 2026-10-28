package litmus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestCommentRedactsOnlyAtEgress(t *testing.T) {
	for _, tc := range []struct{ name, output, secret, env, auth string }{
		{name: "raw", output: "HTTP 200 ghp_fakeToken123", secret: "ghp_fakeToken123"},
		{name: "escaped underscore in JSON", output: `{"token":"ghp\\_fakeToken123"}`, secret: "ghp_fakeToken123"},
		{name: "JSON unicode", output: `{"token":"ghp\u005ffakeToken123"}`, secret: "ghp_fakeToken123"},
		{name: "4KiB boundary", output: strings.Repeat("p", 4090) + " ghp_" + strings.Repeat("q", 80), secret: "ghp_" + strings.Repeat("q", 80)},
		{name: "Markdown", output: "*[ghp_fakeToken123](_proof_)*", secret: "ghp_fakeToken123"},
		{name: "error string", output: errors.New("request failed: ghp_fakeToken123").Error(), secret: "ghp_fakeToken123"},
		{name: "exact eight characters", output: "proof wordword", secret: "wordword", env: "wordword"},
		{name: "replacement word is a secret", output: "proof redacted", secret: "redacted", env: "redacted"},
		{name: "exact JSON string", output: `{"token":"opaque-\"value"}`, secret: `opaque-"value`, env: `opaque-"value`},
		{name: "local gh credential", output: "proof opaque-gh-credential", secret: "opaque-gh-credential", auth: "opaque-gh-credential"},
		{name: "credential shapes", output: "gho_fakeOAuth ghs_fakeServer github_pat_fakeFineGrained sk-fakeOpenAI sk-ant-fakeAnthropic Bearer opaque.header.signature AKIA1234567890ABCDEF eyJhbGci.eyJzdWI.signature xoxb-123-fake"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KEY", "1")
			t.Setenv("AUTH", "true")
			t.Setenv("LITMUS_TEST_TOKEN", tc.env)
			svc, f, _, dir := driveSvc(t)
			svc.media = &fakeMedia{}
			run, err := svc.update("run_s1", func(r *Run) { r.Repo = "o/r"; r.PRNumber = 7; r.Comment = CommentPost; r.Evidence = EvidenceOff })
			is(t, "")(nil, err)
			g := &commentGH{t: t, login: "bot", authToken: tc.auth}
			svc.gh = g
			f.out["eval"] = tc.output
			res, err := svc.Drive(ctx, "s1", "eval", nil)
			is(t, "")(nil, err)
			if res["output"] != tc.output {
				t.Fatal("ingress changed tester output")
			}
			raw := readActions(dir)
			if len(raw) != 1 || !strings.HasSuffix(tc.output, raw[0].Output) {
				t.Fatal("receipt changed raw evidence")
			}
			v := passVerdict()
			v.Headline = tc.output + "\ntrue receipt 1 https://github.com/o/r/pull/1"
			// The summary caps the headline, so the full proof also rides in a finding body, which posts uncut.
			v.Result, v.BlockedOn = "partial", "An account is missing."
			v.Findings = []Finding{{Title: "Proof", Leg: "head", Severity: "worth-a-look", Text: v.Headline}}
			path := verdictInput(t, svc, v)
			original, err := os.ReadFile(filepath.Join(dir, path))
			is(t, "")(nil, err)
			got, err := svc.Submit(ctx, "s1", path)
			is(t, "")(nil, err)
			if got.CommentState != "posted" || len(g.sent) != 2 || g.authCalls != 1 {
				t.Fatalf("post %+v calls %v", got, g.calls)
			}
			for i := range g.sent {
				plain := strings.ReplaceAll(g.field(i, "body"), `\`, "")
				if regexp.MustCompile(credentialShape).MatchString(plain) || tc.secret != "" && strings.Contains(plain, tc.secret) {
					t.Fatalf("outgoing body %d contains a credential", i)
				}
			}
			body := g.field(1, "body")
			plain := strings.ReplaceAll(body, `\`, "")
			if !strings.Contains(plain, "[...]") || !strings.Contains(body, "true receipt 1 https://github.com/o/r/pull/1") {
				t.Fatal("redaction lost proof or short env values damaged it")
			}
			saved, err := os.ReadFile(filepath.Join(dir, verdictFile))
			is(t, "")(nil, err)
			if string(saved) != string(original) {
				t.Fatal("Submit changed tester JSON")
			}
			if draft := readRunFile(t, svc, run, "comment.md"); draft != g.field(0, "body")+"\n"+body {
				t.Fatal("sent body differs from saved draft")
			}
			if report := readRunFile(t, svc, run, "verdict.md"); !strings.Contains(report, raw[0].Output) || !strings.Contains(report, mdText(tc.output)) {
				t.Fatal("local verdict.md changed raw tester text or receipt output")
			}
		})
	}
}

func TestShortSecretEnvironmentLeavesCommentUnchanged(t *testing.T) {
	svc, _, _, _ := submitSvc(t)
	run, err := svc.store.Get("run_s1")
	is(t, "")(nil, err)
	v := passVerdict()
	v.Headline = "true receipt 1 https://github.com/o/r/pull/1"
	before, _, err := svc.composeComment(run, v, svc.credentialPattern(ctx))
	is(t, "")(nil, err)
	t.Setenv("KEY", "1")
	t.Setenv("AUTH", "true")
	after, _, err := svc.composeComment(run, v, svc.credentialPattern(ctx))
	is(t, "")(nil, err)
	if before != after {
		t.Fatal("short environment values changed comment.md")
	}
}

func TestPostGateRefusesEditedCredentialWithoutCallingGitHub(t *testing.T) {
	for _, token := range []string{"ghp_injectedToken", `ghp\_injectedToken`, `ghp\\_injectedToken`, `ghp\u005finjectedToken`, `\u0067hp_injectedToken`, "opaque-injected-token", `opaque\secret`, `opaque\u005csecret`, `opaque\\secret`} {
		t.Run(token, func(t *testing.T) {
			t.Setenv("LITMUS_TEST_SECRET", "opaque-injected-token")
			t.Setenv("LITMUS_BACKSLASH_SECRET", `opaque\secret`)
			svc, _, dir, _ := submitSvc(t)
			run, err := svc.update("run_s1", func(r *Run) { r.Repo = "o/r"; r.ProjectID = "p1"; r.Comment = CommentPost; r.Evidence = EvidencePost })
			is(t, "")(nil, err)
			is(t, "")(svc.SetProjectSettings(ctx, "p1", ProjectSettings{AskBeforePosting: true}))
			g := &commentGH{t: t, login: "bot"}
			svc.gh = g
			is(t, "")(svc.Submit(ctx, "s1", verdictInput(t, svc, passVerdict())))
			body := readRunFile(t, svc, run, "comment.md") + "\n" + token + "\n"
			writeFile(t, filepath.Join(dir, "comment.md"), body)
			git := &evGit{t: t}
			svc.runner = git
			posted, err := svc.Post(ctx, run.ID)
			is(t, "")(nil, err)
			if !strings.HasPrefix(posted.CommentState, "failed:credential detected") || !strings.HasPrefix(posted.EvidenceState, "failed:credential detected") || posted.State != RunAwaitingApproval || len(g.calls) != 0 || len(git.calls) != 0 || g.authCalls != 2 {
				t.Fatalf("gate %+v gh=%v git=%v", posted, g.calls, git.calls)
			}
			if got := readRunFile(t, svc, run, "comment.md"); got != body {
				t.Fatal("gate rewrote the owner's draft")
			}
		})
	}
}

func TestPostGateChecksEveryFindingBeforeAnyNetworkCall(t *testing.T) {
	svc, dir, g := askSvc(t, true)
	run, err := svc.update("run_s1", func(r *Run) { r.Evidence = EvidencePost })
	is(t, "")(nil, err)
	v := passVerdict()
	v.Findings = []Finding{{Title: "First", Leg: "head", Severity: "worth-a-look", Text: "First observation."}, {Title: "Second", Leg: "head", Severity: "worth-a-look", Text: "Second observation."}}
	is(t, "")(svc.Submit(ctx, "s1", verdictInput(t, svc, v)))
	body := readRunFile(t, svc, run, "comment.md")
	body = strings.Replace(body, "Second observation.", "Second observation. ghp_injectedToken", 1)
	writeFile(t, filepath.Join(dir, "comment.md"), body)
	git := &evGit{t: t}
	svc.runner = git
	posted, err := svc.Post(ctx, run.ID)
	is(t, "")(nil, err)
	if !strings.HasPrefix(posted.CommentState, "failed:credential detected") || len(g.calls) != 0 || len(git.calls) != 0 || readRunFile(t, svc, run, "comment.md") != body {
		t.Fatalf("second finding escaped the whole-set gate: %+v gh %v git %v", posted, g.calls, git.calls)
	}
}

func TestPostGateRefusesLocalLinksBeforeNetwork(t *testing.T) {
	for _, edit := range []string{
		"[proof](receipts/head-1-after.png)",
		"![known](evidence/finding-1.png) [proof](./receipts/head-1-after.png)",
		"![proof](./receipts/head-1-after.png)",
		"[proof](%72eceipts%2Fhead-1-after.png)",
		"![proof](./evidence/other-finding.png)",
		"[proof](./clips/other-finding.mp4)",
		"![proof](shots/bug.png)",
		"[proof](./shots/unused/../bug.png)",
		"[proof](%2E%2Fshots%2Fbug.png)",
		"[proof](./shots/bug.png \"Bug\")",
		"[proof][shot]\n\n[shot]: ./receipts/head-1-after.png",
		"![proof][shot]\n\n[shot]: %72eceipts%2Fhead-1-after.png",
		"[proof](http://example.com/proof.png)",
		"<http://example.com/proof.png>",
		"http://example.com/proof.png",
		"<owner@example.com>",
		"[proof](//example.com/proof.png)",
		"[proof](https%3A%2F%2Fexample.com/proof.png)",
		"[proof]()",
		`<a href="receipts/head-1-after.png">proof</a>`,
		`<a href='./receipts/head-1-after.png'>proof</a>`,
		`<a href=receipts/head-1-after.png>proof</a>`,
		`<img src="receipts/head-1-after.png">`,
		`<img src='./evidence/finding-1.png'>`,
		`<img src=./evidence/finding-1.png>`,
		`<a href="%72eceipts%2Fhead-1-after.png">proof</a>`,
		`<img src='%2E%2Fevidence%2Ffinding-1.png'>`,
		`<img src=&#46;&#47;evidence&#47;finding-1.png>`,
		`<a href="relative/proof.png">proof</a>`,
		`<a title='>' href='relative/proof.png'>proof</a>`,
		`<img src=relative/proof.png>`,
		`<div><img alt='proof'src='relative/proof.png'></div>`,
		`<a HREF="javascript:alert(1)">proof</a>`,
		`<a href='http&#58;//example.com/proof.png'>proof</a>`,
		`<video poster='./shots/bug.png'>`,
		`<video poster='relative/proof.png'>`,
		`<source srcset="https://example.com/proof.png 1x, ./shots/bug.png 2x">`,
		`<source srcset='https://example.com/proof.png 1x, http://example.com/bug.png 2x'>`,
		`<source srcset='https://example.com/proof.png 1x,&#32;relative/proof.png 2x'>`,
		`proof="receipts/head-1-after.png"`,
		"Local proof: receipts/head-1-after.png",
		"100% complete. Local proof: %72eceipts%2Fhead-1-after.png",
		"run path", "home path", "encoded run path", "encoded home path",
	} {
		t.Run(edit, func(t *testing.T) {
			svc, dir, g := askSvc(t, true)
			run, err := svc.update("run_s1", func(r *Run) { r.Evidence = EvidencePost })
			is(t, "")(nil, err)
			v := passVerdict()
			v.Findings = []Finding{{Title: "First", Leg: "head", Severity: "worth-a-look", Text: "First observation."}, {Title: "Second", Leg: "head", Severity: "worth-a-look", Text: "Second observation."}}
			is(t, "")(svc.Submit(ctx, "s1", verdictInput(t, svc, v)))
			proof := edit
			home, err := os.UserHomeDir()
			is(t, "")(nil, err)
			switch edit {
			case "run path":
				proof = dir + "/snapshot.png"
			case "home path":
				proof = home + "/snapshot.png"
			case "encoded run path":
				proof = "[proof](" + strings.ReplaceAll(dir, "/", "%2F") + "%2Fsnapshot.png)"
			case "encoded home path":
				proof = "[proof](" + strings.ReplaceAll(home, "/", "%2F") + "%2Fsnapshot.png)"
			}
			body := readRunFile(t, svc, run, "comment.md") + "\n" + proof + "\n"
			writeFile(t, filepath.Join(dir, "comment.md"), body)
			git := &evGit{t: t}
			svc.runner = git
			posted, err := svc.Post(ctx, run.ID)
			is(t, "")(nil, err)
			if posted.State != RunAwaitingApproval || !strings.HasPrefix(posted.CommentState, "failed:comment.md") || len(g.calls) != 0 || len(git.calls) != 0 || len(posted.CommentURLs) != 0 {
				t.Fatalf("local link escaped the whole-set gate: %+v gh %v git %v", posted, g.calls, git.calls)
			}
			if got := readRunFile(t, svc, run, "comment.md"); got != body {
				t.Fatal("refusal changed the owner's draft")
			}
		})
	}
}

func TestPostGateAllowsHTTPSLinksAndAnchors(t *testing.T) {
	svc, dir, g := askSvc(t, true)
	is(t, "")(svc.Submit(ctx, "s1", verdictInput(t, svc, passVerdict())))
	run, err := svc.store.Get("run_s1")
	is(t, "")(nil, err)
	body := readRunFile(t, svc, run, "comment.md") + "\n[proof](https://example.com/proof.png)\n\n![proof](https://example.com/proof.png)\n\n[section](#proof)\n\n<https://example.com/proof.png>\n"
	body += `<a href="https://example.com/proof.png">proof</a><a href='#proof'>section</a><img src=https://example.com/proof.png><video poster='https://example.com/proof.png'><source srcset="https://example.com/proof.png 1x, https://example.com/large.png 2x">` + "\n"
	body += "\n`href=relative/proof.png`\n" + `<a title=" href='literal/proof.png' " href="https://example.com/proof.png">proof</a>` + "\n"
	writeFile(t, filepath.Join(dir, "comment.md"), body)
	posted, err := svc.Post(ctx, run.ID)
	is(t, "")(nil, err)
	if posted.CommentState != "posted" || len(g.sent) != 1 || g.field(0, "body") != body {
		t.Fatalf("public links or owner edits changed: %+v sent %v", posted, g.sent)
	}
}

func TestEvidenceGateRefusesCredentialWithoutNetworkCall(t *testing.T) {
	for _, kind := range []string{"image alt", "image filename", "video filename"} {
		t.Run(kind, func(t *testing.T) {
			secret := map[string]string{"image filename": "head-1-after.png", "video filename": "finding-1.mp4"}[kind]
			t.Setenv("LITMUS_TEST_SECRET", secret)
			svc, _, dir, _ := submitSvc(t)
			run, err := svc.update("run_s1", func(r *Run) { r.Repo = "o/r"; r.ProjectID = "p1"; r.Comment = CommentPost; r.Evidence = EvidencePost })
			is(t, "")(nil, err)
			is(t, "")(svc.SetProjectSettings(ctx, "p1", ProjectSettings{AskBeforePosting: true}))
			g := &commentGH{t: t, login: "bot"}
			svc.gh = g
			v := passVerdict()
			v.Result = "fail"
			v.Findings = []Finding{{Title: "Menu", Leg: "head", Severity: "bug", Receipts: []int{1}, Clip: &FindingClip{From: 1, To: 1}}}
			if kind == "image alt" {
				v.Findings[0].Title = `ghp\u005finjectedToken`
			}
			is(t, "")(svc.Submit(ctx, "s1", verdictInput(t, svc, v)))
			writeFile(t, findingClipPath(dir, 0), "clip")
			body := "The reviewed draft has no credential.\n"
			writeFile(t, filepath.Join(dir, "comment.md"), body)
			git := &evGit{t: t}
			svc.runner = git
			posted, err := svc.Post(ctx, run.ID)
			is(t, "")(nil, err)
			if posted.State != RunAwaitingApproval || !strings.HasPrefix(posted.CommentState, "failed:credential detected") || !strings.HasPrefix(posted.EvidenceState, "failed:credential detected") || !strings.HasPrefix(posted.VideoState, "failed:credential detected") || readRunFile(t, svc, run, "comment.md") != body {
				t.Fatalf("gate state %+v", posted)
			}
			if len(g.calls) != 0 || len(git.calls) != 0 || g.authCalls != 2 {
				t.Fatalf("gate called gh=%v git=%v", g.calls, git.calls)
			}
		})
	}
}

func TestRedactionMarkerIsIdempotent(t *testing.T) {
	t.Setenv("LITMUS_TEST_SECRET", "redacted")
	svc, _ := newSvc(t)
	re := svc.credentialPattern(ctx)
	first, found := redact("redacted ghp_fakeToken123", re)
	second, again := redact(first, re)
	if !found || again || first != second || checkEgress(re, mdText(first)) != nil {
		t.Fatal("replacement marker matched a credential")
	}
}

func TestPostPreservesOrdinaryCredentialWordsAndData(t *testing.T) {
	for _, ordinary := range []string{"Bearer authentication", "Bearer abcdefghijklmnopqrs", "0123456789abcdef0123456789abcdef01234567", "01234567-89ab-cdef-0123-456789abcdef", "data:image/png;base64,xAKIA1234567890ABCDEFx==", "token"} {
		t.Run(ordinary, func(t *testing.T) {
			svc, _, _, _ := submitSvc(t)
			run, err := svc.update("run_s1", func(r *Run) { r.Repo = "o/r"; r.ProjectID = "p1"; r.Comment = CommentPost })
			is(t, "")(nil, err)
			is(t, "")(svc.SetProjectSettings(ctx, "p1", ProjectSettings{AskBeforePosting: true}))
			g := &commentGH{t: t, login: "bot"}
			svc.gh = g
			v := passVerdict()
			v.Headline = ordinary
			is(t, "")(svc.Submit(ctx, "s1", verdictInput(t, svc, v)))
			draft := readRunFile(t, svc, run, "comment.md")
			if !strings.Contains(draft, mdText(ordinary)) {
				t.Fatal("ordinary text was redacted")
			}
			posted, err := svc.Post(ctx, run.ID)
			is(t, "")(nil, err)
			if posted.CommentState != "posted" || len(g.sent) != 1 || !strings.Contains(g.field(0, "body"), mdText(ordinary)) || g.field(0, "body") != readRunFile(t, svc, run, "comment.md") || g.authCalls != 2 {
				t.Fatalf("ordinary draft refused or changed: %+v", posted)
			}
		})
	}
}

func TestCredentialShapesPreserveBase64AWSSubstrings(t *testing.T) {
	svc, _ := newSvc(t)
	const ordinary = "xAKIA1234567890ABCDEFx AKIA1234567890ABCDEFX akia1234567890abcdef"
	if got, found := redact(ordinary, svc.credentialPattern(ctx)); found || got != ordinary {
		t.Fatal("AWS pattern damaged ordinary data")
	}
}

func TestExpiryRechecksIntakeUnderLock(t *testing.T) {
	svc, now := newSvc(t)
	run := Run{ID: "run_s1", SessionID: "s1", State: RunIntake, CreatedAt: *now, Deadline: now.Add(RunBudget)}
	is(t, "")(nil, svc.store.Save(run))
	var finish func()
	svc.WithActivity(func(context.Context, string) (string, time.Time, bool) {
		svc.mu.Lock()
		var err error
		finish, err = svc.markBusy(run.ID, "intake")
		svc.mu.Unlock()
		is(t, "")(nil, err)
		return "exited", *now, true
	})
	if svc.expireUnstarted(ctx, quiet) {
		t.Fatal("expiry ignored newly started intake")
	}
	if finish == nil {
		t.Fatal("race hook did not start intake")
	}
	finish()
	saved, err := svc.store.Get(run.ID)
	if err != nil || saved.State != RunIntake || saved.Error == ReasonNoIntake {
		t.Fatalf("expired in-flight intake: %+v %v", saved, err)
	}
}
