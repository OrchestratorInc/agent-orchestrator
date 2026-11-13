package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// storageReportDTO mirrors the daemon's GET /api/v1/storage body.
type storageReportDTO struct {
	ScannedAt            *time.Time              `json:"scannedAt,omitempty"`
	ScanDurationMs       int64                   `json:"scanDurationMs"`
	Scanning             bool                    `json:"scanning"`
	ScanError            string                  `json:"scanError,omitempty"`
	TotalBytes           int64                   `json:"totalBytes"`
	ReclaimableBytes     int64                   `json:"reclaimableBytes"`
	AutoReclaimableBytes int64                   `json:"autoReclaimableBytes"`
	Disk                 storageDiskDTO          `json:"disk"`
	Categories           []storageCategoryDTO    `json:"categories"`
	Worktrees            []storageWorktreeDTO    `json:"worktrees"`
	Plan                 []storagePlannedDTO     `json:"plan"`
	Settings             storageSettingsDTO      `json:"settings"`
	AutoCleanAllowed     bool                    `json:"autoCleanAllowed"`
	CopyOnWrite          bool                    `json:"copyOnWrite"`
	Cleaning             bool                    `json:"cleaning"`
	LastClean            *storageCleanResponseTO `json:"lastClean,omitempty"`
}

type storageDiskDTO struct {
	Path       string `json:"path"`
	TotalBytes int64  `json:"totalBytes"`
	FreeBytes  int64  `json:"freeBytes"`
	Level      string `json:"level"`
	Error      string `json:"error,omitempty"`
}

type storageCategoryDTO struct {
	Category         string `json:"category"`
	Bytes            int64  `json:"bytes"`
	ReclaimableBytes int64  `json:"reclaimableBytes"`
}

type storageWorktreeDTO struct {
	Path           string `json:"path"`
	SessionID      string `json:"sessionId,omitempty"`
	State          string `json:"state"`
	Bytes          int64  `json:"bytes"`
	Offloaded      bool   `json:"offloaded"`
	OffloadedBytes int64  `json:"offloadedBytes"`
	RestoreError   string `json:"restoreError,omitempty"`
}

type storagePlannedDTO struct {
	Kind      string `json:"kind"`
	Target    string `json:"target"`
	SessionID string `json:"sessionId,omitempty"`
	Reason    string `json:"reason"`
	Bytes     int64  `json:"bytes"`
	Automatic bool   `json:"automatic"`
}

type storageSettingsDTO struct {
	AutoClean                bool `json:"autoClean"`
	OffloadEnabled           bool `json:"offloadEnabled"`
	OffloadIdleHours         int  `json:"offloadIdleHours"`
	OffloadNoPRGraceHours    int  `json:"offloadNoPrGraceHours"`
	OffloadMergedIdleMinutes int  `json:"offloadMergedIdleMinutes"`
	EndedWorktreeDays        int  `json:"endedWorktreeDays"`
	BackupDays               int  `json:"backupDays"`
	LogDays                  int  `json:"logDays"`
	CleanDevProfiles         bool `json:"cleanDevProfiles"`
	DevProfileDays           int  `json:"devProfileDays"`
	TemplateDays             int  `json:"templateDays"`
}

type storageCleanRequestTO struct {
	Kinds      []string `json:"kinds,omitempty"`
	SessionIDs []string `json:"sessionIds,omitempty"`
	DryRun     bool     `json:"dryRun,omitempty"`
	IncludeDev bool     `json:"includeDev,omitempty"`
}

type storageCleanResponseTO struct {
	StartedAt  time.Time          `json:"startedAt"`
	FinishedAt *time.Time         `json:"finishedAt,omitempty"`
	Running    bool               `json:"running"`
	DryRun     bool               `json:"dryRun"`
	FreedBytes int64              `json:"freedBytes"`
	Actions    []storageActionDTO `json:"actions"`
}

type storageActionDTO struct {
	ID        int64     `json:"id,omitempty"`
	At        time.Time `json:"at"`
	Kind      string    `json:"kind"`
	Target    string    `json:"target"`
	SessionID string    `json:"sessionId,omitempty"`
	Reason    string    `json:"reason"`
	Bytes     int64     `json:"bytes"`
	DryRun    bool      `json:"dryRun"`
	Result    string    `json:"result"`
	Error     string    `json:"error,omitempty"`
}

