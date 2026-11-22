package gmi

import "testing"

func imageModel() Model {
	return Model{
		ModelID: "image",
		Parameters: []Parameter{
			{Name: "prompt", Type: "string", Required: true},
			{Name: "image_size", Type: "string", Options: []string{"1024x1024", "1024x1536"}, Default: "1024x1024"},
			{Name: "n", Type: "integer", Default: 1},
			{Name: "seed", Type: "integer"},
		},
	}
}

func TestBuildPayloadMapsOptionsOntoTheModelsOwnNames(t *testing.T) {
	payload, dropped := imageModel().BuildPayload("a cat", map[string]any{
		"aspect_ratio": "3:4",
		"n":            float64(3),
		"seed":         "42",
		"not_a_field":  true,
	})
	if len(dropped) != 1 || dropped[0] != "not_a_field" {
		t.Fatalf("dropped = %v, want the unknown option", dropped)
	}
	if payload["prompt"] != "a cat" {
		t.Fatalf("prompt = %v, want the prompt", payload["prompt"])
	}
	// The model calls the size "image_size"; the request asked for a ratio.
	if payload["image_size"] != "1024x1536" {
		t.Fatalf("image_size = %v, want the size that matches 3:4", payload["image_size"])
	}
	if payload["n"] != 3 {
		t.Fatalf("n = %v (%T), want the integer 3", payload["n"], payload["n"])
	}
	if payload["seed"] != 42 {
		t.Fatalf("seed = %v (%T), want the integer 42", payload["seed"], payload["seed"])
	}
}

func TestBuildPayloadFillsTheModelsDefaults(t *testing.T) {
	payload, _ := imageModel().BuildPayload("a cat", nil)
	if payload["image_size"] != "1024x1024" {
		t.Fatalf("image_size = %v, want the model's default", payload["image_size"])
	}
	if payload["n"] != 1 {
		t.Fatalf("n = %v, want the model's default", payload["n"])
	}
}

func TestBuildPayloadNeedsAPrompt(t *testing.T) {
	payload, dropped := imageModel().BuildPayload("   ", nil)
	if payload != nil {
		t.Fatalf("payload = %v, want none", payload)
	}
	if len(dropped) != 1 || dropped[0] != "prompt" {
		t.Fatalf("dropped = %v, want the prompt reported", dropped)
	}
}

func TestMissingRequiredNamesTheParametersThePayloadLacks(t *testing.T) {
	model := Model{Parameters: []Parameter{
		{Name: "prompt", Required: true},
		{Name: "first_frame", Required: true},
	}}
	missing := model.MissingRequired(map[string]any{"prompt": "a cat"})
	if len(missing) != 1 || missing[0] != "first_frame" {
		t.Fatalf("missing = %v, want first_frame", missing)
	}
}

// A value the model would refuse is brought back to the nearest bound it
// accepts, so the caller's intent survives the round trip.
func TestBuildPayloadClampsAValueToTheModelsRange(t *testing.T) {
	model := Model{ModelID: "image", Parameters: []Parameter{
		{Name: "prompt", Type: "string", Required: true},
		{Name: "guidance_scale", Type: "float", Minimum: price(1), Maximum: price(10)},
	}}
	payload, dropped := model.BuildPayload("a cat", map[string]any{"guidance_scale": float64(20)})
	if len(dropped) != 0 {
		t.Fatalf("dropped = %v, want nothing", dropped)
	}
	if payload["guidance_scale"] != float64(10) {
		t.Fatalf("guidance_scale = %v, want the model's largest accepted value", payload["guidance_scale"])
	}
	payload, _ = model.BuildPayload("a cat", map[string]any{"guidance_scale": float64(0)})
	if payload["guidance_scale"] != float64(1) {
		t.Fatalf("guidance_scale = %v, want the model's smallest accepted value", payload["guidance_scale"])
	}
}

func TestResolveOptionFindsTheNameTheModelUses(t *testing.T) {
	model := Model{Parameters: []Parameter{{Name: "durationSeconds"}}}
	if got := model.ResolveOption("duration"); got != "durationSeconds" {
		t.Fatalf("ResolveOption(duration) = %q, want durationSeconds", got)
	}
	if got := model.ResolveOption("mask"); got != "" {
		t.Fatalf("ResolveOption(mask) = %q, want none", got)
	}
}
