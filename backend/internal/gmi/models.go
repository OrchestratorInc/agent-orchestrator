package gmi

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
)

// microUSD is the unit the catalog prices in: a price of 85000 is $0.085.
const microUSD = 1_000_000.0

// defaultVideoDuration is the length assumed for a video model when the
// caller does not ask for one. It matches the duration the catalog's own
// per-second pricing is normally quoted at.
const defaultVideoDuration = 5

// Model is one entry in GMI Cloud's catalog.
//
// The catalog spells the same field differently in different places: the
// catalog endpoint names the model "model", the model detail endpoint
// names it "model", and GMI's MCP tools publish it as "model_id". Each
// field is read from every spelling it appears under, so a caller reads one
// shape whichever endpoint answered.
type Model struct {
	ModelID     string      `json:"model_id"`
	DisplayName string      `json:"display_name"`
	ModelType   string      `json:"model_type"`
	Kind        string      `json:"kind"`
	Provider    string      `json:"provider"`
	Status      string      `json:"status"`
	Description string      `json:"description"`
	Available   *bool       `json:"available"`
	Parameters  []Parameter `json:"parameters"`
	PriceInfo   PriceInfo   `json:"price_info"`
	// Discount is the account's published discount, as a fraction.
	Discount float64 `json:"percentage_discount"`
}

