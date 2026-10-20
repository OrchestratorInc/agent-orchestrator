package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/charmbracelet/x/term"
)

const triageEvidenceHeading = "## Agent-collected evidence"

var triageRelated = regexp.MustCompile(`Related: #([1-9][0-9]*)\b`)
var triageIssueURL = regexp.MustCompile(`^https://github\.com/OrchestratorInc/agent-orchestrator/issues/([1-9][0-9]*)(?:#issuecomment-[0-9]+)?$`)

func triageTTY(in io.Reader, out io.Writer) bool {
	i, iok := in.(*os.File)
	o, ook := out.(*os.File)
	return iok && ook && term.IsTerminal(i.Fd()) && term.IsTerminal(o.Fd())
}

func finishTriage(ctx context.Context, in io.Reader, out io.Writer, dir string, github triageGitHub, tty bool) (err error) {
	status, url := "draft", ""
	report := filepath.Join(dir, "bug-report.md")
	defer func() {
		receipt, _ := json.Marshal(map[string]string{"status": status, "url": url})
		if writeErr := os.WriteFile(filepath.Join(dir, "result.json"), receipt, 0o600); err == nil {
			err = writeErr
		}
		if status != "filed" && status != "commented" {
			message := "not posted"
			if err != nil {
				message = "posting not confirmed"
			}
			_, _ = fmt.Fprintln(out, report, message)
		}
	}()
	raw, readErr := os.ReadFile(report)
	if readErr != nil {
		return nil
	}
	first, rest, _ := strings.Cut(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	if first != "status: ready" && first != "status: duplicate" {
		return nil
	}
	status = "skipped"
	if !tty || github.Path == "" || github.Auth != "ok" {
		return nil
	}
	human, evidence, hasEvidence := strings.Cut(rest, triageEvidenceHeading)
	if !hasEvidence {
		return fmt.Errorf("draft is missing the evidence section")
	}
	title, body, _ := strings.Cut(strings.TrimSpace(human), "\n")
	if !strings.HasPrefix(title, "Title:") {
		return fmt.Errorf("draft is missing Title")
	}
	title = strings.TrimSpace(strings.TrimPrefix(title, "Title:"))
	mainBody, comment, _ := strings.Cut(body, "## Comment")
	body = strings.TrimSpace(mainBody)
	number := ""
	if first == "status: duplicate" {
		related := triageRelated.FindStringSubmatch(mainBody)
		if related == nil {
			return fmt.Errorf("duplicate draft requires Related: #n")
		}
		number = related[1]
		_, comment, _ = strings.Cut(comment, "\n")
		body = strings.TrimSpace(comment)
	}
	if title == "" || body == "" {
		return fmt.Errorf("draft title or body is empty")
	}
	if first == "status: ready" {
		label := "no label"
		if github.Label == "present" {
			label = "needs-triage"
		}
		if _, err := fmt.Fprintf(out, "Title: %s\n\n%s\n\nLabel: %s\n", title, body, label); err != nil {
			return err
		}
	} else {
		target := fmt.Sprintf("https://github.com/%s/issues/%s", triageRepo, number)
		raw, err := triageGH(ctx, github.Path, "issue", "view", number, "--repo", triageRepo, "--json", "url")
		var issue struct {
			URL string `json:"url"`
		}
		if err != nil || json.Unmarshal([]byte(raw), &issue) != nil || issue.URL != target {
			return fmt.Errorf("could not verify duplicate issue %s; nothing was posted", target)
		}
		if _, err := fmt.Fprintf(out, "Comment on %s\n\n%s\n", target, body); err != nil {
			return err
		}
	}
	evidence = triageEvidenceHeading + evidence
	if _, err := fmt.Fprintln(out, "\n"+evidence); err != nil {
		return err
	}
	yes, err := confirm(in, out, "Post this to GitHub?", false)
	if err != nil || !yes {
		return err
	}
	bodyPath := filepath.Join(dir, "post-body.md")
	if err := os.WriteFile(bodyPath, []byte(body), 0o600); err != nil {
		return err
	}
	args := []string{"issue", "comment", number, "--repo", triageRepo, "--body-file", bodyPath}
	if first == "status: ready" {
		args = []string{"issue", "create", "--repo", triageRepo, "--title", title, "--body-file", bodyPath}
		if github.Label == "present" {
			args = append(args, "--label", "needs-triage")
		}
	}
	status = "draft"
	output, err := triageGH(ctx, github.Path, args...)
	if err != nil {
		return fmt.Errorf("GitHub posting failed; outcome unknown, check GitHub before retrying: %w", err)
	}
	url = strings.TrimSpace(output)
	match := triageIssueURL.FindStringSubmatch(url)
	if match == nil || (number != "" && match[1] != number) {
		url = ""
		return fmt.Errorf("GitHub returned no issue URL; outcome unknown, check GitHub before retrying")
	}
	status = "commented"
	if first == "status: ready" {
		status = "filed"
	}
	evidencePath := filepath.Join(dir, "post-evidence.md")
	if err := os.WriteFile(evidencePath, []byte(evidence), 0o600); err != nil {
		return err
	}
	if _, err := triageGH(ctx, github.Path, "issue", "comment", match[1], "--repo", triageRepo, "--body-file", evidencePath); err != nil {
		return fmt.Errorf("posted at %s; evidence comment failed: %w", url, err)
	}
	_, err = fmt.Fprintln(out, url)
	return err
}
