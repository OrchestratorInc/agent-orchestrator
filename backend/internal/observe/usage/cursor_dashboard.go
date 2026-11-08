package usage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// Cursor keeps no token counts on disk.
const (
	CursorDashboardURL    = "https://cursor.com/api/dashboard/get-filtered-usage-events"
	cursorPricingVersion  = "cursor:reported"
	cursorBillingProvider = "cursor"
	cursorPageSize        = 1000
	cursorPageTimeout     = 10 * time.Second
	cursorKeychainTimeout = 30 * time.Second
	maxCursorPageBytes    = 64 << 20
	maxCursorCount        = 1e15
)

var (
	// ErrCursorSignedOut means there is no usable Cursor login: no Keychain item, or cursor.com refused the token.
	ErrCursorSignedOut = errors.New("cursor is not signed in")
	// ErrCursorKeychain means the Keychain item could not be read, most often because macOS access was denied or not answered.
	ErrCursorKeychain = errors.New("cursor keychain item could not be read")
)

// CursorKeychainToken reads the access token cursor-agent's login keeps in the macOS Keychain. macOS may ask the user to allow the read; the timeout leaves time to answer and fails the read instead of hanging.
func CursorKeychainToken(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, cursorKeychainTimeout)
	defer cancel()
	out, err := process.CommandContext(ctx, "security", "find-generic-password",
		"-s", "cursor-access-token", "-a", "cursor-user", "-w").Output()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case ctx.Err() == nil && errors.As(err, &exitErr) && exitErr.ExitCode() == 44:
		return "", ErrCursorSignedOut
	default:
		return "", errors.Join(ErrCursorKeychain, err)
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", ErrCursorSignedOut
	}
	return token, nil
}

// CursorAccountKey names the account a token signs in to, without the token.
func CursorAccountKey(token string) (string, error) {
	_, key, err := cursorAccount(token)
	return key, err
}

func cursorAccount(token string) (userID, accountKey string, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", "", ErrCursorSignedOut
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return "", "", ErrCursorSignedOut
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Sub == "" {
		return "", "", ErrCursorSignedOut
	}
	userID = claims.Sub[strings.LastIndex(claims.Sub, "|")+1:]
	if userID == "" {
		return "", "", ErrCursorSignedOut
	}
	sum := sha256.Sum256([]byte(claims.Sub))
	return userID, hex.EncodeToString(sum[:]), nil
}

// CursorDashboardEvent is one billed Cursor request and the conversation it was for.
type CursorDashboardEvent struct {
	ConversationID string
	Event          domain.ModelUsageEvent
}

// ReadCursorDashboard returns the account's billed requests from since to until, inclusive to the millisecond.
func ReadCursorDashboard(ctx context.Context, client *http.Client, endpoint, token string, since, until time.Time) ([]CursorDashboardEvent, error) {
	userID, accountKey, err := cursorAccount(token)
	if err != nil {
		return nil, err
	}
	sinceMs, untilMs := since.UnixMilli(), until.UnixMilli()
	if sinceMs < 0 || sinceMs > untilMs {
		return nil, fmt.Errorf("invalid cursor usage window %v to %v", since, until)
	}
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("cursor usage request was redirected")
	}
	cookie := "WorkosCursorSessionToken=" + url.QueryEscape(userID+"::"+token)

	var pages [][]json.RawMessage
	total := -1
	for page := 1; ; page++ {
		limit := 1000
		if total >= 0 {
			limit = (total+cursorPageSize-1)/cursorPageSize*2 + 1
		}
		if page > limit {
			return nil, errors.New("cursor usage page limit exceeded")
		}
		events, count, err := readCursorPage(ctx, &noRedirect, endpoint, cookie, page, sinceMs, untilMs)
		if err != nil {
			return nil, err
		}
		if count >= 0 {
			if total >= 0 && count != total {
				return nil, errors.New("cursor usage count changed between pages")
			}
			total = count
		}
		pages = append(pages, events)
		if len(events) < cursorPageSize {
			break
		}
	}
	read := 0
	for _, events := range pages {
		read += len(events)
	}
	removals := 0
	if total >= 0 {
		if read < total {
			return nil, errors.New("cursor usage pages are incomplete")
		}
		removals = read - total
	}

	var out []CursorDashboardEvent
	occurrences := map[string]int{}
	var previous []string
	for _, events := range pages {
		var keys []string
		if removals > 0 {
			keys = make([]string, len(events))
			for index, raw := range events {
				if keys[index], err = canonicalJSONHash(raw); err != nil {
					return nil, errors.New("invalid cursor usage event")
				}
			}
		}
		drop := min(removals, cursorBoundaryOverlap(previous, keys))
		removals -= drop
		previous = keys
		for _, raw := range events[drop:] {
			event, ok, err := cursorUsageEvent(raw, accountKey, sinceMs, untilMs, occurrences)
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, event)
			}
		}
	}
	if removals != 0 {
		return nil, errors.New("cursor usage page boundaries are inconsistent")
	}
	return out, nil
}

