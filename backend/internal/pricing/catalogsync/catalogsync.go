// Package catalogsync builds AO's content-addressed pricing catalog from LiteLLM's model price table.
package catalogsync

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/pricing"
)

const (
	schemaVersion       = 1
	maxCanonicalRateLen = 128
)

type manifest struct {
	SchemaVersion int            `json:"schemaVersion"`
	Source        manifestSource `json:"source"`
	Providers     []providerRef  `json:"providers"`
}

type manifestSource struct {
	Repository string `json:"repository"`
	Revision   string `json:"revision"`
	Path       string `json:"path"`
}

type providerRef struct {
	ProviderID string `json:"providerId"`
	Version    string `json:"version"`
	SHA256     string `json:"sha256"`
	Path       string `json:"path"`
	ModelCount int    `json:"modelCount"`
}

type providerBlob struct {
	SchemaVersion int           `json:"schemaVersion"`
	ProviderID    string        `json:"providerId"`
	Models        []pricedModel `json:"models"`
}

type pricedModel struct {
	ModelID string `json:"modelId"`
	Rates   rates  `json:"rates"`
}

type rates struct {
	UncachedInputUSDPerToken string  `json:"uncachedInputUsdPerToken"`
	CacheReadUSDPerToken     *string `json:"cacheReadUsdPerToken,omitempty"`
	CacheWriteUSDPerToken    *string `json:"cacheWriteUsdPerToken,omitempty"`
	CacheWrite1HUSDPerToken  *string `json:"cacheWrite1hUsdPerToken,omitempty"`
	OutputUSDPerToken        string  `json:"outputUsdPerToken"`
}

type upstreamRates struct {
	Input   *json.Number
	Output  *json.Number
	Read    *json.Number
	Write   *json.Number
	Write1H *json.Number
}

type upstreamRecord struct {
	Provider string `json:"litellm_provider"`
	Mode     string `json:"mode"`
	upstreamRates
	Tiers        map[string]upstreamRates
	FastMultiple *json.Number
	LongContext  map[int64]longContextRates
}

type longContextRates struct {
	upstreamRates
	Tiers map[string]upstreamRates
}

var longContextInputField = regexp.MustCompile(`^input_cost_per_token_above_(\d+)k_tokens$`)

var suffixTiers = []string{pricing.ServiceTierPriority, pricing.ServiceTierFlex, pricing.ServiceTierUltrafast}

func (u *upstreamRecord) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&fields); err != nil {
		return err
	}
	if err := decodeString(fields["litellm_provider"], &u.Provider); err != nil {
		return fmt.Errorf("litellm_provider: %w", err)
	}
	if err := decodeString(fields["mode"], &u.Mode); err != nil {
		return fmt.Errorf("mode: %w", err)
	}
	var err error
	if u.upstreamRates, err = decodeRates(fields, ""); err != nil {
		return err
	}
	if u.Tiers, err = decodeTiers(fields, ""); err != nil {
		return err
	}
	for name := range fields {
		match := longContextInputField.FindStringSubmatch(name)
		if match == nil {
			continue
		}
		aboveK, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil || aboveK <= 0 {
			return fmt.Errorf("%s: size out of range", name)
		}
		suffix := "_above_" + match[1] + "k_tokens"
		long := longContextRates{}
		if long.upstreamRates, err = decodeRates(fields, suffix); err != nil {
			return err
		}
		if long.Tiers, err = decodeTiers(fields, suffix); err != nil {
			return err
		}
		if u.LongContext == nil {
			u.LongContext = make(map[int64]longContextRates)
		}
		u.LongContext[aboveK] = long
	}
	var entry map[string]json.RawMessage
	if raw := fields["provider_specific_entry"]; len(raw) > 0 && json.Unmarshal(raw, &entry) == nil {
		if u.FastMultiple, err = decodeNumber(entry["fast"]); err != nil {
			return fmt.Errorf("provider_specific_entry.fast: %w", err)
		}
	}
	return nil
}

func decodeTiers(fields map[string]json.RawMessage, suffix string) (map[string]upstreamRates, error) {
	var tiers map[string]upstreamRates
	for _, tier := range suffixTiers {
		rates, err := decodeRates(fields, suffix+"_"+tier)
		if err != nil {
			return nil, err
		}
		if rates != (upstreamRates{}) {
			if tiers == nil {
				tiers = make(map[string]upstreamRates, len(suffixTiers))
			}
			tiers[tier] = rates
		}
	}
	return tiers, nil
}

