package cli

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

type automationDTO struct {
	ID          string                   `json:"id"`
	ProjectID   string                   `json:"projectId"`
	DisplayName string                   `json:"displayName"`
	Prompt      string                   `json:"prompt"`
	UseCase     string                   `json:"useCase,omitempty"`
	TemplateID  string                   `json:"templateId,omitempty"`
	Kind        string                   `json:"kind"`
	Harness     string                   `json:"harness,omitempty"`
	Model       string                   `json:"model,omitempty"`
	Mode        string                   `json:"mode,omitempty"`
	Effort      string                   `json:"effort,omitempty"`
	RRule       string                   `json:"rrule"`
	Timezone    string                   `json:"timezone"`
	Enabled     bool                     `json:"enabled"`
	NextRunAt   time.Time                `json:"nextRunAt"`
	LastRunAt   *time.Time               `json:"lastRunAt,omitempty"`
	LatestRun   *automationRunSummaryDTO `json:"latestRun,omitempty"`
	Trigger     string                   `json:"trigger,omitempty"`
	HookKey     string                   `json:"hookKey,omitempty"`

	HookSecretSet       bool     `json:"hookSecretSet,omitempty"`
	HookSignatureHeader string   `json:"hookSignatureHeader,omitempty"`
	HookFilters         []string `json:"hookFilters,omitempty"`
	HookDedupeKey       string   `json:"hookDedupeKey,omitempty"`
}
type automationRunSummaryDTO struct {
	ID           string    `json:"id"`
	Status       string    `json:"status"`
	SessionID    string    `json:"sessionId,omitempty"`
	ErrorMessage string    `json:"errorMessage,omitempty"`
	ScheduledFor time.Time `json:"scheduledFor"`
}
type automationRunDTO struct {
	ID           string     `json:"id"`
	AutomationID string     `json:"automationId"`
	SessionID    string     `json:"sessionId,omitempty"`
	Status       string     `json:"status"`
	ErrorMessage string     `json:"errorMessage,omitempty"`
	ScheduledFor time.Time  `json:"scheduledFor"`
	AttemptCount int64      `json:"attemptCount"`
	StartedAt    *time.Time `json:"startedAt,omitempty"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
}
type automationEnvelopeDTO struct {
	Automation automationDTO `json:"automation"`
}
type automationListDTO struct {
	Automations []automationDTO `json:"automations"`
	NextCursor  string          `json:"nextCursor,omitempty"`
}
type automationRunsDTO struct {
	Runs       []automationRunDTO `json:"runs"`
	NextCursor string             `json:"nextCursor,omitempty"`
}
type automationCreateDTO struct {
	ProjectID   string `json:"projectId"`
	DisplayName string `json:"displayName"`
	Prompt      string `json:"prompt"`
	UseCase     string `json:"useCase,omitempty"`
	TemplateID  string `json:"templateId,omitempty"`
	Kind        string `json:"kind"`
	Harness     string `json:"harness,omitempty"`
	RRule       string `json:"rrule,omitempty"`
	Cron        string `json:"cron,omitempty"`
	Timezone    string `json:"timezone,omitempty"`
	Enabled     *bool  `json:"enabled,omitempty"`

	Trigger             string   `json:"trigger,omitempty"`
	HookSecret          string   `json:"hookSecret,omitempty"`
	HookSignatureHeader string   `json:"hookSignatureHeader,omitempty"`
	HookFilters         []string `json:"hookFilters,omitempty"`
	HookDedupeKey       string   `json:"hookDedupeKey,omitempty"`
}
type automationUpdateDTO struct {
	DisplayName *string `json:"displayName,omitempty"`
	Prompt      *string `json:"prompt,omitempty"`
	UseCase     *string `json:"useCase,omitempty"`
	Kind        *string `json:"kind,omitempty"`
	Harness     *string `json:"harness,omitempty"`
	RRule       *string `json:"rrule,omitempty"`
	Cron        *string `json:"cron,omitempty"`
	Timezone    *string `json:"timezone,omitempty"`
	Enabled     *bool   `json:"enabled,omitempty"`

	HookSecret          *string   `json:"hookSecret,omitempty"`
	HookSignatureHeader *string   `json:"hookSignatureHeader,omitempty"`
	HookFilters         *[]string `json:"hookFilters,omitempty"`
	HookDedupeKey       *string   `json:"hookDedupeKey,omitempty"`
}

func newAutomationCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{Use: "automation", Aliases: []string{"automations"}, Short: "Manage recurring session automations"}
	cmd.AddCommand(newAutomationCreateCommand(ctx), newAutomationListCommand(ctx), newAutomationGetCommand(ctx), newAutomationUpdateCommand(ctx), newAutomationDeleteCommand(ctx), newAutomationRunsCommand(ctx))
	return cmd
}

func newAutomationCreateCommand(ctx *commandContext) *cobra.Command {
	var project, name, prompt, rrule, cron, timezone, kind, harness, templateID, useCase, hookSecret, hookHeader, dedupeKey string
	var filters []string
	var disabled, whenCalled, jsonOutput bool
	cmd := &cobra.Command{Use: "create", Short: "Create a recurring or called automation", Args: usageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		if strings.TrimSpace(project) == "" || strings.TrimSpace(name) == "" || strings.TrimSpace(prompt) == "" {
			return usageError{errors.New("--project, --name, and --prompt are required")}
		}
		trigger := ""
		if whenCalled {
			if rrule != "" || cron != "" || timezone != "" {
				return usageError{errors.New("--when-called cannot be used with --rrule, --cron or --timezone")}
			}
			trigger = "call"
		} else if (strings.TrimSpace(rrule) == "") == (strings.TrimSpace(cron) == "") {
			return usageError{errors.New("exactly one of --rrule, --cron or --when-called is required")}
		}
		if !whenCalled && timezone == "" {
			timezone = ctx.deps.Now().Location().String()
			if timezone == "Local" || timezone == "" {
				return usageError{errors.New("--timezone is required when the local IANA timezone cannot be resolved")}
			}
		}
		enabled := !disabled
		body := automationCreateDTO{TemplateID: templateID, UseCase: useCase, ProjectID: project, DisplayName: name, Prompt: prompt, Kind: kind, Harness: harness, RRule: rrule, Cron: cron, Timezone: timezone, Enabled: &enabled,
			Trigger: trigger, HookSecret: hookSecret, HookSignatureHeader: hookHeader, HookFilters: filters, HookDedupeKey: dedupeKey}
		var response automationEnvelopeDTO
		if err := ctx.postJSON(cmd.Context(), "automations", body, &response); err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd.OutOrStdout(), response)
		}
		a := response.Automation
		if a.Trigger == "call" {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "created automation %s (%s), runs on POST /hooks/%s at this machine's Remote access address\n", a.ID, a.DisplayName, a.HookKey)
			return err
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "created automation %s (%s), next run %s\n", a.ID, a.DisplayName, formatAutomationTime(a.NextRunAt))
		return err
	}}
	cmd.Flags().StringVar(&templateID, "template-id", "custom", "Starter template origin")
	cmd.Flags().StringVar(&useCase, "use-case", "unknown", "Automation purpose category")
	cmd.Flags().StringVar(&project, "project", "", "Project id")
	cmd.Flags().StringVar(&name, "name", "", "Display name")
	cmd.Flags().StringVar(&prompt, "prompt", "", "Session task prompt")
	cmd.Flags().StringVar(&rrule, "rrule", "", "RFC 5545 recurrence rule")
	cmd.Flags().StringVar(&cron, "cron", "", "Supported five-field cron expression")
	cmd.Flags().StringVar(&timezone, "timezone", "", "IANA timezone (defaults to local zone when available)")
	cmd.Flags().StringVar(&kind, "kind", "worker", "Session kind: worker or orchestrator")
	cmd.Flags().StringVar(&harness, "harness", "", "Agent harness (empty uses project default)")
	cmd.Flags().BoolVar(&disabled, "disabled", false, "Create disabled")
	addHookFlags(cmd, &hookSecret, &hookHeader, &filters, &dedupeKey)
	cmd.Flags().BoolVar(&whenCalled, "when-called", false, "Run when a POST reaches /hooks/<key> instead of on a schedule")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func addHookFlags(cmd *cobra.Command, secret, header *string, filters *[]string, dedupeKey *string) {
	cmd.Flags().StringVar(secret, "hook-secret", "", "HMAC-SHA256 secret that signs each call's body")
	cmd.Flags().StringVar(header, "hook-signature-header", "", "Header that carries the body signature, such as X-Hub-Signature-256")
	cmd.Flags().StringArrayVar(filters, "filter", nil, "Run only when the call matches, such as body.action=opened (repeatable; --filter \"\" on update clears them)")
	cmd.Flags().StringVar(dedupeKey, "dedupe-key", "", "Template that names one delivery, such as {{headers.X-GitHub-Delivery}}")
}

func automationWhen(a automationDTO) string {
	if a.Trigger == "call" {
		return "when called"
	}
	return formatAutomationTime(a.NextRunAt)
}

func newAutomationListCommand(ctx *commandContext) *cobra.Command {
	var project string
	var enabled, jsonOutput bool
	cmd := &cobra.Command{Use: "list", Short: "List automations", Args: usageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		query := url.Values{}
		query.Set("limit", "100")
		if project != "" {
			query.Set("projectId", project)
		}
		if cmd.Flags().Changed("enabled") {
			query.Set("enabled", strconv.FormatBool(enabled))
		}
		response := automationListDTO{Automations: []automationDTO{}}
		seenCursors := make(map[string]bool)
		for {
			path := "automations"
			if encoded := query.Encode(); encoded != "" {
				path += "?" + encoded
			}
			var page automationListDTO
			if err := ctx.getJSON(cmd.Context(), path, &page); err != nil {
				return err
			}
			response.Automations = append(response.Automations, page.Automations...)
			if page.NextCursor == "" {
				break
			}
			if seenCursors[page.NextCursor] {
				return errors.New("automation list pagination returned a repeated cursor")
			}
			seenCursors[page.NextCursor] = true
			query.Set("cursor", page.NextCursor)
		}
		if jsonOutput {
			return writeJSON(cmd.OutOrStdout(), response)
		}
		writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(writer, "ID\tNAME\tPROJECT\tENABLED\tNEXT\tLATEST")
		for _, item := range response.Automations {
			latest := "-"
			if item.LatestRun != nil {
				latest = item.LatestRun.Status
			}
			_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%t\t%s\t%s\n", item.ID, item.DisplayName, item.ProjectID, item.Enabled, automationWhen(item), latest)
		}
		return writer.Flush()
	}}
	cmd.Flags().StringVar(&project, "project", "", "Filter by project id")
	cmd.Flags().BoolVar(&enabled, "enabled", false, "Filter by enabled state")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func newAutomationGetCommand(ctx *commandContext) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{Use: "get <id>", Short: "Get an automation", Args: usageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		var response automationEnvelopeDTO
		if err := ctx.getJSON(cmd.Context(), "automations/"+url.PathEscape(args[0]), &response); err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd.OutOrStdout(), response)
		}
		a := response.Automation
		if a.Trigger == "call" {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\nProject: %s\nRuns: when called at POST /hooks/%s\nEnabled: %t\nPrompt: %s\n", a.DisplayName, a.ProjectID, a.HookKey, a.Enabled, a.Prompt)
			return err
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\nProject: %s\nSchedule: %s (%s)\nEnabled: %t\nNext run: %s\nPrompt: %s\n", a.DisplayName, a.ProjectID, a.RRule, a.Timezone, a.Enabled, formatAutomationTime(a.NextRunAt), a.Prompt)
		return err
	}}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func newAutomationUpdateCommand(ctx *commandContext) *cobra.Command {
	var name, prompt, rrule, cron, timezone, kind, harness, useCase, hookSecret, hookHeader, dedupeKey string
	var filters []string
	var enabled, jsonOutput bool
	cmd := &cobra.Command{Use: "update <id>", Short: "Update an automation", Args: usageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		body := automationUpdateDTO{}
		changed := false
		stringFlag := func(flag string, value string, target **string) {
			if cmd.Flags().Changed(flag) {
				flagValue := value
				*target = &flagValue
				changed = true
			}
		}
		stringFlag("name", name, &body.DisplayName)
		stringFlag("prompt", prompt, &body.Prompt)
		stringFlag("use-case", useCase, &body.UseCase)
		stringFlag("rrule", rrule, &body.RRule)
		stringFlag("cron", cron, &body.Cron)
		stringFlag("timezone", timezone, &body.Timezone)
		stringFlag("kind", kind, &body.Kind)
		stringFlag("harness", harness, &body.Harness)
		stringFlag("hook-secret", hookSecret, &body.HookSecret)
		stringFlag("hook-signature-header", hookHeader, &body.HookSignatureHeader)
		stringFlag("dedupe-key", dedupeKey, &body.HookDedupeKey)
		if cmd.Flags().Changed("filter") {
			filters = slices.DeleteFunc(filters, func(f string) bool { return strings.TrimSpace(f) == "" })
			body.HookFilters = &filters
			changed = true
		}
		if cmd.Flags().Changed("enabled") {
			body.Enabled = &enabled
			changed = true
		}
		if !changed {
			return usageError{errors.New("provide at least one field to update")}
		}
		if body.RRule != nil && body.Cron != nil {
			return usageError{errors.New("--rrule and --cron cannot be used together")}
		}
		var response automationEnvelopeDTO
		if err := ctx.patchJSON(cmd.Context(), "automations/"+url.PathEscape(args[0]), body, &response); err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd.OutOrStdout(), response)
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "updated automation %s (%s)\n", response.Automation.ID, response.Automation.DisplayName)
		return err
	}}
	cmd.Flags().StringVar(&useCase, "use-case", "", "New automation purpose category")
	cmd.Flags().StringVar(&name, "name", "", "New display name")
	cmd.Flags().StringVar(&prompt, "prompt", "", "New task prompt")
	cmd.Flags().StringVar(&rrule, "rrule", "", "Replace schedule with RRule")
	cmd.Flags().StringVar(&cron, "cron", "", "Replace schedule with cron")
	cmd.Flags().StringVar(&timezone, "timezone", "", "IANA timezone")
	cmd.Flags().StringVar(&kind, "kind", "", "Session kind")
	cmd.Flags().StringVar(&harness, "harness", "", "Harness; empty uses project default")
	cmd.Flags().BoolVar(&enabled, "enabled", true, "Enable or disable future dispatch")
	addHookFlags(cmd, &hookSecret, &hookHeader, &filters, &dedupeKey)
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func newAutomationDeleteCommand(ctx *commandContext) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{Use: "delete <id>", Aliases: []string{"rm"}, Short: "Delete an automation and run history", Args: usageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		if !yes {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Delete automation %q and its run history? Type the automation id to confirm: ", id)
			var answer string
			if _, err := fmt.Fscanln(cmd.InOrStdin(), &answer); err != nil {
				return err
			}
			if answer != id {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "aborted")
				return err
			}
		}
		if err := ctx.deleteJSON(cmd.Context(), "automations/"+url.PathEscape(id), nil); err != nil {
			return err
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "deleted automation %s\n", id)
		return err
	}}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip confirmation prompt")
	return cmd
}

func newAutomationRunsCommand(ctx *commandContext) *cobra.Command {
	var limit int
	var cursor string
	var jsonOutput bool
	cmd := &cobra.Command{Use: "runs <id>", Short: "List automation run history", Args: usageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if limit < 1 || limit > 100 {
			return usageError{errors.New("--limit must be between 1 and 100")}
		}
		query := url.Values{}
		query.Set("limit", strconv.Itoa(limit))
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		var response automationRunsDTO
		if err := ctx.getJSON(cmd.Context(), "automations/"+url.PathEscape(args[0])+"/runs?"+query.Encode(), &response); err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd.OutOrStdout(), response)
		}
		writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(writer, "SCHEDULED\tSTATUS\tSESSION\tERROR")
		for _, run := range response.Runs {
			_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", formatAutomationTime(run.ScheduledFor), run.Status, emptyDash(run.SessionID), emptyDash(run.ErrorMessage))
		}
		if response.NextCursor != "" {
			_, _ = fmt.Fprintf(writer, "next cursor: %s\n", response.NextCursor)
		}
		return writer.Flush()
	}}
	cmd.Flags().IntVar(&limit, "limit", 50, "Maximum runs (1-100)")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Opaque page cursor")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func formatAutomationTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Local().Format(time.RFC3339)
}