// UnmarshalJSON reads a catalog entry, accepting every spelling of each
// field the API uses.
func (m *Model) UnmarshalJSON(data []byte) error {
	var raw struct {
		ModelID      string          `json:"model_id"`
		Model        string          `json:"model"`
		DisplayName  string          `json:"display_name"`
		ModelType    string          `json:"model_type"`
		Kind         string          `json:"kind"`
		ExternalProv string          `json:"external_provider"`
		Provider     string          `json:"provider"`
		Status       string          `json:"status"`
		Brief        string          `json:"brief_description"`
		Description  string          `json:"description"`
		Available    *bool           `json:"available"`
		Parameters   []Parameter     `json:"parameters"`
		PriceInfo    PriceInfo       `json:"price_info"`
		RawDiscount  json.Number     `json:"percentage_discount"`
		Multimodal   json.RawMessage `json:"multimodal"`
		Modalities   json.RawMessage `json:"modalities"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.ModelID = firstNonEmpty(raw.ModelID, raw.Model)
	m.DisplayName = firstNonEmpty(raw.DisplayName, m.ModelID)
	m.Kind = raw.Kind
	m.Provider = firstNonEmpty(raw.ExternalProv, raw.Provider)
	m.Status = raw.Status
	m.Description = firstNonEmpty(raw.Brief, raw.Description)
	m.Available = raw.Available
	m.Parameters = raw.Parameters
	m.Discount = parseDiscount(raw.RawDiscount)
	m.ModelType = firstNonEmpty(raw.ModelType, modelTypeOf(raw.Multimodal))
	m.PriceInfo = raw.PriceInfo
	// The catalog publishes the price in more than one place: under
	// price_info, under the model's own multimodal block, and under one of
	// its modalities. The first place that publishes one wins.
	if info, ok := priceInfoFrom(data); ok {
		m.PriceInfo = info
	}
	if !m.PriceInfo.HasPublishedPrice() {
		if info, ok := priceInfoFrom(raw.Multimodal); ok {
			m.PriceInfo = info
		}
	}
	if !m.PriceInfo.HasPublishedPrice() {
		for _, modality := range modalityBlocks(raw.Modalities) {
			if info, ok := priceInfoFrom(modality); ok {
				m.PriceInfo = info
				break
			}
		}
	}
	return nil
}

// modalityBlocks returns the modality blocks a catalog entry publishes.
//
// The catalog spells a model's modalities as an object of named lists, such
// as {"input": [...], "output": [...]}, and other deployments publish them as
// one list. Both are read here, so a model decodes whichever shape arrived:
// an entry whose modalities were read as the wrong shape would fail to
// decode whole, and would lose its price and its parameters with it.
func modalityBlocks(data json.RawMessage) []json.RawMessage {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if trimmed[0] == '[' {
		var blocks []json.RawMessage
		if err := json.Unmarshal(data, &blocks); err != nil {
			return nil
		}
		return blocks
	}
	if trimmed[0] != '{' {
		return nil
	}
	var named map[string]json.RawMessage
	if err := json.Unmarshal(data, &named); err != nil {
		return nil
	}
	blocks := make([]json.RawMessage, 0, len(named))
	for _, value := range named {
		trimmedValue := strings.TrimSpace(string(value))
		if trimmedValue == "" || trimmedValue == "null" {
			continue
		}
		if trimmedValue[0] == '[' {
			var list []json.RawMessage
			if err := json.Unmarshal(value, &list); err != nil {
				continue
			}
			blocks = append(blocks, list...)
			continue
		}
		blocks = append(blocks, value)
	}
	return blocks
}

// priceInfoFrom reads a price out of one catalog object, whichever key
// holds it.
func priceInfoFrom(data []byte) (PriceInfo, bool) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" || trimmed[0] != '{' {
		return PriceInfo{}, false
	}
	var object struct {
		PriceInfo PriceInfo       `json:"price_info"`
		Pricing   PriceInfo       `json:"pricing"`
		Price     json.RawMessage `json:"price"`
		Unit      string          `json:"unit"`
		Type      string          `json:"pricing_type"`
	}
	if err := json.Unmarshal(data, &object); err != nil {
		return PriceInfo{}, false
	}
	for _, candidate := range []PriceInfo{object.PriceInfo, object.Pricing} {
		if candidate.HasPublishedPrice() {
			return candidate, true
		}
	}
	if value, ok := parseNumber(object.Price); ok {
		return PriceInfo{Price: &value, Unit: object.Unit, PricingType: object.Type}, true
	}
	return PriceInfo{}, false
}

// modelTypeOf reads the media a model produces out of its multimodal block.
func modelTypeOf(data []byte) string {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" || trimmed[0] != '{' {
		return ""
	}
	var object struct {
		ModelType string `json:"model_type"`
	}
	if err := json.Unmarshal(data, &object); err != nil {
		return ""
	}
	return object.ModelType
}

// parseNumber reads a number the API published as a number or as a string.
func parseNumber(raw json.RawMessage) (float64, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return 0, false
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, true
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	}
	return 0, false
}

// Modality returns the kind of media the model produces: image, video or
// audio. It reads model_type, falling back to the modality words in the
// kind and description.
func (m Model) Modality() string {
	value := strings.ToLower(strings.TrimSpace(m.ModelType))
	switch {
	case strings.HasPrefix(value, "video"):
		return "video"
	case strings.HasPrefix(value, "audio"), strings.HasPrefix(value, "speech"), strings.HasPrefix(value, "music"):
		return "audio"
	case value != "":
		return value
	}
	kind := strings.ToLower(m.Kind + " " + m.Description)
	switch {
	case strings.Contains(kind, "video"):
		return "video"
	case strings.Contains(kind, "audio"), strings.Contains(kind, "speech"), strings.Contains(kind, "music"):
		return "audio"
	}
	return "image"
}

// IsAvailable reports whether the catalog says the model can be used. A
// model that says nothing about availability is treated as available: the
// catalog only marks a model unavailable when it knows it is.
func (m Model) IsAvailable() bool { return m.Available == nil || *m.Available }

// Parameter is one input a media model declares.
//
// The catalog publishes an option either as a bare value or as an object
// with a "value" field, a default under either "default" or
// "default_value", and the allowed values under either "options" or
// "enum".
type Parameter struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Default     any      `json:"default"`
	Options     []string `json:"options"`
	Description string   `json:"description"`
	// Minimum and Maximum are the bounds the catalog publishes for a
	// numeric parameter.
	Minimum *float64 `json:"min"`
	Maximum *float64 `json:"max"`
}

// UnmarshalJSON reads a parameter, accepting every spelling of its name,
// its default, its options and its bounds.
func (p *Parameter) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name         string            `json:"name"`
		Key          string            `json:"key"`
		Type         string            `json:"type"`
		Required     bool              `json:"required"`
		Default      any               `json:"default"`
		DefaultValue any               `json:"default_value"`
		Options      []json.RawMessage `json:"options"`
		Enum         []json.RawMessage `json:"enum"`
		Description  string            `json:"description"`
		Minimum      json.RawMessage   `json:"min"`
		Maximum      json.RawMessage   `json:"max"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	p.Name = firstNonEmpty(raw.Name, raw.Key)
	p.Type = strings.ToLower(strings.TrimSpace(raw.Type))
	if p.Type == "" {
		p.Type = "string"
	}
	p.Required = raw.Required
	p.Default = raw.Default
	if p.Default == nil {
		p.Default = raw.DefaultValue
	}
	p.Description = raw.Description
	p.Options = nil
	for _, option := range append(raw.Options, raw.Enum...) {
		var text string
		if err := json.Unmarshal(option, &text); err == nil {
			p.Options = append(p.Options, text)
			continue
		}
		var object struct {
			Value any `json:"value"`
		}
		if err := json.Unmarshal(option, &object); err == nil && object.Value != nil {
			p.Options = append(p.Options, fmt.Sprintf("%v", object.Value))
		}
	}
	if value, ok := parseNumber(raw.Minimum); ok {
		p.Minimum = &value
	}
	if value, ok := parseNumber(raw.Maximum); ok {
		p.Maximum = &value
	}
	return nil
}

// PriceInfo is what the catalog publishes about a model's price.
type PriceInfo struct {
	// Price is the price of one unit in micro-USD.
	Price *float64 `json:"price"`
	// Unit names what one unit is: image, request, second, character.
	Unit string `json:"unit"`
	// PricingType is the catalog's own wording for the price.
	PricingType string `json:"pricing_type"`
	// PriceChart carries a per-tier rate for models priced by a chart.
	PriceChart *struct {
		Rate *float64 `json:"rate"`
	} `json:"price_chart"`
}

// UnmarshalJSON reads a price, whether the catalog publishes it as an
// object, as a nested pricing object, or as a bare number.
func (p *PriceInfo) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if trimmed[0] != '{' {
		if value, ok := parseNumber(json.RawMessage(trimmed)); ok {
			p.Price = &value
		}
		return nil
	}
	var raw struct {
		Price       json.RawMessage `json:"price"`
		Unit        string          `json:"unit"`
		PricingType string          `json:"pricing_type"`
		PriceChart  *struct {
			Rate *float64 `json:"rate"`
		} `json:"price_chart"`
		Pricing *PriceInfo `json:"pricing"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if value, ok := parseNumber(raw.Price); ok {
		p.Price = &value
	}
	p.Unit = raw.Unit
	p.PricingType = raw.PricingType
	p.PriceChart = raw.PriceChart
	if !p.HasPublishedPrice() && raw.Pricing != nil {
		p.Price = raw.Pricing.Price
		if p.Unit == "" {
			p.Unit = raw.Pricing.Unit
		}
		if p.PricingType == "" {
			p.PricingType = raw.Pricing.PricingType
		}
	}
	return nil
}

// HasPublishedPrice reports whether the catalog publishes a price for the
// model.
//
// A model published as free has a price of zero, and that is a price. A
// model the catalog says nothing about has an unknown cost, and an
// unknown cost is not a zero cost: pricing it at $0.00 would let it past
// every spend cap while its real price is unbounded.
func (p PriceInfo) HasPublishedPrice() bool {
	if p.Price != nil {
		return true
	}
	return p.PriceChart != nil && p.PriceChart.Rate != nil
}

// UnitPriceUSD returns the price of one unit in USD.
func (p PriceInfo) UnitPriceUSD() float64 {
	if p.Price != nil {
		return *p.Price / microUSD
	}
	if p.PriceChart != nil && p.PriceChart.Rate != nil {
		return *p.PriceChart.Rate / microUSD
	}
	return 0
}

// PricingUnit returns what one unit of this price is: the catalog's own
// unit when it names one, otherwise the unit its pricing type describes.
func (p PriceInfo) PricingUnit() string {
	if unit := strings.ToLower(strings.TrimSpace(p.Unit)); unit != "" {
		return unit
	}
	pricingType := strings.ToLower(p.PricingType)
	for _, token := range []string{"second", "character", "image", "request", "generation", "token"} {
		if strings.Contains(pricingType, token) {
			return token
		}
	}
	return "request"
}

// Request is one generation request in GMI Cloud's queue.
type Request struct {
	RequestID string `json:"request_id"`
	ID        string `json:"id"`
	Model     string `json:"model"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	// Payload is what the request asked for, which is what the request's
	// cost is computed from.
	Payload map[string]any `json:"payload"`
	Outcome *Outcome       `json:"outcome"`
	Error   string         `json:"error"`
}

// UnmarshalJSON reads a request, accepting every spelling of its id.
func (r *Request) UnmarshalJSON(data []byte) error {
	var raw struct {
		RequestID     string          `json:"request_id"`
		ID            string          `json:"id"`
		Model         string          `json:"model"`
		Status        string          `json:"status"`
		CreatedAt     json.RawMessage `json:"created_at"`
		Payload       map[string]any  `json:"payload"`
		Outcome       *Outcome        `json:"outcome"`
		Error         json.RawMessage `json:"error"`
		FailureReason json.RawMessage `json:"failure_reason"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.RequestID = firstNonEmpty(raw.RequestID, raw.ID)
	r.ID = raw.ID
	r.Model = raw.Model
	r.Status = strings.ToLower(strings.TrimSpace(raw.Status))
	r.CreatedAt = scalarText(raw.CreatedAt)
	r.Payload = raw.Payload
	r.Outcome = raw.Outcome
	r.Error = errorText(raw.Error)
	if r.Error == "" {
		r.Error = errorText(raw.FailureReason)
	}
	return nil
}

// scalarText renders a value the API published as a number or as a string.
func scalarText(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		return number.String()
	}
	return trimmed
}

// MediaURLs returns the URLs of the media a finished request produced.
func (r Request) MediaURLs() []string {
	if r.Outcome == nil {
		return nil
	}
	return r.Outcome.URLs()
}

// Succeeded reports whether the request finished with media.
func (r Request) Succeeded() bool { return r.Status == "success" }

// Outcome is the result of a finished generation request.
type Outcome struct {
	MediaURLs    []MediaURL `json:"media_urls"`
	Media        []MediaURL `json:"media"`
	ThumbnailURL string     `json:"thumbnail_image_url"`
	ThumbnailAlt string     `json:"thumbnail_url"`
	Poster       string     `json:"poster"`
	VideoURL     string     `json:"video_url"`
	ImageURL     string     `json:"image_url"`
	AudioURL     string     `json:"audio_url"`
	Error        string     `json:"-"`
}

// UnmarshalJSON reads an outcome, whose error is a string in some answers and
// an object with a message in others.
func (o *Outcome) UnmarshalJSON(data []byte) error {
	var raw struct {
		MediaURLs    []MediaURL      `json:"media_urls"`
		Media        []MediaURL      `json:"media"`
		ThumbnailURL string          `json:"thumbnail_image_url"`
		ThumbnailAlt string          `json:"thumbnail_url"`
		Poster       string          `json:"poster"`
		VideoURL     string          `json:"video_url"`
		ImageURL     string          `json:"image_url"`
		AudioURL     string          `json:"audio_url"`
		Error        json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	o.MediaURLs = raw.MediaURLs
	o.Media = raw.Media
	o.ThumbnailURL = raw.ThumbnailURL
	o.ThumbnailAlt = raw.ThumbnailAlt
	o.Poster = raw.Poster
	o.VideoURL = raw.VideoURL
	o.ImageURL = raw.ImageURL
	o.AudioURL = raw.AudioURL
	o.Error = errorText(raw.Error)
	return nil
}

// Thumbnail returns the outcome's thumbnail URL under any of its spellings.
func (o Outcome) Thumbnail() string {
	return firstNonEmpty(o.ThumbnailURL, o.ThumbnailAlt, o.Poster)
}

// URLs returns every media URL the outcome carries, in the order the API
// listed them. A model that answers with a single named url instead of a
// list still yields its url.
func (o Outcome) URLs() []string {
	urls := make([]string, 0, len(o.MediaURLs)+len(o.Media)+3)
	seen := map[string]bool{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		urls = append(urls, value)
	}
	for _, media := range o.MediaURLs {
		add(media.URL)
	}
	for _, media := range o.Media {
		add(media.URL)
	}
	add(o.VideoURL)
	add(o.ImageURL)
	add(o.AudioURL)
	return urls
}

// MediaURL is one produced media file. The API publishes it as a bare URL
// string in some answers and as an object in others.
type MediaURL struct {
	URL string
}

// UnmarshalJSON accepts both the string and the object spelling.
func (m *MediaURL) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		m.URL = strings.TrimSpace(text)
		return nil
	}
	var object struct {
		URL      string `json:"url"`
		MediaURL string `json:"media_url"`
		ImageURL string `json:"image_url"`
	}
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	m.URL = firstNonEmpty(object.URL, object.MediaURL, object.ImageURL)
	return nil
}

// MarshalJSON writes the URL as a bare string, the shape the API publishes.
func (m MediaURL) MarshalJSON() ([]byte, error) { return json.Marshal(m.URL) }

// EstimateOptions are the inputs that decide what one generation costs.
type EstimateOptions struct {
	// Count is how many outputs the generation produces.
	Count int
	// Duration is the length of a video, in seconds.
	Duration float64
	// HasDuration distinguishes "no duration asked for" from "zero seconds".
	HasDuration bool
	// Text is the prompt, which prices the models billed per character.
	Text string
}

// Estimate is what one generation is expected to cost at list price.
type Estimate struct {
	ModelID      string  `json:"model_id"`
	Unit         string  `json:"unit"`
	UnitPriceUSD float64 `json:"unit_price_usd"`
	Multiplier   float64 `json:"multiplier"`
	Discount     float64 `json:"discount"`
	TotalUSD     float64 `json:"total_usd"`
	// Note says what the estimate is based on, so a caller can tell a priced
	// estimate from a guess.
	Note string `json:"note"`
}

// EstimateCost prices a generation of model at the catalog's list price.
//
// A model the catalog publishes no price for has no estimate: this
// returns an error rather than $0.00, because $0.00 is what an unknown
// price looks like and a caller that cannot tell the difference between
// "free" and "unknown" spends against a cap it never applied.
func EstimateCost(model Model, opts EstimateOptions) (Estimate, error) {
	if !model.PriceInfo.HasPublishedPrice() {
		return Estimate{}, fmt.Errorf("the catalog publishes no price for %q, so the cost of a generation with it cannot be estimated", model.ModelID)
	}
	unit := model.PriceInfo.PricingUnit()
	price := model.PriceInfo.UnitPriceUSD()
	discount := model.Discount
	if discount > 1 {
		discount /= 100
	}
	if discount < 0 || discount >= 1 {
		discount = 0
	}
	estimate := Estimate{
		ModelID:      model.ModelID,
		Unit:         unit,
		UnitPriceUSD: price,
		Discount:     discount,
	}
	if price <= 0 {
		estimate.Multiplier = 1
		estimate.TotalUSD = 0
		estimate.Note = "the catalog publishes this model as free"
		return estimate, nil
	}
	switch unit {
	case "second":
		seconds := float64(defaultVideoDuration)
		if opts.HasDuration && opts.Duration > 0 {
			seconds = opts.Duration
		}
		estimate.Multiplier = seconds
	case "character":
		estimate.Multiplier = math.Max(1, math.Ceil(float64(len(opts.Text))/1000))
	default:
		count := opts.Count
		if count < 1 {
			count = 1
		}
		estimate.Multiplier = float64(count)
	}
	total := price * estimate.Multiplier
	if discount > 0 {
		total *= 1 - discount
	}
	estimate.TotalUSD = roundUSD(total)
	estimate.Note = "list price, before this organization's discount"
	return estimate, nil
}

// roundUSD rounds a dollar amount to the eighth decimal, which is finer
// than any price GMI Cloud publishes.
func roundUSD(value float64) float64 { return math.Round(value*1e8) / 1e8 }

// parseDiscount reads the catalog's discount, which is a fraction in some
// answers and a percentage in others.
func parseDiscount(value json.Number) float64 {
	if value == "" {
		return 0
	}
	parsed, err := value.Float64()
	if err != nil {
		return 0
	}
	if parsed > 1 {
		parsed /= 100
	}
	if parsed < 0 || parsed >= 1 {
		return 0
	}
	return parsed
}

// errorText renders an error field, which is a string in some answers and
// an object with a message in others.
func errorText(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var object struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &object); err == nil {
		return strings.TrimSpace(object.Message)
	}
	return truncate(trimmed, 300)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func pathEscape(value string) string { return url.PathEscape(value) }

func itoa(value int) string { return strconv.Itoa(value) }

// truncate shortens a string to at most limit characters.
func truncate(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}