type storageActionsResponseTO struct {
	Actions []storageActionDTO `json:"actions"`
}

// storagePollInterval is how often the CLI re-reads the report while the
// daemon scans or cleans.
const storagePollInterval = 2 * time.Second

var storageCategoryLabels = map[string]string{
	"worktrees":            "Session worktrees",
	"database":             "Database",
	"db_backups":           "Database backups",
	"harnesses":            "Agent CLIs",
	"browser_data":         "Browser data",
	"logs":                 "Logs",
	"dev_profiles":         "Dev-build profiles",
	"dependency_templates": "Dependency templates",
	"other":                "Other",
}

var storageKindLabels = map[string]string{
	"offload":              "Offload dependencies of quiet sessions",
	"ended_worktrees":      "Remove worktrees of ended sessions",
	"orphan_worktrees":     "Remove clean worktrees no session owns",
	"db_backups":           "Remove old database backups",
	"logs":                 "Remove old logs",
	"dev_profiles":         "Remove unused dev-build profiles",
	"dependency_templates": "Remove unused dependency templates",
	"database":             "Shrink the database",
	"trash":                "Finish earlier deletes",
	"restore":              "Restore dependencies",
}

func newStorageCommand(ctx *commandContext) *cobra.Command {
	var jsonOut, refresh bool
	cmd := &cobra.Command{
		Use:   "storage",
		Short: "Show how much disk space Orchestrator uses and what cleanup would free",
		Long: "Show how much disk space Orchestrator uses under its data folder, by category, and how much its cleanup policy would free.\n\n" +
			"Open sessions never lose source, uncommitted work or history. Quiet sessions may have reinstallable dependencies offloaded; " +
			"they come back automatically when the session resumes.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			report, err := ctx.storageReport(cmd.Context(), cmd, refresh)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), report)
			}
			return writeStorageReport(cmd.OutOrStdout(), report)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output the report as JSON")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "Measure again before reporting")
	cmd.AddCommand(newStorageCleanCommand(ctx))
	cmd.AddCommand(newStorageLogCommand(ctx))
	cmd.AddCommand(newStorageSettingsCommand(ctx))
	return cmd
}

// storageReport reads the report, waiting while the daemon measures.
func (c *commandContext) storageReport(ctx context.Context, cmd *cobra.Command, refresh bool) (storageReportDTO, error) {
	var report storageReportDTO
	if refresh {
		if err := c.postJSON(ctx, "storage/scan", struct{}{}, &report); err != nil {
			return report, err
		}
	} else if err := c.getJSON(ctx, "storage", &report); err != nil {
		return report, err
	}
	return c.waitStorage(ctx, cmd, report, func(r storageReportDTO) bool { return r.Scanning }, "Measuring disk usage...")
}

// waitStorage polls the report until busy reports false.
func (c *commandContext) waitStorage(ctx context.Context, cmd *cobra.Command, report storageReportDTO, busy func(storageReportDTO) bool, message string) (storageReportDTO, error) {
	announced := false
	for busy(report) {
		if !announced {
			if _, err := fmt.Fprintln(cmd.ErrOrStderr(), message); err != nil {
				return report, err
			}
			announced = true
		}
		c.deps.Sleep(storagePollInterval)
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if err := c.getJSON(ctx, "storage", &report); err != nil {
			return report, err
		}
	}
	return report, nil
}

