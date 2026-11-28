package usage

import (
	"context"
	"crypto/sha256"
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
	out.Logins = limitLogins(readings, heldLogins(readings, saved, confirmed), confirmed, until)
	return out, nil
}

// loginKey is one login: two logins can share an email address and differ
// only in the organization they belong to (#8891).
type loginKey struct {
	provider domain.AgentAccountProvider
	who      string
	org      string
	// A Claude reading without an org ID belongs to its recording account.
	account string
}

func loginOf(provider domain.AgentAccountProvider, email, org, accountID string) loginKey {
	who := strings.ToLower(strings.TrimSpace(email))
	if who == "" {
		who = "account:" + accountID
	}
	return loginKey{provider: provider, who: who, org: strings.TrimSpace(org)}
}

func readingLogin(reading domain.AgentLimitReading) loginKey {
	key := loginOf(reading.Provider, reading.Email, reading.OrgID, reading.AccountID)
	if key.provider == domain.AgentAccountProviderClaude && key.org == "" {
		key.account = reading.AccountID
	}
	return key
}

func (key loginKey) id() string {
	identity := fmt.Sprintf("%q %q %q %q", key.provider, key.who, key.org, key.account)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(identity)))
}

// heldBy is each held login with the accounts that hold it now.
type heldBy struct {
	byLogin   map[loginKey]map[string]bool
	byAccount map[string]loginKey
}

func (h heldBy) add(key loginKey, account string) {
	if h.byLogin[key] == nil {
		h.byLogin[key] = map[string]bool{}
	}
	h.byLogin[key][account] = true
	h.byAccount[account] = key
}

// An organization is held by a matching login. An unattributed Claude group
// stays visible only while its own account still holds the same email.
func (h heldBy) holders(key loginKey) map[string]bool {
	if key.account == "" {
		return h.byLogin[key]
	}
	held, ok := h.byAccount[key.account]
	if ok && held.provider == key.provider && held.who == key.who {
		return map[string]bool{key.account: true}
	}
	return nil
}

func heldLogins(readings []domain.AgentLimitReading, saved []domain.AgentAccount, confirmed map[string]domain.AgentAccountIdentityCheck) heldBy {
	accounts := map[string]domain.AgentAccount{}
	for _, p := range domain.AgentAccountProviders {
		accounts[domain.DeviceAgentAccountID(p)] = domain.AgentAccount{Provider: p}
	}
	for _, account := range saved {
		accounts[account.ID] = account
	}
	held := heldBy{byLogin: map[loginKey]map[string]bool{}, byAccount: map[string]loginKey{}}
	for id, identity := range confirmed {
		account, exists := accounts[id]
		if !exists || !identity.SignedIn {
			continue
		}
		email := identity.Email
		if email == "" {
			email = account.Email
		}
		held.add(loginOf(account.Provider, email, identity.OrgID, id), id)
	}
	latest := map[string]domain.AgentLimitReading{}
	for _, reading := range readings {
		_, exists := accounts[reading.AccountID]
		_, known := confirmed[reading.AccountID]
		if last, seen := latest[reading.AccountID]; exists && !known && (!seen || reading.ReadAt.After(last.ReadAt)) {
			latest[reading.AccountID] = reading
		}
	}
	for id, reading := range latest {
		held.add(loginOf(reading.Provider, reading.Email, reading.OrgID, id), id)
	}
	return held
}

func limitLogins(readings []domain.AgentLimitReading, held heldBy, confirmed map[string]domain.AgentAccountIdentityCheck, now time.Time) []domain.UsageLimitLogin {
	byLogin := map[loginKey][]domain.AgentLimitReading{}
	for _, reading := range readings {
		if reading.ResetsAt == nil {
			continue
		}
		key := readingLogin(reading)
		byLogin[key] = append(byLogin[key], reading)
	}
	logins := make([]domain.UsageLimitLogin, 0, len(byLogin))
	for key, group := range byLogin {
		holders := held.holders(key)
		if len(holders) == 0 {
			continue
		}
		sort.SliceStable(group, func(i, j int) bool { return group[i].ReadAt.Before(group[j].ReadAt) })
		// The login is shown as the account that read it last among those that
		// hold it now: an account that read it and has since moved to another
		// organization no longer stands for it. When no holder has read it, the
		// first holder by account ID stands for it.
		latest := group[len(group)-1]
		account := ""
		for i := len(group) - 1; i >= 0; i-- {
			if holders[group[i].AccountID] {
				latest, account = group[i], group[i].AccountID
				break
			}
		}
		if account == "" {
			for id := range holders {
				if account == "" || id < account {
					account = id
				}
			}
		}
		login := domain.UsageLimitLogin{
			LoginID: key.id(), OrganizationUnknown: key.account != "", Provider: latest.Provider,
			Email: latest.Email, AccountID: account, Windows: []domain.UsageLimitSpan{},
		}
		// The organization is named by the account standing for the login, when
		// that account is confirmed in this very organization.
		if identity, ok := confirmed[account]; ok && key.org != "" && strings.TrimSpace(identity.OrgID) == key.org {
			login.OrganizationName = strings.TrimSpace(identity.OrgName)
		}
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
		if logins[i].Email != logins[j].Email {
			return logins[i].Email < logins[j].Email
		}
		if logins[i].AccountID != logins[j].AccountID {
			return logins[i].AccountID < logins[j].AccountID
		}
		return logins[i].LoginID < logins[j].LoginID
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
