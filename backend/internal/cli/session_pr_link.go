package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

// sessionPRLinkDTO mirrors the daemon's SessionPRLinkView.
type sessionPRLinkDTO struct {
	URL      string `json:"url"`
	Provider string `json:"provider"`
	Repo     string `json:"repo"`
	Number   int    `json:"number"`
	Relation string `json:"relation"`
	Title    string `json:"title,omitempty"`
	State    string `json:"state,omitempty"`
	Author   string `json:"author,omitempty"`
}

type linkPRRequest struct {
	PR       string `json:"pr"`
	Relation string `json:"relation"`
}

type unlinkPRRequest struct {
	PR string `json:"pr"`
}

type linkPRResponse struct {
	OK        bool             `json:"ok"`
	SessionID string           `json:"sessionId"`
	Link      sessionPRLinkDTO `json:"link"`
}

type sessionLinkPROptions struct {
	project  string
	relation string
	json     bool
}

const linkPRLong = `Show a pull request on a session as one it reviews or contributes to, without
taking ownership. When session-id is omitted, the current session is read from
AO_SESSION_ID.

Unlike claim-pr, a link changes nothing about the PR: the session does not own
it, its review comments and CI results are not delivered to the session, it does
not affect the session's status, and it never takes the PR from the session that
owns it. Use it for a PR you review, and for someone else's PR you push
commits to.

The PR must be in the project's repositories and readable with your SCM
credentials, as for claim-pr. Running it again is safe: it sets the relation and
refreshes the PR's title, state, and author.`

func newSessionLinkPRCommand(ctx *commandContext) *cobra.Command {
	var opts sessionLinkPROptions
	cmd := &cobra.Command{
		Use:   "link-pr [<session-id>] <pr-ref> --as reviewing|contributing",
		Short: "Show a PR the session reviews or contributes to, without owning it",
		Long:  linkPRLong,
		Example: `  # From inside a session reviewing someone else's PR
  ao session link-pr https://github.com/acme/app/pull/88 --as reviewing

  # After pushing commits to another contributor's PR
  ao session link-pr mer-3 88 --as contributing`,
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.RangeArgs(1, 2)(cmd, args); err != nil {
				return usageError{err}
			}
			_, _, err := resolveSessionClaimPRArgs(args)
			return err
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			relation := strings.TrimSpace(opts.relation)
			if relation != "reviewing" && relation != "contributing" {
				return usageError{errors.New("usage: --as must be reviewing or contributing")}
			}
			id, ref, err := resolveSessionClaimPRArgs(args)
			if err != nil {
				return err
			}
			return ctx.linkSessionPR(cmd.Context(), cmd, id, ref, relation, opts)
		},
	}
	addSessionProjectFlag(cmd.Flags(), &opts.project, "Project id to scope the lookup")
	cmd.Flags().StringVar(&opts.relation, "as", "", "How the session relates to the PR: reviewing or contributing (required)")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output as JSON")
	return cmd
}

func newSessionUnlinkPRCommand(ctx *commandContext) *cobra.Command {
	var opts sessionLinkPROptions
	cmd := &cobra.Command{
		Use:   "unlink-pr [<session-id>] <pr-ref>",
		Short: "Remove a PR the session reviews or contributes to from the session",
		Long: "Remove a related PR from a session. When session-id is omitted, the current session is read from AO_SESSION_ID.\n\n" +
			"It changes nothing on the PR and never releases a PR the session owns.",
		Example: `  ao session unlink-pr https://github.com/acme/app/pull/88
  ao session unlink-pr mer-3 88`,
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.RangeArgs(1, 2)(cmd, args); err != nil {
				return usageError{err}
			}
			_, _, err := resolveSessionClaimPRArgs(args)
			return err
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			id, ref, err := resolveSessionClaimPRArgs(args)
			if err != nil {
				return err
			}
			return ctx.unlinkSessionPR(cmd.Context(), cmd, id, ref, opts)
		},
	}
	addSessionProjectFlag(cmd.Flags(), &opts.project, "Project id to scope the lookup")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output as JSON")
	return cmd
}

func (c *commandContext) linkSessionPR(ctx context.Context, cmd *cobra.Command, id, ref, relation string, opts sessionLinkPROptions) error {
	sess, err := c.fetchScopedSession(ctx, id, opts.project)
	if err != nil {
		return err
	}
	project, err := c.fetchProjectDetails(ctx, sess.ProjectID)
	if err != nil {
		return err
	}
	resolvedRef, err := c.resolvePRRef(ctx, ref, project)
	if err != nil {
		return err
	}
	var res linkPRResponse
	if err := c.postJSON(ctx, "sessions/"+url.PathEscape(id)+"/pr/link", linkPRRequest{PR: resolvedRef, Relation: relation}, &res); err != nil {
		return err
	}
	if opts.json {
		return writeJSON(cmd.OutOrStdout(), res)
	}
	out := cmd.OutOrStdout()
	if _, err := fmt.Fprintf(out, "session %s is %s PR #%d (no ownership taken)\n", res.SessionID, relationVerb(res.Link.Relation), res.Link.Number); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "  pr:       %s\n", res.Link.URL)
	return err
}

func (c *commandContext) unlinkSessionPR(ctx context.Context, cmd *cobra.Command, id, ref string, opts sessionLinkPROptions) error {
	if _, err := c.fetchScopedSession(ctx, id, opts.project); err != nil {
		return err
	}
	var res linkPRResponse
	if err := c.postJSON(ctx, "sessions/"+url.PathEscape(id)+"/pr/unlink", unlinkPRRequest{PR: strings.TrimSpace(ref)}, &res); err != nil {
		return err
	}
	if opts.json {
		return writeJSON(cmd.OutOrStdout(), res)
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "session %s no longer shows PR #%d\n  pr:       %s\n", res.SessionID, res.Link.Number, res.Link.URL)
	return err
}

func relationVerb(relation string) string {
	if relation == "contributing" {
		return "contributing to"
	}
	return "reviewing"
}

// writeSessionPRLinks prints the related PRs block of `ao session get`.
func writeSessionPRLinks(out io.Writer, links []sessionPRLinkDTO) error {
	if len(links) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(out, "related prs (not owned):"); err != nil {
		return err
	}
	for _, link := range links {
		line := fmt.Sprintf("  %s #%d", relationLabel(link.Relation), link.Number)
		var facts []string
		if link.State != "" {
			facts = append(facts, link.State)
		}
		if link.Author != "" {
			facts = append(facts, "by "+link.Author)
		}
		if len(facts) > 0 {
			line += " (" + strings.Join(facts, ", ") + ")"
		}
		if link.Title != "" {
			line += " " + link.Title
		}
		line += " " + link.URL
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	}
	return nil
}

func relationLabel(relation string) string {
	if relation == "contributing" {
		return "Contributing to"
	}
	return "Reviewing"
}
