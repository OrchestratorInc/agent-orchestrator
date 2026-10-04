package cli

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

type councilOptions struct {
	project    string
	standalone bool
	mode       string
	approval   string
	prompt     string
	name       string
	members    []string
}

// councilMemberReq mirrors controllers.SpawnCouncilMember.
type councilMemberReq struct {
	Harness string `json:"harness"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
}

// councilRequest mirrors the daemon's SpawnCouncilRequest body for
// POST /api/v1/sessions/council. The CLI keeps its own copy so it need not
// import httpd.
type councilRequest struct {
	ProjectID    string             `json:"projectId,omitempty"`
	Mode         string             `json:"mode,omitempty"`
	ApprovalMode string             `json:"approvalMode,omitempty"`
	Prompt       string             `json:"prompt,omitempty"`
	DisplayName  string             `json:"displayName,omitempty"`
	Members      []councilMemberReq `json:"members"`
}

type councilMemberResult struct {
	Harness string `json:"harness"`
	Model   string `json:"model,omitempty"`
	Session *struct {
		ID             string `json:"id"`
		Status         string `json:"status"`
		DisplayName    string `json:"displayName"`
		CouncilGroupID string `json:"councilGroupId"`
	} `json:"session,omitempty"`
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
}

type councilResult struct {
	GroupID string                `json:"groupId"`
	Members []councilMemberResult `json:"members"`
}

func newCouncilCommand(ctx *commandContext) *cobra.Command {
	var opts councilOptions
	cmd := &cobra.Command{
		Use:   "council",
		Short: "Run one brief across several models at once (fan-out)",
		Long: "Spawn one worker session per model for the same brief, all linked by a\n" +
			"shared council group id so you can compare their results side by side.\n\n" +
			"Give each model with a repeated --member flag as harness[:model[:effort]], e.g.\n" +
			"  ao council --name compare --prompt \"fix the flaky test\" \\\n" +
			"    --member claude-code:sonnet --member codex:gpt-5 --member cursor",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.standalone && strings.TrimSpace(opts.project) != "" {
				return usageError{fmt.Errorf("--standalone and --project cannot be used together")}
			}
			name := strings.TrimSpace(opts.name)
			if name == "" {
				return usageError{fmt.Errorf("--name is required")}
			}
			if utf8.RuneCountInString(name) > maxDisplayNameLen {
				return usageError{fmt.Errorf("--name must be %d characters or fewer", maxDisplayNameLen)}
			}
			members, err := parseCouncilMembers(opts.members)
			if err != nil {
				return err
			}
			if len(members) < 2 {
				return usageError{fmt.Errorf("a council needs at least 2 --member entries")}
			}
			if opts.mode != "" && opts.mode != "chat" && opts.mode != "tui" {
				return usageError{fmt.Errorf("--mode must be chat or tui")}
			}

			if !opts.standalone {
				project, err := ctx.resolveSpawnProject(cmd.Context(), opts.project)
				if err != nil {
					return err
				}
				opts.project = project.ID
			}

			req := councilRequest{
				ProjectID:    opts.project,
				Mode:         opts.mode,
				ApprovalMode: strings.TrimSpace(opts.approval),
				Prompt:       opts.prompt,
				DisplayName:  name,
				Members:      members,
			}
			var res councilResult
			if err := ctx.postJSON(cmd.Context(), "sessions/council", req, &res); err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if _, err := fmt.Fprintf(out, "council %s (%d models)\n", res.GroupID, len(res.Members)); err != nil {
				return err
			}
			failures := 0
			for _, member := range res.Members {
				label := member.Harness
				if member.Model != "" {
					label = member.Harness + ":" + member.Model
				}
				if member.Session != nil {
					if _, err := fmt.Fprintf(out, "  ✓ %-24s session %s (%s)\n", label, member.Session.ID, member.Session.Status); err != nil {
						return err
					}
					continue
				}
				failures++
				reason := member.ErrorMessage
				if reason == "" {
					reason = member.ErrorCode
				}
				if _, err := fmt.Fprintf(out, "  ✗ %-24s %s\n", label, reason); err != nil {
					return err
				}
			}
			if failures == len(res.Members) {
				return fmt.Errorf("every council member failed to start")
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.project, "project", "", "Project id to spawn the council in (default: AO_PROJECT_ID or the current registered repo)")
	f.BoolVar(&opts.standalone, "standalone", false, "Spawn projectless workers in AO-managed plain directories")
	f.StringArrayVar(&opts.members, "member", nil, "A model to include, as harness[:model[:effort]] (repeatable; at least 2 required)")
	f.StringVar(&opts.mode, "mode", "", "Initial session interface for every member: chat or tui (default: daemon default)")
	f.StringVar(&opts.approval, "approval", "", "Approval policy for every member: default, accept-edits, auto, or bypass-permissions")
	f.StringVar(&opts.prompt, "prompt", "", "The shared brief handed to every model")
	f.StringVar(&opts.name, "name", "", "Base display name for the cohort; each member appends its harness (required, max 100 characters)")
	return cmd
}

// parseCouncilMembers parses repeated --member values of the form
// harness[:model[:effort]] into request members, preserving order.
func parseCouncilMembers(raw []string) ([]councilMemberReq, error) {
	members := make([]councilMemberReq, 0, len(raw))
	for _, entry := range raw {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			return nil, usageError{fmt.Errorf("--member cannot be empty")}
		}
		parts := strings.SplitN(trimmed, ":", 3)
		harness := strings.TrimSpace(parts[0])
		if harness == "" {
			return nil, usageError{fmt.Errorf("--member %q is missing a harness", entry)}
		}
		member := councilMemberReq{Harness: harness}
		if len(parts) > 1 {
			member.Model = strings.TrimSpace(parts[1])
		}
		if len(parts) > 2 {
			member.Effort = strings.TrimSpace(parts[2])
		}
		members = append(members, member)
	}
	return members, nil
}
