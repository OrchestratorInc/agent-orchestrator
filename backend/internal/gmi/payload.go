package gmi

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Building a payload for a media model.
//
// The parameter names are not consistent across GMI's models. One image model
// takes "image" as its input image, another "image_path", another "images"; a
// video model spells the duration "duration", "durationSeconds" or "seconds".
// So a payload is built from the model's own published schema, and a
// caller's options are mapped onto it by meaning rather than by name.
// Anything the model does not accept is reported as dropped rather than
// silently ignored.

// optionAliases maps a semantic option onto the parameter names a model might
// use for it. The first name the model actually declares wins.
var optionAliases = map[string][]string{
	"aspect_ratio":     {"aspect_ratio", "aspectRatio", "ratio", "size", "image_size"},
	"size":             {"size", "image_size", "resolution"},
	"width":            {"width"},
	"height":           {"height"},
	"duration":         {"duration", "durationSeconds", "duration_seconds", "seconds", "length"},
	"resolution":       {"resolution", "size"},
	"image":            {"image", "image_path", "images", "input_reference", "reference_image", "image_url", "first_frame_image", "first_frame", "image_path_url"},
	"mask":             {"mask", "mask_image", "mask_path"},
	"negative_prompt":  {"negative_prompt", "negativePrompt"},
	"quality":          {"quality"},
	"n":                {"n", "max_images", "num_images", "number_of_images"},
	"seed":             {"seed"},
	"voice":            {"voice", "voice_id"},
	"text":             {"text", "prompt"},
	"style":            {"style", "style_id"},
	"output_format":    {"output_format", "format", "image_output_format"},
	"steps":            {"num_inference_steps", "steps"},
	"guidance":         {"guidance_scale", "guidance"},
	"fps":              {"fps"},
	"speed":            {"speed"},
	"prompt_optimizer": {"prompt_optimizer"},
	"generate_audio":   {"generate_audio", "generateAudio", "generate_audio_switch"},
}

// aspectSizes maps an aspect ratio onto the pixel size that expresses it.
var aspectSizes = map[string]string{
	"1:1":    "1024x1024",
	"square": "1024x1024",
	"4:3":    "1152x864",
	"3:4":    "864x1152",
	"3:2":    "1216x832",
	"2:3":    "832x1216",
	"16:9":   "1344x768",
	"9:16":   "768x1344",
	"21:9":   "1536x656",
}

// ParameterNames returns the names of the parameters the model declares.
func (m Model) ParameterNames() []string {
	names := make([]string, 0, len(m.Parameters))
	for _, parameter := range m.Parameters {
		names = append(names, parameter.Name)
	}
	return names
}

// Parameter returns the model's parameter of that name, or nil.
func (m Model) Parameter(name string) *Parameter {
	for index := range m.Parameters {
		if m.Parameters[index].Name == name {
			return &m.Parameters[index]
		}
	}
	return nil
}

// ResolveOption returns the parameter name this model uses for a semantic
// option, or "" when the model declares no such parameter.
func (m Model) ResolveOption(semantic string) string {
	candidates, ok := optionAliases[semantic]
	if !ok {
		candidates = []string{semantic}
	}
	for _, candidate := range candidates {
		if m.Parameter(candidate) != nil {
			return candidate
		}
	}
	return ""
}

// BuildPayload builds the payload for one generation of this model.
//
// It returns the payload and the options the model's schema did not accept.
// The prompt goes into whichever parameter the model calls the prompt, and
// the model's own defaults fill in for anything the caller left out.
func (m Model) BuildPayload(prompt string, options map[string]any) (map[string]any, []string) {
	names := m.ParameterNames()
	dropped := []string{}
	payload := map[string]any{}

	promptParameter := m.ResolveOption("text")
	if promptParameter == "" {
		switch {
		case contains(names, "prompt"):
			promptParameter = "prompt"
		case len(names) > 0:
			promptParameter = names[0]
		default:
			promptParameter = "prompt"
		}
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, []string{"prompt"}
	}
	payload[promptParameter] = strings.TrimSpace(prompt)

	for _, semantic := range sortedKeys(options) {
		value := options[semantic]
		if value == nil {
			continue
		}
		target := m.ResolveOption(semantic)
		if target == "" {
			dropped = append(dropped, semantic)
			continue
		}
		parameter := m.Parameter(target)
		if parameter == nil {
			dropped = append(dropped, semantic)
			continue
		}
		final, ok := mapOptionValue(parameter, semantic, value)
		if !ok {
			dropped = append(dropped, semantic)
			continue
		}
		payload[target] = final
	}

	for _, parameter := range m.Parameters {
		if _, ok := payload[parameter.Name]; ok {
			continue
		}
		if parameter.Required {
			continue
		}
		if parameter.Default != nil && parameter.Default != "" {
			payload[parameter.Name] = parameter.Default
		}
	}
	return payload, dropped
}

// MissingRequired returns the required parameters the payload does not
// supply.
func (m Model) MissingRequired(payload map[string]any) []string {
	var missing []string
	for _, parameter := range m.Parameters {
		if !parameter.Required {
			continue
		}
		if value, ok := payload[parameter.Name]; !ok || isEmptyValue(value) {
			missing = append(missing, parameter.Name)
		}
	}
	return missing
}

