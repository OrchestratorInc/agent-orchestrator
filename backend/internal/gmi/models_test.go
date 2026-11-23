package gmi

import (
	"encoding/json"
	"math"
	"testing"
)

func price(value float64) *float64 { return &value }

func TestPublishedPriceIsNotTheSameAsNoPrice(t *testing.T) {
	free := PriceInfo{Price: price(0)}
	if !free.HasPublishedPrice() {
		t.Fatal("a model published as free has a price")
	}
	unknown := PriceInfo{}
	if unknown.HasPublishedPrice() {
		t.Fatal("a model with no published price has an unknown cost")
	}
}

func TestEstimateCostPricesEachUnit(t *testing.T) {
	cases := []struct {
		name      string
		model     Model
		options   EstimateOptions
		wantTotal float64
		wantUnit  string
	}{
		{
			name:      "one image",
			model:     Model{ModelID: "image", PriceInfo: PriceInfo{Price: price(85000), Unit: "image"}},
			options:   EstimateOptions{Count: 1},
			wantTotal: 0.085,
			wantUnit:  "image",
		},
		{
			name:      "four images",
			model:     Model{ModelID: "image", PriceInfo: PriceInfo{Price: price(85000), Unit: "image"}},
			options:   EstimateOptions{Count: 4},
			wantTotal: 0.34,
			wantUnit:  "image",
		},
		{
			name:      "a video of eight seconds",
			model:     Model{ModelID: "video", PriceInfo: PriceInfo{Price: price(100000), Unit: "second"}},
			options:   EstimateOptions{Duration: 8, HasDuration: true},
			wantTotal: 0.8,
			wantUnit:  "second",
		},
		{
			name:      "a free model",
			model:     Model{ModelID: "free", PriceInfo: PriceInfo{Price: price(0), Unit: "image"}},
			options:   EstimateOptions{Count: 1},
			wantTotal: 0,
			wantUnit:  "image",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			estimate, err := EstimateCost(testCase.model, testCase.options)
			if err != nil {
				t.Fatalf("EstimateCost: %v", err)
			}
			if estimate.Unit != testCase.wantUnit {
				t.Fatalf("unit = %q, want %q", estimate.Unit, testCase.wantUnit)
			}
			if math.Abs(estimate.TotalUSD-testCase.wantTotal) > 1e-9 {
				t.Fatalf("total = %v, want %v", estimate.TotalUSD, testCase.wantTotal)
			}
		})
	}
}

func TestEstimateCostRefusesToPriceAnUnpublishedModel(t *testing.T) {
	model := Model{ModelID: "unpriced"}
	estimate, err := EstimateCost(model, EstimateOptions{Count: 1})
	if err == nil {
		t.Fatalf("EstimateCost = %v, want an error rather than a zero price", estimate)
	}
	if estimate.TotalUSD != 0 {
		t.Fatalf("total = %v, want no estimate at all", estimate.TotalUSD)
	}
}

func TestEstimateCostAppliesThePublishedDiscount(t *testing.T) {
	model := Model{
		ModelID:   "image",
		PriceInfo: PriceInfo{Price: price(100000), Unit: "image"},
		Discount:  20,
	}
	estimate, err := EstimateCost(model, EstimateOptions{Count: 1})
	if err != nil {
		t.Fatalf("EstimateCost: %v", err)
	}
	if math.Abs(estimate.TotalUSD-0.08) > 1e-9 {
		t.Fatalf("total = %v, want 0.08 after a 20%% discount", estimate.TotalUSD)
	}
}

func TestModelReadsEverySpellingOfItsFields(t *testing.T) {
	var model Model
	if err := json.Unmarshal([]byte(`{
		"model": "seedream-4",
		"brief_description": "An image model",
		"external_provider": "bytedance",
		"available": false,
		"price_info": {"price": 85000, "unit": "image"}
	}`), &model); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if model.ModelID != "seedream-4" {
		t.Fatalf("model id = %q, want seedream-4", model.ModelID)
	}
	if model.Description != "An image model" {
		t.Fatalf("description = %q, want the brief description", model.Description)
	}
	if model.Provider != "bytedance" {
		t.Fatalf("provider = %q, want bytedance", model.Provider)
	}
	if model.IsAvailable() {
		t.Fatal("the model says it is unavailable")
	}
}

func TestParameterReadsItsDefaultAndOptions(t *testing.T) {
	var model Model
	if err := json.Unmarshal([]byte(`{"parameters":[
		{"name":"size","type":"string","options":["1024x1024","1024x1536"]},
		{"name":"n","type":"integer","default_value":2},
		{"name":"style","type":"enum","options":[{"value":"photo"}],"default":"photo"}
	]}`), &model); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	size := model.Parameter("size")
	if size == nil || len(size.Options) != 2 {
		t.Fatalf("size parameter = %+v, want two options", size)
	}
	count := model.Parameter("n")
	if count == nil || count.Default != float64(2) {
		t.Fatalf("n default = %v, want 2", count.Default)
	}
	style := model.Parameter("style")
	if style == nil || len(style.Options) != 1 || style.Options[0] != "photo" {
		t.Fatalf("style options = %+v, want one option read from an object", style)
	}
}