func readCursorPage(ctx context.Context, client *http.Client, endpoint, cookie string, page int, sinceMs, untilMs int64) ([]json.RawMessage, int, error) {
	ctx, cancel := context.WithTimeout(ctx, cursorPageTimeout)
	defer cancel()
	body, err := json.Marshal(map[string]any{
		"page": page, "pageSize": cursorPageSize,
		"startDate": strconv.FormatInt(sinceMs, 10), "endDate": strconv.FormatInt(untilMs, 10),
	})
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://cursor.com")
	req.Header.Set("Cookie", cookie)
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("cursor usage request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, 0, ErrCursorSignedOut
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, 0, fmt.Errorf("cursor usage request: HTTP %d", resp.StatusCode)
	}
	var object map[string]json.RawMessage
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxCursorPageBytes)).Decode(&object); err != nil || object == nil {
		return nil, 0, errors.New("invalid cursor usage page")
	}
	for _, key := range []string{"error", "message", "code"} {
		if _, ok := object[key]; ok {
			return nil, 0, errors.New("cursor usage request returned an error")
		}
	}
	if len(object) == 0 {
		return nil, 0, nil
	}
	count := -1
	countRaw, hasCount := object["totalUsageEventsCount"]
	if hasCount {
		if err := json.Unmarshal(countRaw, &count); err != nil || count < 0 {
			return nil, 0, errors.New("invalid cursor usage count")
		}
	}
	if hasCount && len(object) == 1 {
		return nil, count, nil
	}
	var events []json.RawMessage
	eventsRaw, ok := object["usageEventsDisplay"]
	if !ok || json.Unmarshal(eventsRaw, &events) != nil || len(events) > cursorPageSize {
		return nil, 0, errors.New("invalid cursor usage page")
	}
	return events, count, nil
}

type cursorNativeEvent struct {
	Timestamp      json.RawMessage `json:"timestamp"`
	Model          string          `json:"model"`
	ConversationID string          `json:"conversationId"`
	TokenUsage     json.RawMessage `json:"tokenUsage"`
}

type cursorNativeUsage struct {
	InputTokens      *float64 `json:"inputTokens"`
	OutputTokens     *float64 `json:"outputTokens"`
	CacheReadTokens  *float64 `json:"cacheReadTokens"`
	CacheWriteTokens *float64 `json:"cacheWriteTokens"`
	TotalCents       *float64 `json:"totalCents"`
}

