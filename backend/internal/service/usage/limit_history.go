package usage

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

type limitReadingStore interface {
	ListAgentLimitReadings(ctx context.Context, since time.Time) ([]domain.AgentLimitReading, error)
}

// LimitHistoryReader turns the limit readings AO took into the windows each
// login went through. Everything it reports was read; nothing is inferred.
type LimitHistoryReader struct {
	store limitReadingStore
	now   func() time.Time
}

// NewLimitHistoryReader constructs a limit history reader.
func NewLimitHistoryReader(store limitReadingStore) *LimitHistoryReader {
	return &LimitHistoryReader{store: store, now: time.Now}
}

const (
	// sameResetWithin merges readings whose reset times differ by this much:
	// providers report a window's reset with a few seconds of jitter.
	sameResetWithin = 2 * time.Minute
	// finalWithin is how close to its reset a window's last reading must be for
	// its peak to count as how full it got.
	finalWithin = 6 * time.Minute
	// pointsFrom is the shortest window that carries a reading series.
	pointsFrom = 24 * time.Hour
)

// History reads the last days of limit windows, at most 14, which is as long as
// readings are kept.
func (r *LimitHistoryReader) History(ctx context.Context, days int) (domain.UsageLimitHistory, error) {
	if r == nil || r.store == nil {
		return domain.UsageLimitHistory{}, fmt.Errorf("limit reading store is unavailable")
	}
	if days < 1 || days > 14 {
		return domain.UsageLimitHistory{}, apierr.Invalid("INVALID_USAGE_WINDOW", "days must be between 1 and 14", nil)
	}
	until := r.now().UTC()
	out := domain.UsageLimitHistory{Since: until.AddDate(0, 0, -days), Until: until, Logins: []domain.UsageLimitLogin{}}
	readings, err := r.store.ListAgentLimitReadings(ctx, out.Since)
	if err != nil {
		return domain.UsageLimitHistory{}, err
	}
	for _, reading := range readings {
		if out.FirstReadingAt == nil || reading.ReadAt.Before(*out.FirstReadingAt) {
			at := reading.ReadAt
			out.FirstReadingAt = &at
		}
	}
	out.Logins = limitLogins(readings, until)
	return out, nil
}

// limitLogins groups readings by login, then by window and reset time.
func limitLogins(readings []domain.AgentLimitReading, now time.Time) []domain.UsageLimitLogin {
	type loginKey struct {
		provider domain.AgentAccountProvider
		who      string
	}
	byLogin := map[loginKey][]domain.AgentLimitReading{}
	for _, reading := range readings {
		// A reading with no reset is a window with nothing used yet: no window is open.
		if reading.ResetsAt == nil {
			continue
		}
		who := strings.ToLower(strings.TrimSpace(reading.Email))
		if who == "" {
			who = "account:" + reading.AccountID
		}
		key := loginKey{reading.Provider, who}
		byLogin[key] = append(byLogin[key], reading)
	}
	logins := make([]domain.UsageLimitLogin, 0, len(byLogin))
	for _, group := range byLogin {
		sort.SliceStable(group, func(i, j int) bool { return group[i].ReadAt.Before(group[j].ReadAt) })
		latest := group[len(group)-1]
		login := domain.UsageLimitLogin{Provider: latest.Provider, Email: latest.Email, AccountID: latest.AccountID, Windows: []domain.UsageLimitSpan{}}
		byWindow := map[string][]domain.AgentLimitReading{}
		var order []string
		for _, reading := range group {
			if _, seen := byWindow[reading.WindowID]; !seen {
				order = append(order, reading.WindowID)
			}
			byWindow[reading.WindowID] = append(byWindow[reading.WindowID], reading)
		}
		for _, id := range order {
			login.Windows = append(login.Windows, limitSpans(byWindow[id], now)...)
		}
		sort.SliceStable(login.Windows, func(i, j int) bool { return login.Windows[i].ResetsAt.Before(login.Windows[j].ResetsAt) })
		logins = append(logins, login)
	}
	sort.Slice(logins, func(i, j int) bool {
		if logins[i].Provider != logins[j].Provider {
			return logins[i].Provider < logins[j].Provider
		}
		return logins[i].Email < logins[j].Email
	})
	return logins
}

// limitSpans splits one window's readings, oldest first, at each new reset.
func limitSpans(readings []domain.AgentLimitReading, now time.Time) []domain.UsageLimitSpan {
	var spans []domain.UsageLimitSpan
	var hours []string // the hour key of each span's last point
	for _, reading := range readings {
		reset := reading.ResetsAt.UTC()
		last := len(spans) - 1
		if last < 0 || absDuration(reset.Sub(spans[last].ResetsAt)) > sameResetWithin {
			span := domain.UsageLimitSpan{
				WindowID: reading.WindowID, Label: reading.Label, DurationMinutes: reading.DurationMinutes,
				ResetsAt: reset, FirstReadAt: reading.ReadAt,
			}
			spans = append(spans, span)
			hours = append(hours, "")
			last++
		}
		span := &spans[last]
		span.Readings++
		span.LastReadAt = reading.ReadAt
		span.PeakUsedPercent = max(span.PeakUsedPercent, reading.UsedPercent)
		if reading.Label != "" {
			span.Label = reading.Label
		}
		if reading.DurationMinutes > 0 {
			span.DurationMinutes = reading.DurationMinutes
		}
		if time.Duration(span.DurationMinutes)*time.Minute >= pointsFrom {
			point := domain.UsageLimitPoint{At: reading.ReadAt, UsedPercent: reading.UsedPercent}
			hour := reading.ReadAt.UTC().Format("2006-01-02T15")
			if hours[last] == hour {
				span.Points[len(span.Points)-1] = point
			} else {
				span.Points = append(span.Points, point)
				hours[last] = hour
			}
		}
	}
	for i := range spans {
		span := &spans[i]
		if span.DurationMinutes > 0 {
			start := span.ResetsAt.Add(-time.Duration(span.DurationMinutes) * time.Minute)
			span.StartsAt = &start
		}
		// A provider can start a new window before the old one's reset (Codex
		// does when a week restarts early); the old one ended when the next began.
		if i+1 < len(spans) && spans[i+1].FirstReadAt.Before(span.ResetsAt) {
			if spans[i+1].FirstReadAt.Sub(span.LastReadAt) <= finalWithin {
				span.State = domain.UsageLimitSpanFinal
			} else {
				span.State = domain.UsageLimitSpanPartial
			}
			continue
		}
		switch {
		case span.ResetsAt.After(now):
			span.State = domain.UsageLimitSpanOpen
		case span.ResetsAt.Sub(span.LastReadAt) <= finalWithin:
			span.State = domain.UsageLimitSpanFinal
		default:
			span.State = domain.UsageLimitSpanPartial
		}
	}
	return spans
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