// The catalog publishes an enum's allowed values under "enum", a numeric
// parameter's bounds under "min" and "max", and a parameter's name under
// "key" in some answers.
func TestParameterReadsItsEnumItsBoundsAndItsKey(t *testing.T) {
	var model Model
	if err := json.Unmarshal([]byte(`{"parameters":[
		{"key":"size","type":"enum","enum":["1024x1024","1152x864"],"default":"1024x1024"},
		{"name":"guidance_scale","type":"float","min":1,"max":10,"default":2.5}
	]}`), &model); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	size := model.Parameter("size")
	if size == nil || len(size.Options) != 2 || size.Options[0] != "1024x1024" {
		t.Fatalf("size parameter = %+v, want the two values read from its enum", size)
	}
	guidance := model.Parameter("guidance_scale")
	if guidance == nil || guidance.Minimum == nil || *guidance.Minimum != 1 {
		t.Fatalf("guidance_scale minimum = %v, want 1", guidance)
	}
	if guidance.Maximum == nil || *guidance.Maximum != 10 {
		t.Fatalf("guidance_scale maximum = %v, want 10", guidance.Maximum)
	}
}

// The catalog publishes a model's price inside its own multimodal block.
func TestModelReadsThePriceFromItsMultimodalBlock(t *testing.T) {
	var model Model
	if err := json.Unmarshal([]byte(`{
		"model_id": "seedream-3-0-t2i-250415",
		"kind": "multimodal",
		"multimodal": {"model_type": "image", "pricing": {"price": 50000, "pricing_type": "per image", "unit": "image"}}
	}`), &model); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if model.PriceInfo.Price == nil || *model.PriceInfo.Price != 50000 {
		t.Fatalf("price = %v, want 50000 micro-USD", model.PriceInfo.Price)
	}
	if model.PriceInfo.Unit != "image" {
		t.Fatalf("unit = %q, want image", model.PriceInfo.Unit)
	}
	if got := model.Modality(); got != "image" {
		t.Fatalf("Modality = %q, want image", got)
	}
}

// The catalog publishes a model's modalities as an object of named lists,
// and a model whose modalities were read as the wrong shape would fail to
// decode whole, losing its price and its parameters with it.
func TestModelReadsAModalitiesObject(t *testing.T) {
	var model Model
	if err := json.Unmarshal([]byte(`{
		"model": "seedream-3-0-t2i-250415",
		"display_name": "seedream-3-0-t2i-250415",
		"brief_description": "High-quality text-to-image generation.",
		"external_provider": "byteplus-seedream",
		"model_type": "image",
		"status": "active",
		"price_info": {"pricing_type": "per image", "price": 50000, "unit": "image"},
		"percentage_discount": 0,
		"modalities": {"input": [{"type": "text", "required": true}], "output": [{"type": "image", "required": true}]},
		"parameters": [
			{"name": "prompt", "type": "string", "required": true, "default_value": ""},
			{"name": "size", "type": "enum", "default_value": "1024x1024", "options": [{"value": "1024x1024"}]}
		]
	}`), &model); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if model.ModelID != "seedream-3-0-t2i-250415" {
		t.Fatalf("model id = %q, want seedream-3-0-t2i-250415", model.ModelID)
	}
	if model.Provider != "byteplus-seedream" {
		t.Fatalf("provider = %q, want byteplus-seedream", model.Provider)
	}
	if model.Status != "active" {
		t.Fatalf("status = %q, want active", model.Status)
	}
	if model.PriceInfo.Price == nil || *model.PriceInfo.Price != 50000 {
		t.Fatalf("price = %v, want 50000 micro-USD", model.PriceInfo.Price)
	}
	if model.PriceInfo.Unit != "image" {
		t.Fatalf("unit = %q, want image", model.PriceInfo.Unit)
	}
	if len(model.Parameters) != 2 {
		t.Fatalf("parameters = %+v, want the two the catalog published", model.Parameters)
	}
	if size := model.Parameter("size"); size == nil || len(size.Options) != 1 || size.Options[0] != "1024x1024" {
		t.Fatalf("size parameter = %+v, want its one option", size)
	}
}

// The modalities are read as a list by other deployments.
func TestModelReadsAModalitiesList(t *testing.T) {
	var model Model
	if err := json.Unmarshal([]byte(`{
		"model": "image",
		"modalities": [{"type": "image", "pricing": {"price": 25000, "unit": "image"}}]
	}`), &model); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if model.PriceInfo.Price == nil || *model.PriceInfo.Price != 25000 {
		t.Fatalf("price = %v, want the price under the modality", model.PriceInfo.Price)
	}
}

// An option published as a bare number or boolean is an option the model
// accepts, so it has to be readable: an option that cannot be read is an
// option a caller can break.
func TestParameterReadsNumericAndBooleanOptions(t *testing.T) {
	var model Model
	if err := json.Unmarshal([]byte(`{"parameters":[
		{"name":"steps","type":"integer","options":[5,10]},
		{"name":"enhance","type":"boolean","options":[true,false]}
	]}`), &model); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	steps := model.Parameter("steps")
	if steps == nil || len(steps.Options) != 2 || steps.Options[0] != "5" || steps.Options[1] != "10" {
		t.Fatalf("steps options = %+v, want the two numbers", steps)
	}
	enhance := model.Parameter("enhance")
	if enhance == nil || len(enhance.Options) != 2 || enhance.Options[0] != "true" || enhance.Options[1] != "false" {
		t.Fatalf("enhance options = %+v, want the two booleans", enhance)
	}
}