func cursorUsageEvent(raw json.RawMessage, accountKey string, sinceMs, untilMs int64, occurrences map[string]int) (CursorDashboardEvent, bool, error) {
	invalid := errors.New("invalid cursor usage event")
	var native cursorNativeEvent
	if err := json.Unmarshal(raw, &native); err != nil {
		return CursorDashboardEvent{}, false, invalid
	}
	if !jsonValueReported(native.TokenUsage) {
		return CursorDashboardEvent{}, false, nil
	}
	var usage cursorNativeUsage
	if err := json.Unmarshal(native.TokenUsage, &usage); err != nil {
		return CursorDashboardEvent{}, false, invalid
	}
	for _, value := range []*float64{usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheWriteTokens, usage.TotalCents} {
		if value != nil && (*value < 0 || *value > maxCursorCount) {
			return CursorDashboardEvent{}, false, invalid
		}
	}
	whenMs, err := strconv.ParseFloat(strings.Trim(strings.TrimSpace(string(native.Timestamp)), `"`), 64)
	model := strings.TrimSpace(native.Model)
	if err != nil || math.IsNaN(whenMs) || math.IsInf(whenMs, 0) || model == "" || len(model) > 256 {
		return CursorDashboardEvent{}, false, invalid
	}
	at := int64(whenMs)
	if at < sinceMs || at > untilMs {
		return CursorDashboardEvent{}, false, nil
	}

	input, cacheRead, cacheWrite, output := cursorTokens(usage.InputTokens), cursorTokens(usage.CacheReadTokens),
		cursorTokens(usage.CacheWriteTokens), cursorTokens(usage.OutputTokens)
	providerID := domain.UsageProviderOpenAI
	if strings.Contains(strings.ToLower(model), "claude") {
		providerID = domain.UsageProviderAnthropic
	}
	tokens, ok := normalizeRoutedUsage(providerID, input, cacheRead, cacheWrite, output)
	if !ok {
		return CursorDashboardEvent{}, false, invalid
	}
	var costs domain.UsageEventCosts
	cents := ""
	if usage.TotalCents != nil {
		cents = strconv.FormatFloat(*usage.TotalCents, 'g', -1, 64)
		if total, ok := usdToNanos(*usage.TotalCents / 100); ok {
			costs = domain.UsageEventCosts{EstimatedCostNanos: &total, PricingVersion: cursorPricingVersion}
		}
	}
	conversation := strings.TrimSpace(native.ConversationID)
	identity := stableSourceEventKey("cursor", accountKey, strconv.FormatInt(at, 10), model, conversation,
		strconv.FormatInt(input, 10), strconv.FormatInt(cacheRead, 10), strconv.FormatInt(cacheWrite, 10),
		strconv.FormatInt(output, 10), cents)
	occurrence := occurrences[identity]
	occurrences[identity] = occurrence + 1
	return CursorDashboardEvent{
		ConversationID: conversation,
		Event: domain.ModelUsageEvent{
			ProviderID:            providerID,
			BillingProviderID:     cursorBillingProvider,
			BillingProviderSource: domain.UsageBillingProviderObserved,
			ModelID:               model,
			MeasurementKind:       domain.UsageMeasurementNativeReported,
			Tokens:                tokens,
			ProviderUsageJSON:     providerUsageWithCacheWrite(native.TokenUsage, &cacheWrite, nil),
			CreatedAt:             time.UnixMilli(at).UTC(),
			SourceEventKey:        identity + ":" + strconv.Itoa(occurrence),
			Costs:                 costs,
		},
	}, true, nil
}

func cursorTokens(value *float64) int64 {
	if value == nil || *value <= 0 {
		return 0
	}
	return int64(*value)
}

func canonicalJSONHash(raw json.RawMessage) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func cursorBoundaryOverlap(previous, current []string) int {
	sequence := make([]string, 0, len(current)+1+len(previous))
	sequence = append(append(append(sequence, current...), ""), previous...)
	lengths := make([]int, len(sequence))
	for index := 1; index < len(sequence); index++ {
		length := lengths[index-1]
		for length > 0 && sequence[index] != sequence[length] {
			length = lengths[length-1]
		}
		if sequence[index] == sequence[length] {
			length++
		}
		lengths[index] = length
	}
	return lengths[len(sequence)-1]
}