func writeStorageReport(out io.Writer, report storageReportDTO) error {
	var b strings.Builder
	if report.Disk.TotalBytes > 0 {
		fmt.Fprintf(&b, "Disk: %s free of %s (%s)\n", humanBytes(report.Disk.FreeBytes), humanBytes(report.Disk.TotalBytes), report.Disk.Level)
	}
	if report.ScanError != "" {
		fmt.Fprintf(&b, "Last measurement failed: %s\n", report.ScanError)
	}
	b.WriteString("\n")
	var rows strings.Builder
	rows.WriteString("CATEGORY\tSIZE\tCAN FREE\n")
	for _, category := range report.Categories {
		if category.Bytes == 0 && category.ReclaimableBytes == 0 {
			continue
		}
		label := storageCategoryLabels[category.Category]
		if label == "" {
			label = category.Category
		}
		free := "-"
		if category.ReclaimableBytes > 0 {
			free = humanBytes(category.ReclaimableBytes)
		}
		fmt.Fprintf(&rows, "%s\t%s\t%s\n", label, humanBytes(category.Bytes), free)
	}
	fmt.Fprintf(&rows, "Total\t%s\t%s\n", humanBytes(report.TotalBytes), humanBytes(report.ReclaimableBytes))
	tw := tabwriter.NewWriter(&b, 0, 0, 3, ' ', 0)
	if _, err := io.WriteString(tw, rows.String()); err != nil {
		return err
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	offloaded, freed := 0, int64(0)
	var failures []string
	for _, wt := range report.Worktrees {
		if wt.Offloaded {
			offloaded++
			freed += wt.OffloadedBytes
		}
		if wt.RestoreError != "" {
			failures = append(failures, fmt.Sprintf("  %s: %s", wt.SessionID, wt.RestoreError))
		}
	}
	if offloaded > 0 {
		fmt.Fprintf(&b, "\nOffloaded sessions: %d (%s freed; restored automatically on resume)\n", offloaded, humanBytes(freed))
	}
	if len(failures) > 0 {
		b.WriteString("\nDependency restores that need attention:\n")
		b.WriteString(strings.Join(failures, "\n"))
		b.WriteString("\n")
	}
	s := report.Settings
	autoState := "on"
	switch {
	case !report.AutoCleanAllowed:
		autoState = "off (AO_STORAGE_AUTOCLEAN)"
	case !s.AutoClean:
		autoState = "off"
	}
	fmt.Fprintf(&b, "\nAuto-clean: %s; %s would be freed automatically now\n", autoState, humanBytes(report.AutoReclaimableBytes))
	if s.OffloadEnabled {
		fmt.Fprintf(&b, "Offload: after %dh quiet (%dh with no pull request, %dm after merge)\n", s.OffloadIdleHours, s.OffloadNoPRGraceHours, s.OffloadMergedIdleMinutes)
	} else {
		b.WriteString("Offload: off\n")
	}
	if s.EndedWorktreeDays > 0 {
		fmt.Fprintf(&b, "Ended session worktrees: removed %d days after the session ends\n", s.EndedWorktreeDays)
	}
	if report.ReclaimableBytes > 0 {
		b.WriteString("\nRun `ao storage clean --dry-run` to preview a cleanup.\n")
	}
	_, err := io.WriteString(out, b.String())
	return err
}

type storageCleanOptions struct {
	only       []string
	sessions   []string
	includeDev bool
	dryRun     bool
	yes        bool
	json       bool
}

func newStorageCleanCommand(ctx *commandContext) *cobra.Command {
	var opts storageCleanOptions
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Free disk space Orchestrator no longer needs",
		Long: "Free disk space by running the storage cleanup now. Kinds: " + strings.Join(storageCleanKindNames(), ", ") + ".\n\n" +
			"Worktrees with uncommitted changes are never removed, sessions whose agent may be running are never offloaded, " +
			"and git branches and pull requests are kept. Dev-build profiles are included only with --include-dev or --only dev_profiles.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.cleanStorage(cmd.Context(), cmd, opts)
		},
	}
	cmd.Flags().StringSliceVar(&opts.only, "only", nil, "Limit the cleanup to these kinds (comma-separated)")
	cmd.Flags().StringArrayVar(&opts.sessions, "session", nil, "Limit session cleanups (offload, ended worktrees) to this session; repeatable")
	cmd.Flags().BoolVar(&opts.includeDev, "include-dev", false, "Also remove unused dev-build profiles")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Show what would be cleaned without changing anything")
	cmd.Flags().BoolVarP(&opts.yes, "yes", "y", false, "Skip the confirmation prompt")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output the plan or result as JSON")
	return cmd
}

func storageCleanKindNames() []string {
	names := make([]string, 0, len(domain.StorageCleanKinds))
	for _, kind := range domain.StorageCleanKinds {
		names = append(names, string(kind))
	}
	return names
}