func decodeRates(fields map[string]json.RawMessage, suffix string) (upstreamRates, error) {
	var r upstreamRates
	for _, field := range []struct {
		name   string
		target **json.Number
	}{
		{"input_cost_per_token", &r.Input},
		{"output_cost_per_token", &r.Output},
		{"cache_read_input_token_cost", &r.Read},
		{"cache_creation_input_token_cost", &r.Write},
		{"cache_creation_input_token_cost_above_1hr", &r.Write1H},
	} {
		name := field.name + suffix
		number, err := decodeNumber(fields[name])
		if err != nil {
			return upstreamRates{}, fmt.Errorf("%s: %w", name, err)
		}
		*field.target = number
	}
	return r, nil
}

func decodeString(raw json.RawMessage, target *string) error {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	return json.Unmarshal(raw, target)
}

func decodeNumber(raw json.RawMessage) (*json.Number, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var number json.Number
	if err := decoder.Decode(&number); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("must be one JSON number")
	}
	return &number, nil
}

type generatedCatalog struct {
	manifest manifest
	blobs    map[string][]byte
}

func build(upstream []byte) (generatedCatalog, error) {
	decoder := json.NewDecoder(bytes.NewReader(upstream))
	var records map[string]json.RawMessage
	if err := decoder.Decode(&records); err != nil {
		return generatedCatalog{}, fmt.Errorf("decode upstream catalog: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return generatedCatalog{}, errors.New("upstream catalog must contain one JSON object")
	}
	modelsByProvider := map[string]map[string]rates{}
	conflicted := map[string]map[string]bool{}
	for name, data := range records {
		var record upstreamRecord
		if json.Unmarshal(data, &record) != nil {
			continue
		}
		providerID, ok := canonicalProvider(record.Provider)
		if !ok || !supportedMode(record.Mode) {
			continue
		}
		// LiteLLM also carries metadata-only model entries in chat/responses
		// modes. They are not price records.
		if record.Input == nil || record.Output == nil {
			continue
		}
		modelID, err := canonicalModel(providerID, name)
		if err != nil {
			continue
		}
		r, err := normalizeRates(record.upstreamRates)
		if err != nil {
			continue
		}
		rows, err := serviceTierRows(modelID, record, r)
		if err != nil {
			continue
		}
		rows[modelID] = r
		if err := addLongContextRows(rows, modelID, record); err != nil {
			continue
		}
		if modelsByProvider[providerID] == nil {
			modelsByProvider[providerID] = map[string]rates{}
			conflicted[providerID] = map[string]bool{}
		}
		for rowID, rowRates := range rows {
			if previous, exists := modelsByProvider[providerID][rowID]; exists && !equalRates(previous, rowRates) {
				conflicted[providerID][rowID] = true
			}
			modelsByProvider[providerID][rowID] = rowRates
		}
	}
	for providerID, rows := range conflicted {
		for rowID := range rows {
			delete(modelsByProvider[providerID], rowID)
		}
	}

	for _, providerID := range pricing.RequiredProviderIDs {
		if len(modelsByProvider[providerID]) == 0 {
			return generatedCatalog{}, fmt.Errorf("provider %q produced zero supported models", providerID)
		}
	}
	providers := make([]string, 0, len(modelsByProvider))
	for providerID, models := range modelsByProvider {
		if len(models) > 0 {
			providers = append(providers, providerID)
		}
	}
	sort.Strings(providers)
	generated := generatedCatalog{blobs: make(map[string][]byte)}
	generated.manifest = manifest{SchemaVersion: schemaVersion, Source: manifestSource{
		Repository: "BerriAI/litellm",
		Revision:   gitBlobID(upstream),
		Path:       "model_prices_and_context_window.json",
	}, Providers: make([]providerRef, 0, len(providers))}
	for _, providerID := range providers {
		models := modelsByProvider[providerID]
		ids := make([]string, 0, len(models))
		for modelID := range models {
			ids = append(ids, modelID)
		}
		sort.Strings(ids)
		blob := providerBlob{SchemaVersion: schemaVersion, ProviderID: providerID, Models: make([]pricedModel, 0, len(ids))}
		for _, modelID := range ids {
			blob.Models = append(blob.Models, pricedModel{
				ModelID: modelID,
				Rates:   withPlausibleCacheTiers(providerID, models[modelID]),
			})
		}
		contents, err := canonicalJSON(blob)
		if err != nil {
			return generatedCatalog{}, err
		}
		digest := hash(contents)
		generated.blobs[providerID] = contents
		generated.manifest.Providers = append(generated.manifest.Providers, providerRef{
			ProviderID: providerID,
			Version:    "ao-catalog:" + providerID + ":sha256:" + digest,
			SHA256:     digest,
			Path:       "providers/" + providerID + "/" + digest + ".json",
			ModelCount: len(blob.Models),
		})
	}
	return generated, nil
}

func serviceTierRows(modelID string, record upstreamRecord, standard rates) (map[string]rates, error) {
	rows := make(map[string]rates)
	for tier, upstream := range record.Tiers {
		if upstream.Input == nil || upstream.Output == nil {
			continue
		}
		r, err := normalizeRates(upstream)
		if err != nil {
			return nil, fmt.Errorf("%s tier: %w", tier, err)
		}
		if !coversCacheRates(r, standard) {
			continue
		}
		rows[pricing.TierModelID(modelID, tier)] = r
	}
	if record.FastMultiple != nil {
		multiple, err := normalizeDecimal(record.FastMultiple.String())
		if err != nil {
			return nil, fmt.Errorf("fast multiple: %w", err)
		}
		if multiple != "0" {
			r, err := scaleRates(standard, multiple)
			if err != nil {
				return nil, fmt.Errorf("fast tier: %w", err)
			}
			rows[pricing.TierModelID(modelID, pricing.ServiceTierFast)] = r
		}
	}
	return rows, nil
}

func coversCacheRates(r, base rates) bool {
	return (base.CacheReadUSDPerToken == nil || r.CacheReadUSDPerToken != nil) &&
		(base.CacheWriteUSDPerToken == nil || r.CacheWriteUSDPerToken != nil) &&
		(base.CacheWrite1HUSDPerToken == nil || r.CacheWrite1HUSDPerToken != nil)
}

func addLongContextRows(rows map[string]rates, modelID string, record upstreamRecord) error {
	for aboveK, long := range record.LongContext {
		extend := func(rowID string, upstream upstreamRates) error {
			base, ok := rows[rowID]
			if !ok || upstream.Input == nil || upstream.Output == nil {
				return nil
			}
			r, err := normalizeRates(upstream)
			if err != nil {
				return fmt.Errorf("above %dk: %w", aboveK, err)
			}
			if !coversCacheRates(r, base) {
				return nil
			}
			rows[pricing.LongContextModelID(rowID, aboveK)] = r
			return nil
		}
		if err := extend(modelID, long.upstreamRates); err != nil {
			return err
		}
		for tier, upstream := range long.Tiers {
			if err := extend(pricing.TierModelID(modelID, tier), upstream); err != nil {
				return err
			}
		}
	}
	return nil
}

func scaleRates(r rates, multiple string) (rates, error) {
	var scaled rates
	var err error
	if scaled.UncachedInputUSDPerToken, err = multiplyDecimals(r.UncachedInputUSDPerToken, multiple); err != nil {
		return rates{}, err
	}
	if scaled.OutputUSDPerToken, err = multiplyDecimals(r.OutputUSDPerToken, multiple); err != nil {
		return rates{}, err
	}
	for _, field := range []struct {
		from *string
		to   **string
	}{
		{r.CacheReadUSDPerToken, &scaled.CacheReadUSDPerToken},
		{r.CacheWriteUSDPerToken, &scaled.CacheWriteUSDPerToken},
		{r.CacheWrite1HUSDPerToken, &scaled.CacheWrite1HUSDPerToken},
	} {
		if field.from == nil {
			continue
		}
		value, err := multiplyDecimals(*field.from, multiple)
		if err != nil {
			return rates{}, err
		}
		*field.to = &value
	}
	return scaled, nil
}

func multiplyDecimals(left, right string) (string, error) {
	x, okX := new(big.Rat).SetString(left)
	y, okY := new(big.Rat).SetString(right)
	if !okX || !okY {
		return "", errors.New("rate is not a decimal")
	}
	return normalizeDecimal(new(big.Rat).Mul(x, y).FloatString(fractionDigits(left) + fractionDigits(right)))
}

func fractionDigits(decimal string) int {
	if point := strings.IndexByte(decimal, '.'); point >= 0 {
		return len(decimal) - point - 1
	}
	return 0
}

func normalizeRates(record upstreamRates) (rates, error) {
	input, err := normalizeRequired(record.Input)
	if err != nil {
		return rates{}, fmt.Errorf("input: %w", err)
	}
	output, err := normalizeRequired(record.Output)
	if err != nil {
		return rates{}, fmt.Errorf("output: %w", err)
	}
	read, err := normalizeOptional(record.Read)
	if err != nil {
		return rates{}, fmt.Errorf("cache read: %w", err)
	}
	write, err := normalizeOptional(record.Write)
	if err != nil {
		return rates{}, fmt.Errorf("cache write: %w", err)
	}
	write1H, err := normalizeOptional(record.Write1H)
	if err != nil {
		return rates{}, fmt.Errorf("1h cache write: %w", err)
	}
	return rates{input, read, write, write1H, output}, nil
}

func normalizeRequired(number *json.Number) (string, error) {
	if number == nil {
		return "", errors.New("rate is required")
	}
	return normalizeDecimal(number.String())
}

func normalizeOptional(number *json.Number) (*string, error) {
	if number == nil {
		return nil, nil
	}
	value, err := normalizeDecimal(number.String())
	if err != nil {
		return nil, err
	}
	return &value, nil
}

// normalizeDecimal parses a JSON decimal or scientific notation without a
// float64 round trip and returns its canonical non-exponent decimal spelling.
func normalizeDecimal(input string) (string, error) {
	if input == "" {
		return "", errors.New("rate is not a number")
	}
	if strings.HasPrefix(input, "-") {
		return "", errors.New("rate must be nonnegative")
	}
	if strings.HasPrefix(input, "+") {
		return "", errors.New("rate is not valid JSON number")
	}
	parts := strings.FieldsFunc(input, func(r rune) bool { return r == 'e' || r == 'E' })
	if len(parts) > 2 || len(parts) == 0 {
		return "", errors.New("rate is not a decimal")
	}
	mantissa := parts[0]
	exponent := 0
	if len(parts) == 2 {
		parsed, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return "", errors.New("rate has invalid exponent")
		}
		if parsed < -maxCanonicalRateLen || parsed > maxCanonicalRateLen {
			return "", errors.New("rate exponent is out of range")
		}
		exponent = int(parsed)
	}
	decimal := strings.Split(mantissa, ".")
	if len(decimal) > 2 || len(decimal) == 0 || (len(decimal) == 2 && decimal[1] == "") {
		return "", errors.New("rate is not a decimal")
	}
	digits := strings.Join(decimal, "")
	if digits == "" {
		return "", errors.New("rate is not a decimal")
	}
	if len(digits) > maxCanonicalRateLen {
		return "", errors.New("rate exceeds supported precision")
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return "", errors.New("rate is not a decimal")
		}
	}
	fraction := 0
	if len(decimal) == 2 {
		fraction = len(decimal[1])
	}
	shift := exponent - fraction
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		return "0", nil
	}
	if shift >= 0 {
		if shift > maxCanonicalRateLen-len(digits) {
			return "", errors.New("rate exceeds supported precision")
		}
		return digits + strings.Repeat("0", shift), nil
	}
	point := len(digits) + shift
	if point <= 0 {
		zeroes := -point
		if len(digits) > maxCanonicalRateLen-2 || zeroes > maxCanonicalRateLen-2-len(digits) {
			return "", errors.New("rate exceeds supported precision")
		}
		result := "0." + strings.Repeat("0", zeroes) + digits
		return strings.TrimRight(result, "0"), nil
	}
	if len(digits) >= maxCanonicalRateLen {
		return "", errors.New("rate exceeds supported precision")
	}
	result := digits[:point] + "." + digits[point:]
	result = strings.TrimRight(result, "0")
	return strings.TrimSuffix(result, "."), nil
}

