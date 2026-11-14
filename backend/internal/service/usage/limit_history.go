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
	ListAgentAccounts(ctx context.Context) ([]domain.AgentAccount, error)
}

type confirmedLogins interface {
	ConfirmedLogins() map[string]domain.AgentAccountIdentityCheck
}

// LimitHistoryReader turns the limit readings AO took into the windows each login went through.
type LimitHistoryReader struct {
	store  limitReadingStore
	logins confirmedLogins
	now    func() time.Time
}

// NewLimitHistoryReader constructs a limit history reader.
func NewLimitHistoryReader(store limitReadingStore, logins confirmedLogins) *LimitHistoryReader {
	return &LimitHistoryReader{store: store, logins: logins, now: time.Now}
}

const (
	sameResetWithin = 2 * time.Minute
	finalWithin     = 6 * time.Minute
	pointsFrom      = 24 * time.Hour
)

// History reads the last days of limit windows, at most 14, which is as long as readings are kept.
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
	saved, err := r.store.ListAgentAccounts(ctx)
	if err != nil {
		return domain.UsageLimitHistory{}, err
	}
	var confirmed map[string]domain.AgentAccountIdentityCheck
	if r.logins != nil {
		confirmed = r.logins.ConfirmedLogins()
	}
	out.Logins = limitLogins(readings, heldLogins(readings, saved, confirmed), until)
	return out, nil
}

type loginKey struct {
	provider domain.AgentAccountProvider
	who      string
}

func loginOf(provider domain.AgentAccountProvider, email, accountID string) loginKey {
	who := strings.ToLower(strings.TrimSpace(email))
	if who == "" {
		who = "account:" + accountID
	}
	return loginKey{provider, who}
}

func readingLogin(reading domain.AgentLimitReading) loginKey {
	return loginOf(reading.Provider, reading.Email, reading.AccountID)
}

func heldLogins(readings []domain.AgentLimitReading, saved []domain.AgentAccount, confirmed map[string]domain.AgentAccountIdentityCheck) map[loginKey]bool {
	accounts := map[string]domain.AgentAccount{}
	for _, p := range domain.AgentAccountProviders {
		accounts[domain.DeviceAgentAccountID(p)] = domain.AgentAccount{Provider: p}
	}
	for _, account := range saved {
		accounts[account.ID] = account
	}
	held := map[loginKey]bool{}
	for id, identity := range confirmed {
		account, exists := accounts[id]
		if !exists || !identity.SignedIn {
			continue
		}
		email := identity.Email
		if email == "" {
			email = account.Email
		}
		held[loginOf(account.Provider, email, id)] = true
	}
	latest := map[string]domain.AgentLimitReading{}
	for _, reading := range readings {
		_, exists := accounts[reading.AccountID]
		_, known := confirmed[reading.AccountID]
		if last, seen := latest[reading.AccountID]; exists && !known && (!seen || reading.ReadAt.After(last.ReadAt)) {
			latest[reading.AccountID] = reading
		}
	}
	for _, reading := range latest {
		held[readingLogin(reading)] = true
	}
	return held
}

func limitLogins(readings []domain.AgentLimitReading, held map[loginKey]bool, now time.Time) []domain.UsageLimitLogin {
	byLogin := map[loginKey][]domain.AgentLimitReading{}
	for _, reading := range readings {
		if reading.ResetsAt == nil {
			continue
		}
		key := readingLogin(reading)
		if !held[key] {
			continue
		}
		byLogin[key] = append(byLogin[key], reading)
	}
	logins := make([]domain.UsageLimitLogin, 0, len(byLogin))
	for _, group := range byLogin {
		sort.SliceStable(group, func(i, j int) bool { return group[i].ReadAt.Before(group[j].ReadAt) })
		latest := group[len(group)-1]
		login := domain.UsageLimitLogin{Provider: latest.Provider, Email: latest.Email, AccountID: latest.AccountID, Windows: []domain.UsageLimitSpan{}}
		for i := len(group) - 1; i >= 0 && login.Plan == ""; i-- {
			login.Plan = group[i].Plan
		}
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

func limitSpans(readings []domain.AgentLimitReading, now time.Time) []domain.UsageLimitSpan {
	var spans []domain.UsageLimitSpan
	var hours []string
	for _, reading := range readings {
		reset := reading.ResetsAt.UTC()
		last := len(spans) - 1
		if last < 0 || reset.Sub(spans[last].ResetsAt).Abs() > sameResetWithin {
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
