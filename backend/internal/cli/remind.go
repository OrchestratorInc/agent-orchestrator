package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// These DTOs mirror the daemon's reminder API without importing controller types.
type reminderDTO struct {
	ID           string    `json:"id"`
	SessionID    string    `json:"sessionId"`
	ProjectID    string    `json:"projectId"`
	Name         string    `json:"name,omitempty"`
	Message      string    `json:"message"`
	DueAt        time.Time `json:"dueAt"`
	CreatedBy    string    `json:"createdBy"`
	Status       string    `json:"status"`
	StatusReason string    `json:"statusReason,omitempty"`
}

type reminderEnvelopeDTO struct {
	Reminder reminderDTO `json:"reminder"`
}

type reminderListDTO struct {
	Reminders []reminderDTO `json:"reminders"`
}

type reminderCreateDTO struct {
	SessionID string    `json:"sessionId"`
	Message   string    `json:"message"`
	Name      string    `json:"name,omitempty"`
	DueAt     time.Time `json:"dueAt"`
}

const remindLong = `Schedule a one-shot follow-up into this session. When it is due, AO
delivers --message back into this same conversation as a new turn, so you can
end your turn now instead of polling with sleep. A running turn is never
interrupted: the follow-up waits for it to finish.

--at takes an RFC 3339 time (2026-11-01T04:00:00Z), a delay (+30m, +2h,
+1d), or a local clock time (04:00, the next time it comes round).

Do not schedule check-ins for things that already report back to you, such
as worker reports or PR events. Space repeated checks further apart, and stop
after a few with no change.`

func newRemindCommand(ctx *commandContext) *cobra.Command {
	var at, message, name string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "remind",
		Short: "Schedule a follow-up into this session (wake me later)",
		Long:  remindLong,
		Example: `  ao remind --at +30m --message "Check the worker's PR CI"
  ao remind --at 04:00 --name "Nightly" --message "Verify ao-electron after the nightly build"
  ao remind ls
  ao remind cancel rem_123`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			sessionID, err := remindSessionID()
			if err != nil {
				return err
			}
			if strings.TrimSpace(at) == "" {
				return usageError{errors.New("usage: --at is required")}
			}
			if strings.TrimSpace(message) == "" {
				return usageError{errors.New("usage: --message is required")}
			}
			if utf8.RuneCountInString(message) > domain.MaxReminderMessageCharacters {
				return usageError{fmt.Errorf("usage: --message must be at most %d characters", domain.MaxReminderMessageCharacters)}
			}
			dueAt, err := parseRemindAt(at, ctx.deps.Now())
			if err != nil {
				return usageError{err}
			}
			response, err := ctx.createReminder(cmd.Context(), reminderCreateDTO{SessionID: sessionID, Message: message, Name: name, DueAt: dueAt})
			if err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "scheduled %s for %s. Cancel it with: ao remind cancel %s\n",
				response.Reminder.ID, response.Reminder.DueAt.UTC().Format(time.RFC3339), response.Reminder.ID)
			return err
		},
	}
	f := cmd.Flags()
	f.StringVar(&at, "at", "", "When to deliver it: RFC 3339 time, +duration (+30m, +2h, +1d), or local HH:MM")
	f.StringVar(&message, "message", "", "What to tell yourself, written for the context you are in now")
	f.StringVar(&name, "name", "", "Short label shown on the session")
	f.BoolVar(&jsonOutput, "json", false, "Print JSON")
	cmd.AddCommand(newRemindListCommand(ctx), newRemindCancelCommand(ctx))
	return cmd
}

func newRemindListCommand(ctx *commandContext) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List this session's pending follow-ups",
		Args:    usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			sessionID, err := remindSessionID()
			if err != nil {
				return err
			}
			response, err := ctx.listReminders(cmd.Context(), sessionID)
			if err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			return writeReminderTable(cmd.OutOrStdout(), response.Reminders)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func newRemindCancelCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <id>",
		Short: "Cancel one of this session's pending follow-ups",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID, err := remindSessionID()
			if err != nil {
				return err
			}
			response, err := ctx.cancelReminder(cmd.Context(), args[0], sessionID)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "cancelled %s\n", response.Reminder.ID)
			return err
		},
	}
}

func (c *commandContext) createReminder(ctx context.Context, body reminderCreateDTO) (reminderEnvelopeDTO, error) {
	var response reminderEnvelopeDTO
	err := c.postJSON(ctx, "reminders", body, &response)
	return response, err
}

func (c *commandContext) listReminders(ctx context.Context, sessionID string) (reminderListDTO, error) {
	var response reminderListDTO
	err := c.getJSON(ctx, "reminders?sessionId="+url.QueryEscape(sessionID), &response)
	return response, err
}

func (c *commandContext) cancelReminder(ctx context.Context, id, sessionID string) (reminderEnvelopeDTO, error) {
	var response reminderEnvelopeDTO
	err := c.deleteJSON(ctx, "reminders/"+url.PathEscape(id)+"?sessionId="+url.QueryEscape(sessionID), &response)
	return response, err
}

func writeReminderTable(w io.Writer, reminders []reminderDTO) error {
	if len(reminders) == 0 {
		_, err := fmt.Fprintln(w, "no pending follow-ups")
		return err
	}
	writer := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "ID\tDUE\tNAME\tMESSAGE")
	for _, r := range reminders {
		_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", r.ID, r.DueAt.UTC().Format(time.RFC3339), r.Name, reminderPreview(r.Message, 60))
	}
	return writer.Flush()
}

func remindSessionID() (string, error) {
	sessionID := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	if sessionID == "" {
		return "", usageError{errors.New("usage: AO_SESSION_ID is required; run ao remind from inside an AO session")}
	}
	return sessionID, nil
}

// parseRemindAt reads an absolute RFC 3339 time, a +delay, or a local HH:MM
// clock time (its next occurrence after now).
func parseRemindAt(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "+") {
		delay, err := parseRemindDelay(value[1:])
		if err != nil {
			return time.Time{}, fmt.Errorf("usage: --at %q: %w", value, err)
		}
		return now.Add(delay), nil
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	if clock, err := time.ParseInLocation("15:04", value, now.Location()); err == nil {
		t := time.Date(now.Year(), now.Month(), now.Day(), clock.Hour(), clock.Minute(), 0, 0, now.Location())
		if !t.After(now) {
			t = t.AddDate(0, 0, 1)
		}
		return t, nil
	}
	return time.Time{}, fmt.Errorf("usage: --at %q must be an RFC 3339 time, +duration (+30m, +2h, +1d), or HH:MM", value)
}

// parseRemindDelay is time.ParseDuration plus a whole-day "d" unit.
func parseRemindDelay(value string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(value, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return 0, errors.New("days must be a positive whole number")
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	delay, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	if delay <= 0 {
		return 0, errors.New("delay must be positive")
	}
	return delay, nil
}

func reminderPreview(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit-1]) + "…"
}