func canonicalProvider(value string) (string, bool) {
	provider := strings.ToLower(strings.TrimSpace(value))
	if strings.HasPrefix(provider, "vertex_ai-") {
		provider = "vertex_ai"
	}
	provider = pricing.CanonicalProviderID(provider)
	if !pricing.ValidProviderID(provider) {
		return "", false
	}
	return provider, true
}

func canonicalModel(providerID, value string) (string, error) {
	modelID := strings.ToLower(strings.TrimSpace(value))
	modelID = strings.TrimPrefix(modelID, providerID+"/")
	if modelID == "" {
		return "", fmt.Errorf("%s: empty model identifier", providerID)
	}
	if pricing.CanonicalModelID(providerID, modelID) != modelID {
		return "", fmt.Errorf("%s: model identifier %q is not canonical", providerID, modelID)
	}
	return modelID, nil
}

func supportedMode(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "chat", "responses":
		return true
	default:
		return false
	}
}

func canonicalJSON(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func equalRates(left, right rates) bool {
	return left.UncachedInputUSDPerToken == right.UncachedInputUSDPerToken &&
		equalOptionalRate(left.CacheReadUSDPerToken, right.CacheReadUSDPerToken) &&
		equalOptionalRate(left.CacheWriteUSDPerToken, right.CacheWriteUSDPerToken) &&
		equalOptionalRate(left.CacheWrite1HUSDPerToken, right.CacheWrite1HUSDPerToken) &&
		left.OutputUSDPerToken == right.OutputUSDPerToken
}

func equalOptionalRate(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

// withPlausibleCacheTiers drops an Anthropic cache-write rate the upstream feed
// cannot mean, rather than shipping it.
//
// Anthropic prices a 5-minute write above base input and a 1-hour write above
// that; the feed has carried both a 1-hour rate below the 5-minute rate
// (claude-3-opus-20240229, 5x too low) and one twenty-four times base input
// (claude-3-haiku-20240307, 12x too high) — in both cases the value belonging
// to a different model. An absent rate prices that bucket as unknown, which the
// estimator already reports honestly; a wrong one is billed as fact.
//
// This is deliberately not applied to the other providers, whose tiers do not
// follow that shape: z.ai writes to cache for free, and an OpenAI fine-tune
// prices a write at half its input rate. Both are real, and a relational
// invariant borrowed from Anthropic would delete them.
func withPlausibleCacheTiers(providerID string, r rates) rates {
	if providerID != "anthropic" {
		return r
	}
	if err := checkAnthropicCacheTiers(r); err == nil {
		return r
	}
	trimmed := r
	trimmed.CacheWrite1HUSDPerToken = nil
	if checkAnthropicCacheTiers(trimmed) == nil {
		return trimmed
	}
	trimmed.CacheWriteUSDPerToken = nil
	return trimmed
}

// maxAnthropicWriteMultiple bounds a 1-hour write against base input. Anthropic
// documents 2x; the headroom leaves room for a repricing without letting a
// value from another model's row through.
var maxAnthropicWriteMultiple = big.NewRat(5, 2)

// checkAnthropicCacheTiers reports the relationship Anthropic's published
// pricing keeps: a write is never cheaper than plain input, a longer time to
// live is never cheaper than a shorter one, and an hour is not an order of
// magnitude above base input. The exact multiples are not asserted, because
// Anthropic rounds them — claude-3-haiku's 5-minute write is 1.2x base input,
// not the documented 1.25x.
func checkAnthropicCacheTiers(r rates) error {
	input, ok := new(big.Rat).SetString(r.UncachedInputUSDPerToken)
	if !ok {
		return fmt.Errorf("uninterpretable input rate %q", r.UncachedInputUSDPerToken)
	}
	write, err := optionalRat(r.CacheWriteUSDPerToken)
	if err != nil {
		return err
	}
	write1H, err := optionalRat(r.CacheWrite1HUSDPerToken)
	if err != nil {
		return err
	}
	if write != nil && write.Cmp(input) < 0 {
		return fmt.Errorf("5m write %s is below input %s", write.RatString(), input.RatString())
	}
	if write1H != nil {
		if write != nil && write1H.Cmp(write) < 0 {
			return fmt.Errorf("1h write %s is below the 5m write %s", write1H.RatString(), write.RatString())
		}
		if ceiling := new(big.Rat).Mul(input, maxAnthropicWriteMultiple); write1H.Cmp(ceiling) > 0 {
			return fmt.Errorf("1h write %s exceeds %s times input %s",
				write1H.RatString(), maxAnthropicWriteMultiple.RatString(), input.RatString())
		}
	}
	return nil
}

func optionalRat(value *string) (*big.Rat, error) {
	if value == nil {
		return nil, nil
	}
	parsed, ok := new(big.Rat).SetString(*value)
	if !ok {
		return nil, fmt.Errorf("uninterpretable rate %q", *value)
	}
	return parsed, nil
}