func (c *commandContext) cleanStorage(ctx context.Context, cmd *cobra.Command, opts storageCleanOptions) error {
	req := storageCleanRequestTO{IncludeDev: opts.includeDev, DryRun: true}
	for _, raw := range opts.only {
		kind := strings.TrimSpace(raw)
		if kind == "" {
			// Dropping it would turn a narrowed cleanup into a cleanup of
			// every kind.
			return usageError{errors.New("--only needs a cleanup kind; an empty value is not allowed")}
		}
		if _, ok := domain.ParseStorageCleanKind(kind); !ok {
			return usageError{fmt.Errorf("unknown cleanup kind %q (want one of %s)", kind, strings.Join(storageCleanKindNames(), ", "))}
		}
		req.Kinds = append(req.Kinds, kind)
	}
	for _, raw := range opts.sessions {
		id, err := normalizeSessionID(raw)
		if err != nil {
			return err
		}
		req.SessionIDs = append(req.SessionIDs, id)
	}
	preview, err := c.storageClean(ctx, cmd, req)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if opts.dryRun || len(preview.Actions) == 0 {
		if opts.json {
			return writeJSON(out, preview)
		}
		if err := writeStoragePlan(out, preview); err != nil {
			return err
		}
		if opts.dryRun && len(preview.Actions) > 0 {
			_, err = fmt.Fprintln(out, "\n(dry-run: nothing was changed)")
		}
		return err
	}
	if !opts.json {
		if err := writeStoragePlan(out, preview); err != nil {
			return err
		}
	}
	if !opts.yes {
		confirmed, err := confirmTyped(cmd, fmt.Sprintf("\nFree about %s? Type yes to confirm: ", humanBytes(preview.FreedBytes)))
		if err != nil {
			return err
		}
		if !confirmed {
			_, err := fmt.Fprintln(out, "aborted")
			return err
		}
	}
	req.DryRun = false
	if _, err := c.storageClean(ctx, cmd, req); err != nil {
		return err
	}
	var report storageReportDTO
	if err := c.getJSON(ctx, "storage", &report); err != nil {
		return err
	}
	report, err = c.waitStorage(ctx, cmd, report, func(r storageReportDTO) bool { return r.Cleaning }, "Cleaning up...")
	if err != nil {
		return err
	}
	if report.LastClean == nil {
		_, err := fmt.Fprintln(out, "Cleanup finished.")
		return err
	}
	if opts.json {
		if err := writeJSON(out, report.LastClean); err != nil {
			return err
		}
	} else if err := writeStorageResult(out, *report.LastClean); err != nil {
		return err
	}
	return storageCleanFailure(*report.LastClean)
}

// storageCleanFailure turns failed cleanup actions into a non-zero exit, so a
// script never reads a failed cleanup as a success. Skipped actions (a dirty
// worktree, a running agent) are expected and do not fail the command.
func storageCleanFailure(result storageCleanResponseTO) error {
	failed := 0
	for _, action := range result.Actions {
		if action.Result == "failed" {
			failed++
		}
	}
	if failed == 0 {
		return nil
	}
	return fmt.Errorf("%d cleanup action%s failed; see `ao storage log`", failed, pluralS(failed))
}

// storageClean posts a cleanup request, waiting for the first measurement
// when the daemon has none yet.
func (c *commandContext) storageClean(ctx context.Context, cmd *cobra.Command, req storageCleanRequestTO) (storageCleanResponseTO, error) {
	var res storageCleanResponseTO
	err := c.postJSON(ctx, "storage/clean", req, &res)
	var responseErr apiResponseError
	if errors.As(err, &responseErr) && responseErr.ErrorBody.Code == "STORAGE_SCAN_REQUIRED" {
		if _, err := c.storageReport(ctx, cmd, false); err != nil {
			return res, err
		}
		err = c.postJSON(ctx, "storage/clean", req, &res)
	}
	return res, err
}