// A model priced only by a chart publishes its rate inside the nested
// pricing, so the chart travels with the price it belongs to.
func TestPriceInfoReadsTheChartOfItsNestedPricing(t *testing.T) {
	var info PriceInfo
	if err := json.Unmarshal([]byte(`{"pricing":{"price_chart":{"rate":50000},"unit":"image"}}`), &info); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !info.HasPublishedPrice() {
		t.Fatal("a model priced by a nested chart has no published price")
	}
	if info.PriceChart == nil || info.PriceChart.Rate == nil || *info.PriceChart.Rate != 50000 {
		t.Fatalf("price chart = %+v, want the nested rate", info.PriceChart)
	}
	if info.UnitPriceUSD() != 0.05 {
		t.Fatalf("unit price = %v, want 0.05", info.UnitPriceUSD())
	}
}

// A video model is priced per second, so the length the catalog publishes
// as its default is part of its cost.
func TestEstimateCostUsesTheModelsDeclaredDuration(t *testing.T) {
	model := Model{
		ModelID:   "video",
		PriceInfo: PriceInfo{Price: price(100000), Unit: "second"},
		Parameters: []Parameter{
			{Name: "durationSeconds", Type: "integer", Default: float64(10)},
		},
	}
	estimate, err := EstimateCost(model, EstimateOptions{})
	if err != nil {
		t.Fatalf("EstimateCost: %v", err)
	}
	if estimate.Multiplier != 10 {
		t.Fatalf("multiplier = %v, want the model's declared length of ten seconds", estimate.Multiplier)
	}
	if estimate.TotalUSD != 1 {
		t.Fatalf("total = %v, want 1", estimate.TotalUSD)
	}
	// A length the caller asked for still wins over the declared default.
	estimate, err = EstimateCost(model, EstimateOptions{Duration: 4, HasDuration: true})
	if err != nil {
		t.Fatalf("EstimateCost: %v", err)
	}
	if estimate.TotalUSD != 0.4 {
		t.Fatalf("total = %v, want 0.4", estimate.TotalUSD)
	}
}

// A price published as a bare number is still a price.
func TestPriceInfoReadsABareNumber(t *testing.T) {
	var info PriceInfo
	if err := json.Unmarshal([]byte(`50000`), &info); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if info.Price == nil || *info.Price != 50000 {
		t.Fatalf("price = %v, want 50000 micro-USD", info.Price)
	}
	if !info.HasPublishedPrice() {
		t.Fatal("a bare number is a published price")
	}
}

// The API publishes a request's timestamps as epoch numbers and its media
// under either spelling, so a request must decode from both.
func TestRequestReadsTheTimestampsTheAPIPublishes(t *testing.T) {
	var request Request
	if err := json.Unmarshal([]byte(`{
		"request_id": "req-1",
		"model": "seedream-3-0-t2i-250415",
		"status": "success",
		"created_at": 1766420000,
		"outcome": {
			"media_urls": [{"mime_type": "image/png", "type": "image", "url": "https://storage.example/a.png"}],
			"thumbnail_image_url": "https://storage.example/a-thumb.png"
		}
	}`), &request); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if request.CreatedAt != "1766420000" {
		t.Fatalf("created_at = %q, want the epoch it was published as", request.CreatedAt)
	}
	urls := request.MediaURLs()
	if len(urls) != 1 || urls[0] != "https://storage.example/a.png" {
		t.Fatalf("media urls = %v, want the produced media", urls)
	}
	if request.Outcome.Thumbnail() != "https://storage.example/a-thumb.png" {
		t.Fatalf("thumbnail = %q, want the outcome's thumbnail", request.Outcome.Thumbnail())
	}
}

// A request that failed carries the provider's own words.
func TestRequestReadsTheFailureReason(t *testing.T) {
	var request Request
	if err := json.Unmarshal([]byte(`{
		"request_id": "req-2",
		"status": "failed",
		"failure_reason": "the API key is forbidden by IP whitelist setting"
	}`), &request); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if request.Error != "the API key is forbidden by IP whitelist setting" {
		t.Fatalf("error = %q, want the failure reason", request.Error)
	}
}

func TestModalityReadsTheKindOfMedia(t *testing.T) {
	cases := []struct {
		model Model
		want  string
	}{
		{model: Model{ModelType: "video_generation"}, want: "video"},
		{model: Model{ModelType: "image"}, want: "image"},
		{model: Model{Kind: "text to speech"}, want: "audio"},
		{model: Model{}, want: "image"},
	}
	for _, testCase := range cases {
		if got := testCase.model.Modality(); got != testCase.want {
			t.Fatalf("Modality = %q, want %q", got, testCase.want)
		}
	}
}