// mapOptionValue maps one option onto the value the model expects for it,
// coercing it to the parameter's declared type. ok is false when the value
// cannot be expressed for that parameter.
func mapOptionValue(parameter *Parameter, semantic string, value any) (any, bool) {
	final := value
	switch semantic {
	case "aspect_ratio":
		if len(parameter.Options) > 0 {
			mapped := sizeForAspect(parameter, fmt.Sprintf("%v", value))
			if mapped == "" {
				return nil, false
			}
			final = mapped
		} else if parameter.Name == "size" || parameter.Name == "image_size" || parameter.Name == "resolution" {
			mapped := aspectSizes[strings.ToLower(fmt.Sprintf("%v", value))]
			if mapped == "" {
				mapped = fmt.Sprintf("%v", value)
			}
			final = mapped
		}
	}
	final, ok := coerce(parameter, final)
	if !ok {
		return nil, false
	}
	return clampToRange(parameter, final), true
}

// coerce converts a value to the parameter's declared type. A value that
// cannot be converted is dropped rather than sent on and rejected.
func coerce(parameter *Parameter, value any) (any, bool) {
	switch parameter.Type {
	case "integer", "int":
		switch typed := value.(type) {
		case float64:
			return int(typed), true
		case int:
			return typed, true
		case string:
			parsed, err := strconv.Atoi(strings.TrimSpace(typed))
			if err != nil {
				return nil, false
			}
			return parsed, true
		}
		return nil, false
	case "number", "float":
		switch typed := value.(type) {
		case float64:
			return typed, true
		case int:
			return float64(typed), true
		case string:
			parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
			if err != nil {
				return nil, false
			}
			return parsed, true
		}
		return nil, false
	case "boolean", "bool":
		switch typed := value.(type) {
		case bool:
			return typed, true
		case string:
			switch strings.ToLower(strings.TrimSpace(typed)) {
			case "1", "true", "yes", "on":
				return true, true
			case "0", "false", "no", "off":
				return false, true
			}
			return nil, false
		}
		return nil, false
	case "enum":
		text := fmt.Sprintf("%v", value)
		if len(parameter.Options) == 0 {
			// The model names no allowed values, so there is nothing to
			// check the value against.
			return text, true
		}
		for _, option := range parameter.Options {
			if option == text {
				return text, true
			}
		}
		for _, option := range parameter.Options {
			if strings.EqualFold(option, text) {
				return option, true
			}
		}
		return nil, false
	}
	return value, true
}

// clampToRange brings a number inside the bounds the model publishes for
// that parameter. A value the model would reject is brought back to the
// nearest bound it accepts, so the caller's intent survives the round trip.
func clampToRange(parameter *Parameter, value any) any {
	switch typed := value.(type) {
	case float64:
		return clampNumber(typed, parameter.Minimum, parameter.Maximum)
	case int:
		clamped := clampNumber(float64(typed), parameter.Minimum, parameter.Maximum)
		return int(clamped)
	}
	return value
}

// clampNumber brings one number inside its bounds.
func clampNumber(value float64, minimum, maximum *float64) float64 {
	if minimum != nil && value < *minimum {
		value = *minimum
	}
	if maximum != nil && value > *maximum {
		value = *maximum
	}
	return value
}

// sizeForAspect maps an aspect ratio onto a size the model actually accepts,
// picking the allowed size whose ratio is closest to the request.
func sizeForAspect(parameter *Parameter, aspect string) string {
	preferred := aspectSizes[strings.ToLower(strings.TrimSpace(aspect))]
	if len(parameter.Options) == 0 {
		return preferred
	}
	if preferred != "" && contains(parameter.Options, preferred) {
		return preferred
	}
	if contains(parameter.Options, aspect) {
		return aspect
	}
	target := ratioValue(aspect)
	best := ""
	bestDistance := math.MaxFloat64
	for _, option := range parameter.Options {
		width, height, ok := strings.Cut(strings.ToLower(option), "x")
		if !ok {
			continue
		}
		parsedWidth, widthErr := strconv.ParseFloat(width, 64)
		parsedHeight, heightErr := strconv.ParseFloat(height, 64)
		if widthErr != nil || heightErr != nil || parsedHeight == 0 {
			continue
		}
		distance := math.Abs(parsedWidth/parsedHeight - target)
		if distance < bestDistance {
			bestDistance = distance
			best = option
		}
	}
	return best
}

// ratioValue returns an aspect ratio as a number.
func ratioValue(aspect string) float64 {
	width, height, ok := strings.Cut(aspect, ":")
	if !ok {
		return 1
	}
	parsedWidth, widthErr := strconv.ParseFloat(strings.TrimSpace(width), 64)
	parsedHeight, heightErr := strconv.ParseFloat(strings.TrimSpace(height), 64)
	if widthErr != nil || heightErr != nil || parsedHeight == 0 {
		return 1
	}
	return parsedWidth / parsedHeight
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func isEmptyValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	}
	return false
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