func writeStoragePlan(out io.Writer, plan storageCleanResponseTO) error {
	if len(plan.Actions) == 0 {
		_, err := fmt.Fprintln(out, "Nothing to clean up.")
		return err
	}
	type group struct {
		count   int
		bytes   int64
		targets []string
	}
	groups := map[string]*group{}
	var kinds []string
	for _, action := range plan.Actions {
		g, ok := groups[action.Kind]
		if !ok {
			g = &group{}
			groups[action.Kind] = g
			kinds = append(kinds, action.Kind)
		}
		g.count++
		g.bytes += action.Bytes
		target := action.Target
		if action.SessionID != "" {
			target = action.SessionID
		}
		g.targets = append(g.targets, fmt.Sprintf("%s (%s; %s)", target, humanBytes(action.Bytes), action.Reason))
	}
	sort.SliceStable(kinds, func(i, j int) bool { return groups[kinds[i]].bytes > groups[kinds[j]].bytes })
	var b strings.Builder
	for _, kind := range kinds {
		g := groups[kind]
		label := storageKindLabels[kind]
		if label == "" {
			label = kind
		}
		fmt.Fprintf(&b, "%s: %d item%s, %s\n", label, g.count, pluralS(g.count), humanBytes(g.bytes))
		for i, target := range g.targets {
			if i == 5 {
				fmt.Fprintf(&b, "  ... and %d more\n", len(g.targets)-5)
				break
			}
			fmt.Fprintf(&b, "  %s\n", target)
		}
	}
	fmt.Fprintf(&b, "\nTotal: about %s\n", humanBytes(plan.FreedBytes))
	_, err := io.WriteString(out, b.String())
	return err
}

func writeStorageResult(out io.Writer, result storageCleanResponseTO) error {
	counts := map[string]int{}
	var b strings.Builder
	for _, action := range result.Actions {
		counts[action.Result]++
		if action.Result == "skipped" || action.Result == "failed" {
			target := action.Target
			if action.SessionID != "" {
				target = action.SessionID
			}
			fmt.Fprintf(&b, "  %s: %s (%s)\n", strings.ToUpper(action.Result[:1])+action.Result[1:], target, action.Error)
		}
	}
	fmt.Fprintf(&b, "\nCleanup complete. Freed %s: %d done, %d skipped, %d failed.\n",
		humanBytes(result.FreedBytes), counts["done"], counts["skipped"], counts["failed"])
	_, err := io.WriteString(out, b.String())
	return err
}

func newStorageLogCommand(ctx *commandContext) *cobra.Command {
	var limit int
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Show what storage cleanups did, newest first",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit < 1 || limit > 1000 {
				return usageError{errors.New("--limit must be between 1 and 1000")}
			}
			params := url.Values{}
			params.Set("limit", strconv.Itoa(limit))
			var res storageActionsResponseTO
			if err := ctx.getJSON(cmd.Context(), apiPath("storage/actions", params), &res); err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return writeStorageLog(cmd.OutOrStdout(), res.Actions)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "How many entries to show (1-1000)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output entries as JSON")
	return cmd
}

func writeStorageLog(out io.Writer, actions []storageActionDTO) error {
	if len(actions) == 0 {
		_, err := fmt.Fprintln(out, "No storage cleanups yet.")
		return err
	}
	var rows strings.Builder
	rows.WriteString("TIME\tKIND\tRESULT\tSIZE\tTARGET\n")
	for _, action := range actions {
		result := action.Result
		if action.DryRun {
			result += " (dry run)"
		}
		target := action.Target
		if action.SessionID != "" {
			target = action.SessionID
		}
		if action.Error != "" {
			target += " - " + action.Error
		}
		fmt.Fprintf(&rows, "%s\t%s\t%s\t%s\t%s\n", action.At.Local().Format("2006-01-02 15:04"), action.Kind, result, humanBytes(action.Bytes), target)
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if _, err := io.WriteString(tw, rows.String()); err != nil {
		return err
	}
	return tw.Flush()
}

type storageSettingsFlags struct {
	autoClean     string
	offload       string
	cleanDev      string
	offloadIdle   int
	offloadGrace  int
	offloadMerged int
	endedDays     int
	backupDays    int
	logDays       int
	devDays       int
	templateDays  int
	json          bool
}

func newStorageSettingsCommand(ctx *commandContext) *cobra.Command {
	var f storageSettingsFlags
	cmd := &cobra.Command{
		Use:   "settings",
		Short: "Show or change storage cleanup settings",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.storageSettings(cmd, f)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&f.autoClean, "auto-clean", "", "Let the daemon clean up on its own: on or off")
	flags.StringVar(&f.offload, "offload", "", "Offload dependencies of quiet sessions: on or off")
	flags.StringVar(&f.cleanDev, "clean-dev-profiles", "", "Remove unused dev-build profiles automatically: on or off")
	flags.IntVar(&f.offloadIdle, "offload-idle-hours", 0, "Quiet hours before offloading a session with an open pull request")
	flags.IntVar(&f.offloadGrace, "offload-no-pr-hours", 0, "Quiet hours before offloading a session with no pull request")
	flags.IntVar(&f.offloadMerged, "offload-merged-minutes", 0, "Quiet minutes before offloading a session whose pull requests merged or closed")
	flags.IntVar(&f.endedDays, "ended-worktree-days", 0, "Days before an ended session's worktree is removed (0 never)")
	flags.IntVar(&f.backupDays, "backup-days", 0, "Days to keep old database backups")
	flags.IntVar(&f.logDays, "log-days", 0, "Days to keep unused log files")
	flags.IntVar(&f.devDays, "dev-profile-days", 0, "Days a dev-build profile must be unused before removal")
	flags.IntVar(&f.templateDays, "template-days", 0, "Days to keep an unused dependency template")
	flags.BoolVar(&f.json, "json", false, "Output settings as JSON")
	return cmd
}

func parseOnOff(name, raw string) (*bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return nil, nil
	case "on", "true", "yes":
		v := true
		return &v, nil
	case "off", "false", "no":
		v := false
		return &v, nil
	default:
		return nil, usageError{fmt.Errorf("--%s must be on or off", name)}
	}
}

