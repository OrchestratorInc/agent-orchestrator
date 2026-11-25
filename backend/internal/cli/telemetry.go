package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// telemetryStatusResponse mirrors httpd.TelemetryStatus.
type telemetryStatusResponse struct {
	AnalyticsSending bool   `json:"analyticsSending"`
	OptedOut         bool   `json:"optedOut"`
	DoNotTrack       bool   `json:"doNotTrack"`
	RemoteConfigured bool   `json:"remoteConfigured"`
	ShareIdentity    bool   `json:"shareIdentity"`
	ErrorReports     bool   `json:"errorReports"`
	LocalLog         bool   `json:"localLog"`
	RetentionDays    int    `json:"retentionDays"`
	InstallID        string `json:"installId,omitempty"`
}

// telemetryLogEvent mirrors telemetry.ExportedEvent.
type telemetryLogEvent struct {
	OccurredAt time.Time      `json:"occurredAt"`
	Event      string         `json:"event"`
	Surface    string         `json:"surface"`
	DistinctID string         `json:"distinctId,omitempty"`
	Properties map[string]any `json:"properties"`
}

func newTelemetryCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "telemetry",
		Short: "See and control what Orchestrator sends as telemetry",
		Long: "See and control product telemetry. `status` shows what is on, `log` shows a local record of the events " +
			"sent and their properties, and `off`/`on` switch Share usage analytics for the desktop, the daemon and a " +
			"paired phone. Error reports have their own Settings switch; DO_NOT_TRACK=1 turns off both.",
	}
	cmd.AddCommand(newTelemetryStatusCommand(ctx), newTelemetryLogCommand(ctx), newTelemetrySwitchCommand(ctx, "off", true), newTelemetrySwitchCommand(ctx, "on", false))
	return cmd
}

func newTelemetryStatusCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show which telemetry is on",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var status telemetryStatusResponse
			if err := ctx.doJSONPath(cmd.Context(), http.MethodGet, "/internal/telemetry/status", nil, &status); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), status)
			}
			return writeTelemetryStatus(cmd.OutOrStdout(), status)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output JSON")
	return cmd
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func writeTelemetryStatus(w io.Writer, s telemetryStatusResponse) error {
	analytics := "on"
	switch {
	case s.DoNotTrack:
		analytics = "off (DO_NOT_TRACK)"
	case s.OptedOut:
		analytics = "off"
	case !s.RemoteConfigured:
		analytics = "on, but this daemon does not export (started without the desktop app)"
	}
	errorReports := onOff(s.ErrorReports)
	if s.DoNotTrack {
		errorReports = "off (DO_NOT_TRACK)"
	}
	lines := []string{
		"Share usage analytics:     " + analytics,
		"Include account identity:  " + onOff(s.ShareIdentity && !s.OptedOut),
		"Share error events:        " + errorReports,
	}
	if s.LocalLog {
		lines = append(lines, fmt.Sprintf("Local log of sent events:  kept %d days (`ao telemetry log`)", s.RetentionDays))
	} else {
		lines = append(lines, "Local log of sent events:  off (local telemetry events are disabled)")
	}
	if s.InstallID != "" {
		lines = append(lines, "Install ID:                "+s.InstallID)
	}
	lines = append(lines, "", "Change these in Settings > General > Privacy, with `ao telemetry off`, or with DO_NOT_TRACK=1.", "Details: "+telemetryDocsURL)
	_, err := fmt.Fprintln(w, strings.Join(lines, "\n"))
	return err
}

func newTelemetryLogCommand(ctx *commandContext) *cobra.Command {
	var (
		asJSON bool
		limit  int
		since  time.Duration
		event  string
	)
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Show a local record of the telemetry events sent and their properties",
		Long: "Lists a best-effort local record of telemetry events, newest first, with their properties: daemon and " +
			"CLI events once PostHog acknowledged them, and desktop app events as they were handed to PostHog, before " +
			"delivery is confirmed. It is not a complete delivery history, and phone events are not included. Kept " +
			"locally for AO_TELEMETRY_RETENTION (30 days by default).",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit < 1 || limit > 1000 {
				return usageError{fmt.Errorf("--limit must be between 1 and 1000")}
			}
			if since < 0 {
				return usageError{fmt.Errorf("--since must not be negative")}
			}
			query := url.Values{"limit": {fmt.Sprint(limit)}}
			// Filtered by the daemon before --limit applies, so an older
			// matching event is still found behind newer ones.
			if event != "" {
				query.Set("event", event)
			}
			if since > 0 {
				query.Set("since", time.Now().Add(-since).UTC().Format(time.RFC3339))
			}
			var resp struct {
				Events []telemetryLogEvent `json:"events"`
			}
			if err := ctx.doJSONPath(cmd.Context(), http.MethodGet, "/internal/telemetry/log?"+query.Encode(), nil, &resp); err != nil {
				return err
			}
			events := resp.Events
			if asJSON {
				if events == nil {
					events = []telemetryLogEvent{}
				}
				return writeJSON(cmd.OutOrStdout(), events)
			}
			return writeTelemetryLog(cmd.OutOrStdout(), events)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output JSON")
	cmd.Flags().IntVar(&limit, "limit", 50, "Most events to show (1-1000)")
	cmd.Flags().DurationVar(&since, "since", 0, "Only events sent within this long, e.g. 24h")
	cmd.Flags().StringVar(&event, "event", "", "Only this event name")
	return cmd
}

func writeTelemetryLog(w io.Writer, events []telemetryLogEvent) error {
	if len(events) == 0 {
		_, err := fmt.Fprintln(w, "No telemetry events were sent in this period.")
		return err
	}
	for i, ev := range events {
		if i > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "%s  %s  (%s)\n", ev.OccurredAt.Local().Format("2006-01-02 15:04:05"), ev.Event, ev.Surface); err != nil {
			return err
		}
		if ev.DistinctID != "" {
			if _, err := fmt.Fprintf(w, "  distinct_id: %s\n", ev.DistinctID); err != nil {
				return err
			}
		}
		keys := make([]string, 0, len(ev.Properties))
		for k := range ev.Properties {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			value, err := json.Marshal(ev.Properties[k])
			if err != nil {
				value = []byte(fmt.Sprint(ev.Properties[k]))
			}
			if _, err := fmt.Fprintf(w, "  %s: %s\n", k, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func newTelemetrySwitchCommand(ctx *commandContext, use string, optOut bool) *cobra.Command {
	short := "Turn off Share usage analytics (desktop, daemon and paired phone)"
	if !optOut {
		short = "Turn Share usage analytics back on"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var status telemetryStatusResponse
			if err := ctx.doJSONPath(cmd.Context(), http.MethodPost, "/internal/telemetry/opt-out", map[string]bool{"optedOut": optOut}, &status); err != nil {
				return err
			}
			return writeTelemetryStatus(cmd.OutOrStdout(), status)
		},
	}
}
