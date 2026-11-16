package modelcatalog

import (
	"math"
	"strconv"
	"strings"
)

// Catalogs use these explicit token counts; no model-name or pricing lookup is
// used to fill gaps. Table suffixes K/M are the CLI's decimal token units.
func catalogTokenCount(value any) int64 {
	var count float64
	switch v := value.(type) {
	case float64:
		count = v
	case string:
		text := strings.ToUpper(strings.TrimSpace(v))
		scale := float64(1)
		if strings.HasSuffix(text, "K") {
			scale = 1_000
			text = strings.TrimSuffix(text, "K")
		} else if strings.HasSuffix(text, "M") {
			scale = 1_000_000
			text = strings.TrimSuffix(text, "M")
		}
		parsed, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return 0
		}
		count = parsed * scale
	default:
		return 0
	}
	if math.IsNaN(count) || math.IsInf(count, 0) || count <= 0 || count >= math.MaxInt64 || math.Trunc(count) != count {
		return 0
	}
	return int64(count)
}

func catalogContextWindow(node map[string]any) int64 {
	for _, key := range []string{"contextWindow", "context_window", "contextLength", "context_length"} {
		if value := catalogTokenCount(node[key]); value > 0 {
			return value
		}
	}
	if limit, ok := node["limit"].(map[string]any); ok {
		return catalogTokenCount(limit["context"])
	}
	return 0
}

func modelInputs(values []string) []string {
	var inputs []string
	for _, wanted := range []string{"text", "image"} {
		for _, value := range values {
			if value == wanted {
				inputs = append(inputs, wanted)
				break
			}
		}
	}
	return inputs
}

func catalogInputs(node map[string]any) []string {
	for _, key := range []string{"inputs", "input", "inputModalities", "input_modalities"} {
		if values, ok := node[key].([]any); ok {
			var inputs []string
			for _, value := range values {
				if text, ok := value.(string); ok {
					inputs = append(inputs, text)
				}
			}
			return modelInputs(inputs)
		}
	}
	if modalities, ok := node["modalities"].(map[string]any); ok {
		return catalogInputs(modalities)
	}
	return nil
}