func (c *commandContext) storageSettings(cmd *cobra.Command, f storageSettingsFlags) error {
	patch := map[string]any{}
	for _, flag := range []struct {
		name, key string
		raw       string
	}{{"auto-clean", "autoClean", f.autoClean}, {"offload", "offloadEnabled", f.offload}, {"clean-dev-profiles", "cleanDevProfiles", f.cleanDev}} {
		value, err := parseOnOff(flag.name, flag.raw)
		if err != nil {
			return err
		}
		if value != nil {
			patch[flag.key] = *value
		}
	}
	for _, flag := range []struct {
		name, key string
		value     int
	}{
		{"offload-idle-hours", "offloadIdleHours", f.offloadIdle},
		{"offload-no-pr-hours", "offloadNoPrGraceHours", f.offloadGrace},
		{"offload-merged-minutes", "offloadMergedIdleMinutes", f.offloadMerged},
		{"ended-worktree-days", "endedWorktreeDays", f.endedDays},
		{"backup-days", "backupDays", f.backupDays},
		{"log-days", "logDays", f.logDays},
		{"dev-profile-days", "devProfileDays", f.devDays},
		{"template-days", "templateDays", f.templateDays},
	} {
		if cmd.Flags().Changed(flag.name) {
			if flag.value < 0 {
				return usageError{fmt.Errorf("--%s must not be negative", flag.name)}
			}
			patch[flag.key] = flag.value
		}
	}
	var settings storageSettingsDTO
	if len(patch) > 0 {
		if err := c.patchJSON(cmd.Context(), "storage/settings", patch, &settings); err != nil {
			return err
		}
	} else {
		var report storageReportDTO
		if err := c.getJSON(cmd.Context(), "storage", &report); err != nil {
			return err
		}
		settings = report.Settings
	}
	if f.json {
		return writeJSON(cmd.OutOrStdout(), settings)
	}
	onOff := func(v bool) string {
		if v {
			return "on"
		}
		return "off"
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(),
		"auto-clean:             %s\noffload:                %s\noffload-idle-hours:     %d\noffload-no-pr-hours:    %d\noffload-merged-minutes: %d\nended-worktree-days:    %d\nbackup-days:            %d\nlog-days:               %d\nclean-dev-profiles:     %s\ndev-profile-days:       %d\ntemplate-days:          %d\n",
		onOff(settings.AutoClean), onOff(settings.OffloadEnabled), settings.OffloadIdleHours, settings.OffloadNoPRGraceHours,
		settings.OffloadMergedIdleMinutes, settings.EndedWorktreeDays, settings.BackupDays, settings.LogDays,
		onOff(settings.CleanDevProfiles), settings.DevProfileDays, settings.TemplateDays)
	return err
}
